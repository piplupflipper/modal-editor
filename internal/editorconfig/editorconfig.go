// Package editorconfig は、開いたファイルに当たる .editorconfig を探して読み、ファイルに当たる項目を返す（FR-164。基本設計 7.3）。
//
// ファイルの探し方、セクションの glob の書き方、項目の書き方は、EditorConfig の仕様（https://spec.editorconfig.org/）に合わせる。
// glob の解釈は、外部ライブラリを足さずにこのパッケージで書いた（Match。仕様の glob の例は editorconfig_test.go にある）。
// どの項目を本体の設定にどう写すかは、呼び出し側（internal/editor）が決める。このパッケージは項目の名前と値の文字列を返すだけである。
//
// 基本設計 2 章の土台の層に置き、他のパッケージを import しない。
//
// # 探し方
//
// ファイルのディレクトリから親のディレクトリへ順に .editorconfig を探す。近いファイルの値が、遠いファイルの値に勝つ。
// 前文（最初のセクションの前）に root = true と書いたファイルで探すのを止める。
// 1 つのファイルの中では、ファイルに当たるセクションのうち後に書いたものの値が勝つ。
// 値が unset の項目は、そのまま "unset" として返す（遠いファイルの値を打ち消すためで、使う側は書かなかったものとして扱う）。
//
// # 書き方
//
//   - 空の行と、先頭（空白を除く）が # か ; の行は読み飛ばす
//   - [<glob>] はセクションの始まりである
//   - <名前> = <値> は項目である。名前と値の前後の空白は除き、どちらも小文字にする（Neovim 0.12.5 の runtime/lua/editorconfig.lua と同じ）
//   - 読めない行は読み飛ばす
package editorconfig

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

// FileName は、探すファイルの名前である。
const FileName = ".editorconfig"

// Unset は、項目の効き目を打ち消す値である（仕様の unset）。
const Unset = "unset"

// Properties は、ファイルに当たる項目の、名前から値への対応である。名前と値は小文字にしてある。
type Properties map[string]string

// File は、1 つの .editorconfig を読んだものである。
type File struct {
	// Root は、前文に root = true と書いたかである。true なら、親のディレクトリの .editorconfig を探さない。
	Root bool
	// Sections は、セクションを書いた順に並べたものである。
	Sections []Section
}

// Section は、1 つのセクションである。
type Section struct {
	// Glob は、セクションの名前（[] の中）である。
	Glob string
	// Props は、セクションの項目を書いた順に並べたものである。
	Props []Prop
}

// Prop は、1 つの項目である。Name と Value は小文字にしてある。
type Prop struct {
	Name, Value string
}

