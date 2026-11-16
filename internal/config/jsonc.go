package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"

	"github.com/tailscale/hujson"
)

// Error は、設定ファイルか宣言ファイルの 1 件の誤りである（基本設計 8 章の「位置と内容を知らせる」）。
// 画面に出すことを前提にした文言なので、日本語で書く。
type Error struct {
	// Path は、誤りのあるファイルのパスである。
	Path string
	// Line は、誤りの位置の行（1 始まり）である。位置が無い誤り（ファイルを読めないなど）では 0。
	Line int
	// Column は、誤りの位置の列（1 始まり）である。列はバイトで数える（Go のコンパイラ、hujson、Vim の col() と同じ）。
	// Line が 0 なら 0。
	Column int
	// Msg は、誤りの内容である。
	Msg string
}

// Error は、"パス:行:列: 内容" の形の文字列を返す。位置が無ければ "パス: 内容" である。
func (p *Error) Error() string {
	if p.Line == 0 {
		return fmt.Sprintf("%s: %s", p.Path, p.Msg)
	}
	return fmt.Sprintf("%s:%d:%d: %s", p.Path, p.Line, p.Column, p.Msg)
}

// source は、読んでいる JSONC のファイルである。
type source struct {
	path string
	// data は、ファイルの内容そのままである。位置（行と列）はこちらで数える。
	data []byte
	// std は、data のコメントと末尾のカンマを空白に置き換えた標準の JSON である。
	// hujson の Standardize はバイトの位置を変えないので、data の位置でそのまま切り出せる。
	std []byte
	// problems は、見つけた誤りである。
	problems []error
}

// parseJSONC は、data を JSONC として構文木にし、標準の JSON（std）を作る。
// 構文の誤りは、hujson が返す行と列を付けた Error で返す。
func (s *source) parseJSONC() (hujson.Value, error) {
	v, err := hujson.Parse(s.data)
	if err != nil {
		return v, s.syntaxError(err)
	}
	// Clone した木は末尾のカンマの印（空の AfterExtra）を失うので、もう一度読む。
	// Standardize は木が指す入力のバイト列を書き換えるので、data の写しを読ませる。
	std, err := hujson.Parse(bytes.Clone(s.data))
	if err != nil {
		return v, s.syntaxError(err)
	}
	std.Standardize()
	s.std = std.Pack()
	if len(s.std) != len(s.data) {
		// Standardize の文書の約束（位置を変えない）が破られたときに、誤った位置を出さないための確認である。
		return v, &Error{Path: s.path, Msg: "JSONC を標準の JSON に直せない"}
	}
	return v, nil
}

// syntaxRe は、hujson.Parse のエラーの文字列から位置を取り出す。
var syntaxRe = regexp.MustCompile(`^hujson: line (\d+), column (\d+): (.*)$`)

// syntaxError は、hujson.Parse のエラーを Error にする。
func (s *source) syntaxError(err error) *Error {
	m := syntaxRe.FindStringSubmatch(err.Error())
	if m == nil {
		return &Error{Path: s.path, Msg: "JSONC として読めない: " + err.Error()}
	}
	line, _ := strconv.Atoi(m[1])
	col, _ := strconv.Atoi(m[2])
	return &Error{Path: s.path, Line: line, Column: col, Msg: "JSONC として読めない: " + m[3]}
}

// at は、data のバイトの位置 off に Error を作る。
func (s *source) at(off int, format string, args ...any) *Error {
	off = min(max(off, 0), len(s.data))
	line := 1 + bytes.Count(s.data[:off], []byte("\n"))
	col := off - bytes.LastIndexByte(s.data[:off], '\n')
	return &Error{Path: s.path, Line: line, Column: col, Msg: fmt.Sprintf(format, args...)}
}

// report は、位置 off の誤りを記録する。
func (s *source) report(off int, format string, args ...any) {
	s.problems = append(s.problems, s.at(off, format, args...))
}

// raw は、v の標準の JSON の表現を返す。
func (s *source) raw(v *hujson.Value) []byte {
	return s.std[v.StartOffset:v.EndOffset]
}

// member は、オブジェクトの 1 つの項目である。
type member struct {
	name string
	// nameOff は、項目の名前の位置である。
	nameOff int
	value   *hujson.Value
}

// members は、obj の項目を並びの順に返す。
// 同じ名前の項目が重なったら、後のものを使い（encoding/json と同じ）、後の項目の位置に誤りを記録する。
// path は、誤りの文言に出す、obj の位置の名前（"keymap.normal" など。最上位は空）である。
func (s *source) members(obj *hujson.Object, path string) []member {
	out := make([]member, 0, len(obj.Members))
	index := map[string]int{}
	for i := range obj.Members {
		m := &obj.Members[i]
		name := m.Name.Value.(hujson.Literal).String()
		mb := member{name: name, nameOff: m.Name.StartOffset, value: &m.Value}
		if j, ok := index[name]; ok {
			s.report(mb.nameOff, "項目 %s が重なっている。後のものを使う", join(path, name))
			out[j] = mb
			continue
		}
		index[name] = len(out)
		out = append(out, mb)
	}
	return out
}

// plainName は、項目の位置の名前に "." でつないでも読み違えない名前である。
var plainName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// join は、項目の位置の名前をつなぐ。"." や記号を含む名前（".md"、"<Space>w"）は ["..."] の形でつなぐ。
func join(path, name string) string {
	switch {
	case !plainName.MatchString(name):
		return path + "[" + strconv.Quote(name) + "]"
	case path == "":
		return name
	}
	return path + "." + name
}

// object は、v がオブジェクトならそれを返す。そうでなければ誤りを記録して false を返す。
func (s *source) object(v *hujson.Value, path string) (*hujson.Object, bool) {
	if o, ok := v.Value.(*hujson.Object); ok {
		return o, true
	}
	s.report(v.StartOffset, "%s はオブジェクトにする", path)
	return nil, false
}

// array は、v が配列ならそれを返す。そうでなければ誤りを記録して false を返す。
func (s *source) array(v *hujson.Value, path string) (*hujson.Array, bool) {
	if a, ok := v.Value.(*hujson.Array); ok {
		return a, true
	}
	s.report(v.StartOffset, "%s は配列にする", path)
	return nil, false
}

// str は、v が文字列ならそれを返す。そうでなければ誤りを記録して false を返す。
func (s *source) str(v *hujson.Value, path string) (string, bool) {
	if l, ok := v.Value.(hujson.Literal); ok && l.Kind() == '"' {
		var out string
		if err := json.Unmarshal(l, &out); err == nil {
			return out, true
		}
	}
	s.report(v.StartOffset, "%s は文字列にする", path)
	return "", false
}

// nonNegInt は、v が 0 以上 limit 以下の整数ならそれを返す。そうでなければ誤りを記録して false を返す。
// 小数の表記（1.0、1e3）は整数として扱わない。
func (s *source) nonNegInt(v *hujson.Value, path string, limit int64) (int64, bool) {
	if l, ok := v.Value.(hujson.Literal); ok && l.Kind() == '0' {
		n, err := strconv.ParseInt(string(l), 10, 64)
		if err == nil && n >= 0 && n <= limit {
			return n, true
		}
	}
	s.report(v.StartOffset, "%s は 0 以上 %d 以下の整数にする", path, limit)
	return 0, false
}
