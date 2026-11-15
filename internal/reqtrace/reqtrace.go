// Package reqtrace は、要件定義の ID とコードを突き合わせて、進捗の一覧を作る。
//
// 要件定義（docs/requirements.md）の表から要件の ID を読み取り、
// Go のソースとテストと Makefile のコメントに書かれた ID の注釈を集めて、
// 要件ごとに「実装があるか」「テストがあるか」を導出する。
//
// 注釈は、コメントの行頭に "req:" と書き、その後に ID をカンマ区切りで並べる。
// 書き方の例は docs/basic-design.md の「要件との対応」を参照する。
package reqtrace

import (
	"bufio"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Requirement は、要件定義の表の 1 行を表す。
type Requirement struct {
	ID   string // 例: "FR-01"
	Text string // 要件の本文
}

// Ref は、コードのコメントに書かれた注釈 1 件を表す。
type Ref struct {
	ID     string // 注釈に書かれた要件の ID
	File   string // 走査の起点からの相対パス（区切りは "/"）
	Line   int    // 注釈のある行番号
	IsTest bool   // ファイル名が _test.go で終わる場合に true
}

// Status は、要件 1 件の進み具合を表す。
type Status int

// 進み具合は、注釈の有無だけから決まる。
// 実装が要件を満たしているかどうかは、この値からは分からない。
const (
	StatusNotStarted Status = iota // 注釈が 1 件も無い
	StatusImplOnly                 // 実装の注釈だけがある
	StatusTestOnly                 // テストの注釈だけがある
	StatusDone                     // 実装とテストの両方に注釈がある
)

// String は、進み具合の表示用の名前を返す。
func (s Status) String() string {
	switch s {
	case StatusNotStarted:
		return "未着手"
	case StatusImplOnly:
		return "実装のみ"
	case StatusTestOnly:
		return "テストのみ"
	case StatusDone:
		return "実装とテスト"
	}
	return fmt.Sprintf("Status(%d)", int(s))
}

// Entry は、要件 1 件と、その要件を指す注釈をまとめたものである。
type Entry struct {
	Requirement Requirement
	Impl        []Ref // テスト以外のファイルにある注釈
	Tests       []Ref // テストのファイルにある注釈
}

// Status は、注釈の有無から進み具合を返す。
func (e Entry) Status() Status {
	switch {
	case len(e.Impl) > 0 && len(e.Tests) > 0:
		return StatusDone
	case len(e.Impl) > 0:
		return StatusImplOnly
	case len(e.Tests) > 0:
		return StatusTestOnly
	}
	return StatusNotStarted
}

// Report は、全ての要件の進み具合である。
type Report struct {
	Entries []Entry // 要件定義に書かれた順
	Unknown []Ref   // 要件定義に無い ID を指す注釈
}

// Count は、指定した進み具合の要件の数を返す。
func (r Report) Count(s Status) int {
	n := 0
	for _, e := range r.Entries {
		if e.Status() == s {
			n++
		}
	}
	return n
}

var (
	// requirementRow は、要件定義の表の行（"| FR-01 | 本文 |"）に一致する。
	requirementRow = regexp.MustCompile(`^\|\s*(N?FR-\d+)\s*\|\s*(.*?)\s*\|\s*$`)
	// annotation は、コメントの 1 行が注釈であるかを判定する。
	annotation = regexp.MustCompile(`^req:\s*(.+)$`)
	// requirementID は、要件の ID の形式である。
	requirementID = regexp.MustCompile(`^N?FR-\d+$`)
)

// ParseRequirements は、要件定義の Markdown から要件の一覧を読み取る。
// 同じ ID が 2 回現れた場合はエラーを返す。
func ParseRequirements(r io.Reader) ([]Requirement, error) {
	var reqs []Requirement
	seen := make(map[string]bool)

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		m := requirementRow.FindStringSubmatch(scanner.Text())
		if m == nil {
			continue
		}
		id, text := m[1], m[2]
		if seen[id] {
			return nil, fmt.Errorf("要件の ID %s が重複している", id)
		}
		seen[id] = true
		reqs = append(reqs, Requirement{ID: id, Text: text})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("要件定義の読み込みに失敗: %w", err)
	}
	return reqs, nil
}