// Parse は、data を .editorconfig として読む。読めない行は読み飛ばすので、誤りを返さない。
func Parse(data []byte) File {
	var f File
	inSection := false
	for line := range strings.Lines(string(data)) {
		line = strings.TrimSpace(line)
		switch {
		case line == "" || line[0] == '#' || line[0] == ';':
			continue
		case line[0] == '[' && line[len(line)-1] == ']' && len(line) >= 2:
			f.Sections = append(f.Sections, Section{Glob: line[1 : len(line)-1]})
			inSection = true
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		name = strings.ToLower(strings.TrimSpace(name))
		value = strings.ToLower(strings.TrimSpace(value))
		if name == "" {
			continue
		}
		if !inSection {
			// 前文で意味を持つのは root だけである。
			if name == "root" {
				f.Root = value == "true"
			}
			continue
		}
		s := &f.Sections[len(f.Sections)-1]
		s.Props = append(s.Props, Prop{Name: name, Value: value})
	}
	return f
}

// Properties は、.editorconfig のあるディレクトリからの相対パス rel（区切りは /）のファイルに当たる項目を返す。
// 当たるセクションのうち、後に書いたものの値が勝つ。
func (f File) Properties(rel string) Properties {
	props := Properties{}
	for _, s := range f.Sections {
		if !sectionMatches(s.Glob, rel) {
			continue
		}
		for _, p := range s.Props {
			props[p.Name] = p.Value
		}
	}
	return props
}

// sectionMatches は、セクションの名前 glob が、.editorconfig のあるディレクトリからの相対パス rel に当たるかを返す。
// glob が / を含めば（角括弧の中の / は数えない）、.editorconfig のあるディレクトリからのパスとして当てる（先頭の / は除く）。
// 含まなければ、どの深さのファイルの名前にも当てる（仕様の「/ を含まない glob は、どの階層のファイルにも当たる」）。
func sectionMatches(glob, rel string) bool {
	if hasSeparator(glob) {
		return Match(strings.TrimPrefix(glob, "/"), rel)
	}
	return Match("**/"+glob, "/"+rel)
}

// hasSeparator は、glob が角括弧の外に / を含むかを返す。
func hasSeparator(glob string) bool {
	in := false
	for i := 0; i < len(glob); i++ {
		switch glob[i] {
		case '\\':
			i++
		case '[':
			in = true
		case ']':
			in = false
		case '/':
			if !in {
				return true
			}
		}
	}
	return false
}

// readFile は、.editorconfig を読む関数である。テストが差し替える。
var readFile = os.ReadFile

// Lookup は、絶対パス path のファイルに当たる項目を、ファイルのディレクトリから親のディレクトリへ .editorconfig を探して返す（パッケージの doc）。
// 当たる項目が無ければ空の Properties を返す。.editorconfig が無いディレクトリは読み飛ばす。
// 読めない .editorconfig（権限が無いなど）があれば、そのファイルを読み飛ばして、最初の誤りを返す（ほかのファイルの項目は返す）。
//
// req: FR-164
func Lookup(path string) (Properties, error) {
	props := Properties{}
	var firstErr error
	dir := filepath.Dir(path)
	for {
		data, err := readFile(filepath.Join(dir, FileName))
		switch {
		case err == nil:
			rel, rerr := filepath.Rel(dir, path)
			if rerr == nil {
				f := Parse(data)
				for k, v := range f.Properties(filepath.ToSlash(rel)) {
					if _, ok := props[k]; !ok {
						props[k] = v
					}
				}
				if f.Root {
					return props, firstErr
				}
			}
		case !errors.Is(err, fs.ErrNotExist) && firstErr == nil:
			firstErr = err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return props, firstErr
		}
		dir = parent
	}
}

// Match は、EditorConfig の glob（仕様の Glob Expressions）が、パス name の全体に当たるかを返す。区切りは / である。
//
//   - * は / 以外の 0 文字以上、** は / を含む 0 文字以上、? は / 以外の 1 文字に当たる。/**/ は / にも当たる
//   - [seq] は seq の 1 文字（a-z の範囲を書ける）、[!seq] は seq でない 1 文字に当たる。どちらも / には当たらない
//   - {s1,s2,s3} は、どれかの文字列に当たる（入れ子にできる）。, を含まない {s} は、文字どおりの {s} に当たる
//   - {num1..num2} は、num1 から num2 までの整数（負の数を含む）に当たる
//   - \ は次の 1 文字を文字どおりにする
//   - 閉じていない [ と { は、文字どおりの文字として扱う
//
// req: FR-164
func Match(glob, name string) bool {
	nodes, _ := parseGlob(glob, false)
	return matchNodes(nodes, name)
}

// nodeKind は、glob の 1 つの要素の種類である。
type nodeKind int

const (
	nodeLiteral    nodeKind = iota // 1 文字
	nodeStar                       // *
	nodeDoubleStar                 // **
	nodeQuestion                   // ?
	nodeClass                      // [seq]、[!seq]
	nodeAlt                        // {s1,s2}
	nodeRange                      // {num1..num2}
)

// node は、glob の 1 つの要素である。
type node struct {
	kind nodeKind
	r    rune // nodeLiteral の文字
	// neg と ranges は、nodeClass の否定と、当たる文字の範囲（両端を含む）の列である。
	neg    bool
	ranges [][2]rune
	// alts は、nodeAlt の選択肢の列である。
	alts [][]node
	// lo と hi は、nodeRange の範囲（両端を含む）である。
	lo, hi int
}

// parseGlob は、glob を要素の列にする。inBrace なら、選択肢の中として、対応する , か } の手前で止め、読んだバイト数を返す。
func parseGlob(glob string, inBrace bool) ([]node, int) {
	var nodes []node
	i := 0
	for i < len(glob) {
		c := glob[i]
		switch {
		case inBrace && (c == ',' || c == '}'):
			return nodes, i
		case c == '\\' && i+1 < len(glob):
			r, size := utf8.DecodeRuneInString(glob[i+1:])
			nodes = append(nodes, node{kind: nodeLiteral, r: r})
			i += 1 + size
			continue
		case c == '*':
			if i+1 < len(glob) && glob[i+1] == '*' {
				nodes = append(nodes, node{kind: nodeDoubleStar})
				i += 2
				for i < len(glob) && glob[i] == '*' {
					i++
				}
				continue
			}
			nodes = append(nodes, node{kind: nodeStar})
			i++
			continue
		case c == '?':
			nodes = append(nodes, node{kind: nodeQuestion})
			i++
			continue
		case c == '[':
			if n, size, ok := parseClass(glob[i:]); ok {
				nodes = append(nodes, n)
				i += size
				continue
			}
		case c == '{':
			if n, size, ok := parseBrace(glob[i:]); ok {
				nodes = append(nodes, n)
				i += size
				continue
			}
		}
		r, size := utf8.DecodeRuneInString(glob[i:])
		nodes = append(nodes, node{kind: nodeLiteral, r: r})
		i += size
	}
	return zeroDirs(nodes), i
}

// zeroDirs は、/**/ の並び（/ の文字、**、/ の文字）を、/ にも当たる選択肢にする（a/**/b は a/b にも当たる）。
func zeroDirs(nodes []node) []node {
	for i := 0; i+2 < len(nodes); i++ {
		if isSlash(nodes[i]) && nodes[i+1].kind == nodeDoubleStar && isSlash(nodes[i+2]) {
			alt := node{kind: nodeAlt, alts: [][]node{
				{{kind: nodeLiteral, r: '/'}},
				{{kind: nodeLiteral, r: '/'}, {kind: nodeDoubleStar}, {kind: nodeLiteral, r: '/'}},
			}}
			nodes = append(nodes[:i:i], append([]node{alt}, nodes[i+3:]...)...)
		}
	}
	return nodes
}

// isSlash は、n が文字 / かを返す。
func isSlash(n node) bool { return n.kind == nodeLiteral && n.r == '/' }

// parseClass は、s の先頭の [ から始まる文字の集まりを読む。閉じていない、または中に / があれば ok は false である。
func parseClass(s string) (node, int, bool) {
	n := node{kind: nodeClass}
	i := 1
	if i < len(s) && s[i] == '!' {
		n.neg = true
		i++
	}
	first := true
	for i < len(s) {
		if s[i] == ']' && !first {
			return n, i + 1, len(n.ranges) > 0
		}
		first = false
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == '\\' && i+size < len(s) {
			i += size
			r, size = utf8.DecodeRuneInString(s[i:])
		}
		if r == '/' {
			return node{}, 0, false
		}
		i += size
		lo, hi := r, r
		if i+1 < len(s) && s[i] == '-' && s[i+1] != ']' {
			r2, size2 := utf8.DecodeRuneInString(s[i+1:])
			hi = r2
			i += 1 + size2
		}
		n.ranges = append(n.ranges, [2]rune{lo, hi})
	}
	return node{}, 0, false
}

// parseBrace は、s の先頭の { から始まる選択肢か整数の範囲を読む。閉じていない、または , も .. も無ければ ok は false である。
func parseBrace(s string) (node, int, bool) {
	end := closingBrace(s)
	if end < 0 {
		return node{}, 0, false
	}
	inner := s[1:end]
	if lo, hi, ok := numRange(inner); ok {
		return node{kind: nodeRange, lo: min(lo, hi), hi: max(lo, hi)}, end + 1, true
	}
	n := node{kind: nodeAlt}
	i := 1
	for {
		alt, size := parseGlob(s[i:end], true)
		n.alts = append(n.alts, alt)
		i += size
		if i >= end {
			break
		}
		i++ // ,
	}
	if len(n.alts) < 2 {
		// , を含まない {s} は、文字どおりの { と } で s を囲んだものに当たる（中の s は glob として読む）。
		// 選択肢の中の {also} のように入れ子でも、閉じた } を外側の選択肢の終わりと取り違えない（EditorConfig の公式のテストの
		// [{word,{also},this}.g] は {also}.g に当たる。PR #324 のレビューの推奨 3）。
		lit := append([]node{{kind: nodeLiteral, r: '{'}}, n.alts[0]...)
		lit = append(lit, node{kind: nodeLiteral, r: '}'})
		return node{kind: nodeAlt, alts: [][]node{lit}}, end + 1, true
	}
	return n, end + 1, true
}

// closingBrace は、s の先頭の { に対応する } の位置を返す。無ければ -1 を返す。
func closingBrace(s string) int {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// numRange は、s が num1..num2 の形なら、2 つの整数を返す。
func numRange(s string) (lo, hi int, ok bool) {
	a, b, found := strings.Cut(s, "..")
	if !found {
		return 0, 0, false
	}
	lo, err1 := strconv.Atoi(a)
	hi, err2 := strconv.Atoi(b)
	if err1 != nil || err2 != nil || strings.HasPrefix(a, "+") || strings.HasPrefix(b, "+") {
		return 0, 0, false
	}
	return lo, hi, true
}

// matchNodes は、要素の列 nodes が s の全体に当たるかを返す。
func matchNodes(nodes []node, s string) bool {
	if len(nodes) == 0 {
		return s == ""
	}
	n, rest := nodes[0], nodes[1:]
	switch n.kind {
	case nodeLiteral:
		r, size := utf8.DecodeRuneInString(s)
		return size > 0 && r == n.r && matchNodes(rest, s[size:])
	case nodeQuestion:
		r, size := utf8.DecodeRuneInString(s)
		return size > 0 && r != '/' && matchNodes(rest, s[size:])
	case nodeClass:
		r, size := utf8.DecodeRuneInString(s)
		return size > 0 && r != '/' && n.contains(r) != n.neg && matchNodes(rest, s[size:])
	case nodeStar, nodeDoubleStar:
		for i := 0; ; {
			if matchNodes(rest, s[i:]) {
				return true
			}
			if i >= len(s) || (n.kind == nodeStar && s[i] == '/') {
				return false
			}
			_, size := utf8.DecodeRuneInString(s[i:])
			i += size
		}
	case nodeAlt:
		for _, alt := range n.alts {
			if matchNodes(append(append([]node{}, alt...), rest...), s) {
				return true
			}
		}
		return false
	case nodeRange:
		for i := 1; i <= len(s); i++ {
			v, err := strconv.Atoi(s[:i])
			if err != nil || s[0] == '+' || leadingZero(s[:i]) {
				continue
			}
			if v >= n.lo && v <= n.hi && matchNodes(rest, s[i:]) {
				return true
			}
		}
		return false
	}
	return false
}

// leadingZero は、整数の文字列 s（先頭の - を許す）の数字が、2 桁以上で 0 から始まるかを返す。
// そのような文字列は {num1..num2} に当たる整数として読まない（EditorConfig の公式のテストの [{3..120}] は 060 に当たらない）。
func leadingZero(s string) bool {
	s = strings.TrimPrefix(s, "-")
	return len(s) > 1 && s[0] == '0'
}

// contains は、文字の集まり n の範囲に r が入るかを返す。
func (n node) contains(r rune) bool {
	for _, rg := range n.ranges {
		if r >= rg[0] && r <= rg[1] {
			return true
		}
	}
	return false
}
