// Package comment は、行のコメントの形（設定 comment.string。Vim の 'commentstring'）を読み、行の並びのコメントを切り替える
// （FR-169。基本設計 3.3）。
//
// 振る舞いは Neovim 0.12.5 の組み込みの gc（runtime/lua/vim/_comment.lua）を写した。
// Lua のパターンの照合（vim._comment の make_comment_check と make_uncomment_function）は、同じ形の Go の正規表現に直した。
// Lua の %s（空白）は、Go の \s と違って垂直タブを含むので、ASCII の 6 文字の集合（space）で書いた。
// 両端を ^ と $ で止めた照合で、Go の regexp の左から優先する照合（RE2 の leftmost-first）は、Lua の後戻りの照合と同じ部分一致を返す。
// Lua の照合はバイト単位で、Go は UTF-8 の文字単位だが、パターンの文字の集合は ASCII だけなので、結果は同じになる
// （全角の空白 U+3000 は、どちらでも空白にならない）。
//
// このパッケージは他のパッケージを import しない。コメントの形をどこから決めるか（バッファの設定、後に tree-sitter の拡張機能）は、
// 呼び出し側（internal/command の State.commentString）が 1 か所で決める。
package comment

import (
	"errors"
	"regexp"
	"strings"
)

// space は、Lua のパターンの %s（空白）と同じ文字の集合である。
const space = `[ \t\n\v\f\r]`

// ErrEmpty は、コメントの形が空であることを表す。gc は、Neovim と同じく、前と後ろを空としてそのまま続ける（Parse）。
var ErrEmpty = errors.New("comment: comment string is empty")

// ErrInvalid は、コメントの形が空でなく、%s を含まないことを表す。設定の表の検査（option.Def.Contains）が断るので、ふつうは起きない。
var ErrInvalid = errors.New("comment: comment string does not contain %s")

// Parts は、コメントの形を、%s の前（Left）と後ろ（Right）に分けたものである（Neovim の vim._comment.Parts）。
// ゼロ値は、前と後ろが空の形で、Neovim の 'commentstring' が空のときの形と同じである。
type Parts struct {
	Left, Right string
}

// Parse は、コメントの形 cs を、最初の %s の前と後ろに分ける（"// %s" は "// " と ""、"# %s %s" は "# " と " %s"）。
// cs が空なら、ゼロ値と ErrEmpty を返す。Neovim は、空の 'commentstring' で警告を出し、前と後ろを空として続けるので、
// 呼び出し側は ErrEmpty のときも返った Parts で続けてよい。cs が %s を含まなければ ErrInvalid を返す。
//
// req: FR-169
func Parse(cs string) (Parts, error) {
	if cs == "" {
		return Parts{}, ErrEmpty
	}
	left, right, ok := strings.Cut(cs, "%s")
	if !ok {
		return Parts{}, ErrInvalid
	}
	return Parts{Left: left, Right: right}, nil
}

// HasMarker は、前と後ろの、両端の空白を除いた部分のどちらかが空でないかを返す。
// 空なら、どの行もコメントの行に当たる（Checker）。
func (p Parts) HasMarker() bool {
	return trim(p.Left) != "" || trim(p.Right) != ""
}

// trim は、Neovim の vim.trim と同じく、両端の空白（space）を除く。
func trim(s string) string { return strings.Trim(s, " \t\n\v\f\r") }

// checker は、コメントの行かを確かめる正規表現を返す（Neovim の make_comment_check）。
// コメントの行は、空白、両端の空白を除いた前、任意の文字、両端の空白を除いた後ろ、空白の並びである。
func (p Parts) checker() *regexp.Regexp {
	return regexp.MustCompile(`(?s)^` + space + `*?` + regexp.QuoteMeta(trim(p.Left)) + `.*` + regexp.QuoteMeta(trim(p.Right)) + space + `*?$`)
}

// Checker は、行がコメントの行かを返す関数を返す（Neovim の make_comment_check）。
// 前と後ろが空の形（HasMarker が false）では、どの行もコメントの行である。
//
// req: FR-169
func (p Parts) Checker() func(line string) bool {
	return p.checker().MatchString
}

// blank は、行が空白だけ（空を含む）かを返す。
func blank(line string) bool { return trim(line) == "" }

// leading は、行の先頭の空白の長さ（バイト数）を返す。
func leading(line string) int { return len(line) - len(strings.TrimLeft(line, " \t\n\v\f\r")) }

// Toggle は、行の並び lines のコメントを切り替えた行の並びを返す（Neovim の toggle_lines）。lines は変えない。
//
// 空白だけの行を除いたすべての行がコメントの行（Checker）なら、コメントを外す。そうでなければ、すべての行をコメントにする。
// 空白だけの行しか無ければ、コメントを外す側になる。
//
//   - コメントにするときは、空白だけでない行のうち、先頭の空白のバイト数が最も少ない行の先頭の空白（タブ文字とスペースをそのまま写す）を
//     字下げとし、各行の字下げの後に前を、行末に後ろを付ける。各行の先頭から字下げのバイト数を除いて付けるので、
//     タブ文字とスペースの混ざった字下げは、Neovim と同じくバイト数で揃う。空白だけの行は、字下げと、両端の空白を除いた前と後ろだけの行にする
//   - コメントを外すときは、行の先頭の空白の後の前と、行末の空白の前の後ろを外す。前と後ろがそのままで当たらなければ、
//     両端の空白を除いた前と後ろで外す（"// %s" で "//a" を外す）。どちらにも当たらない行はそのまま残す。
//     外した後が空白だけなら、字下げと行末の空白も除く
//
// req: FR-169
func (p Parts) Toggle(lines []string) []string {
	check := p.checker()
	indent, commented := "", true
	minIndent := -1
	for _, l := range lines {
		if blank(l) {
			continue
		}
		if n := leading(l); minIndent < 0 || n < minIndent {
			minIndent, indent = n, l[:n]
		}
		if commented {
			commented = check.MatchString(l)
		}
	}
	out := make([]string, len(lines))
	if commented {
		exact := uncommenter(p.Left, p.Right)
		trimmed := uncommenter(trim(p.Left), trim(p.Right))
		for i, l := range lines {
			out[i] = uncomment(l, exact, trimmed)
		}
		return out
	}
	blankLine := indent + trim(p.Left) + trim(p.Right)
	for i, l := range lines {
		if blank(l) {
			out[i] = blankLine
			continue
		}
		out[i] = indent + p.Left + l[len(indent):] + p.Right
	}
	return out
}

// uncommenter は、前 left と後ろ right のコメントを外す正規表現を返す（Neovim の make_uncomment_function の regex）。
// 部分一致は、字下げ、中身、行末の空白の 3 つである。
func uncommenter(left, right string) *regexp.Regexp {
	return regexp.MustCompile(`(?s)^(` + space + `*)` + regexp.QuoteMeta(left) + `(.*)` + regexp.QuoteMeta(right) + `(` + space + `*?)$`)
}

// uncomment は、行 line のコメントを、exact か、当たらなければ trimmed で外す。どちらにも当たらなければ line を返す。
func uncomment(line string, exact, trimmed *regexp.Regexp) string {
	m := exact.FindStringSubmatch(line)
	if m == nil {
		m = trimmed.FindStringSubmatch(line)
	}
	if m == nil {
		return line
	}
	indent, body, trail := m[1], m[2], m[3]
	if blank(body) {
		// Neovim は、外した後が空白だけなら、行末の空白を残さないように字下げと行末の空白を除く（中身の空白は残す）。
		indent, trail = "", ""
	}
	return indent + body + trail
}