// Scan は、root 以下の Go のファイルと Makefile から注釈を集める。
// 名前が "." で始まるディレクトリと、testdata、vendor、bin は走査しない。
func Scan(root string) ([]Ref, error) {
	var refs []Ref

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		scan := scanFile
		switch {
		case strings.HasSuffix(d.Name(), ".go"):
		case d.Name() == "Makefile":
			scan = scanMakefile
		default:
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		found, err := scan(path, filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		refs = append(refs, found...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("ソースの走査に失敗: %w", err)
	}

	sort.Slice(refs, func(i, j int) bool {
		if refs[i].File != refs[j].File {
			return refs[i].File < refs[j].File
		}
		return refs[i].Line < refs[j].Line
	})
	return refs, nil
}

func skipDir(name string) bool {
	return strings.HasPrefix(name, ".") || name == "testdata" || name == "vendor" || name == "bin"
}

// scanFile は、Go のファイル 1 個のコメントから注釈を集める。
// 文字列の中に書かれた "req:" を拾わないように、Go のパーサーでコメントだけを取り出す。
func scanFile(path, rel string) ([]Ref, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	var refs []Ref
	isTest := strings.HasSuffix(rel, "_test.go")
	for _, group := range file.Comments {
		for _, comment := range group.List {
			startLine := fset.Position(comment.Pos()).Line
			// ブロックコメント（/* */）は複数行になるので、行ごとに調べる。
			for offset, line := range strings.Split(comment.Text, "\n") {
				for _, id := range parseAnnotation(line) {
					refs = append(refs, Ref{ID: id, File: rel, Line: startLine + offset, IsTest: isTest})
				}
			}
		}
	}
	return refs, nil
}

// scanMakefile は、Makefile のコメントから注釈を集める。
// ビルドの指定（静的リンク、cgo を使わないこと、配布物の形）で満たす要件は、Go のソースではなく Makefile が実装になる。
// 行頭が "#" の行だけを注釈の候補にする。タブで始まるレシピの行は、シェルに渡す内容なので拾わない。
// Makefile は実装として扱い、検証は Go のテストに書く。
func scanMakefile(path, rel string) ([]Ref, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var refs []Ref
	for i, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "#") {
			continue
		}
		for _, id := range parseAnnotation(strings.TrimPrefix(line, "#")) {
			refs = append(refs, Ref{ID: id, File: rel, Line: i + 1})
		}
	}
	return refs, nil
}

// parseAnnotation は、コメントの 1 行から要件の ID を取り出す。
// 注釈でない行と、ID の形式に合わない語は無視する。
func parseAnnotation(line string) []string {
	text := strings.TrimSpace(line)
	text = strings.TrimPrefix(text, "//")
	text = strings.TrimPrefix(text, "/*")
	text = strings.TrimSuffix(text, "*/")
	text = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text), "*"))

	m := annotation.FindStringSubmatch(text)
	if m == nil {
		return nil
	}

	var ids []string
	for _, field := range strings.Split(m[1], ",") {
		id := strings.TrimSpace(field)
		if requirementID.MatchString(id) {
			ids = append(ids, id)
		}
	}
	return ids
}

// Build は、要件の一覧と注釈の一覧を突き合わせる。
func Build(reqs []Requirement, refs []Ref) Report {
	index := make(map[string]int, len(reqs))
	entries := make([]Entry, len(reqs))
	for i, req := range reqs {
		index[req.ID] = i
		entries[i].Requirement = req
	}

	var unknown []Ref
	for _, ref := range refs {
		i, ok := index[ref.ID]
		switch {
		case !ok:
			unknown = append(unknown, ref)
		case ref.IsTest:
			entries[i].Tests = append(entries[i].Tests, ref)
		default:
			entries[i].Impl = append(entries[i].Impl, ref)
		}
	}
	return Report{Entries: entries, Unknown: unknown}
}
