package config

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/tailscale/hujson"
)

// ColorKind は、色の書き方の種類である。
type ColorKind int

// 色の書き方の種類。
const (
	// ColorUnset は、色を書いていない（端末の既定の色）ことを表す。
	ColorUnset ColorKind = iota
	// ColorIndexed は、256 色の番号（0〜255）で書いた色である。
	ColorIndexed
	// ColorRGB は、"#rrggbb" で書いた 24 ビットの色である。
	ColorRGB
)

// Color は、colors の項目の 1 つの色（fg、bg、sp）である。ゼロ値は色を書いていないことを表す。
type Color struct {
	Kind ColorKind
	// Index は、Kind が ColorIndexed のときの番号である。
	Index uint8
	// R、G、B は、Kind が ColorRGB のときの成分である。
	R, G, B uint8
}

// Style は、colors の 1 つの名前の属性である（基本設計 7.1。FR-91）。
// 書かなかった色はゼロ値（端末の既定の色）、書かなかった飾りは false である。
type Style struct {
	// Fg は文字の色、Bg は背景の色、Sp は下線と波線の色である。
	Fg, Bg, Sp Color
	// Bold は太字、Italic は斜体、Underline は下線、Undercurl は波線、Reverse は文字と背景の色の入れ替えである。
	Bold, Italic, Underline, Undercurl, Reverse bool
}

// StatusLine は、statusline の値で、ステータス行の左と右に並べる部品の ID の列である（基本設計 7.1。FR-92）。
type StatusLine struct {
	Left, Right []string
}

// AppearanceCheck は、見た目の設定（colors、symbols、statusline）の名前と値を確かめる関数の組である（Config.CheckAppearance）。
// 名前の一覧と記号の値の規則は描画（internal/ui）が持つが、このパッケージは同じ層の internal/ui を import しないので、呼び出し側が渡す。
// nil の関数の項目は確かめない。
type AppearanceCheck struct {
	// Color は、色の名前 name が本体の色を付ける対象かを返す。
	Color func(name string) bool
	// Symbol は、記号の名前 name に value を使えるかを確かめ、使えなければ理由を返す。
	Symbol func(name, value string) error
	// StatusPart は、id が本体のステータス行の部品の ID かを返す。
	StatusPart func(id string) bool
}

// appearancePos は、見た目の設定の項目を書いた位置である。CheckAppearance が誤りの位置に使う。
type appearancePos struct {
	colors, symbols map[string]*Error
	parts           []partPos
}

// partPos は、statusline の 1 つの部品の ID と、それを書いた位置の誤りの雛形である。
type partPos struct {
	id  string
	err *Error
}

// CheckAppearance は、Colors、Symbols、StatusLine の名前と値を check で確かめ、使えない項目を除いて、位置と内容を誤りとして返す。
// Parse は名前の一覧を知らないので、呼び出し側が Parse の後に 1 回呼ぶ。
//
// req: FR-91, FR-92, FR-93
func (c *Config) CheckAppearance(check AppearanceCheck) []error {
	var errs []error
	if check.Color != nil {
		for name := range c.Colors {
			if !check.Color(name) {
				errs = append(errs, c.pos.colors[name].with("colors の %s は未知の色の名前なので無視する", name))
				delete(c.Colors, name)
			}
		}
	}
	if check.Symbol != nil {
		for name, v := range c.Symbols {
			if err := check.Symbol(name, v); err != nil {
				errs = append(errs, c.pos.symbols[name].with("symbols の %s を使えない: %v", name, err))
				delete(c.Symbols, name)
			}
		}
	}
	if check.StatusPart != nil && c.StatusLine != nil {
		bad := map[string]bool{}
		for _, p := range c.pos.parts {
			if !check.StatusPart(p.id) && !bad[p.id] {
				bad[p.id] = true
				errs = append(errs, p.err.with("statusline の部品 %s は未知なので無視する", p.id))
			}
		}
		keep := func(ids []string) []string {
			out := ids[:0:0]
			for _, id := range ids {
				if !bad[id] {
					out = append(out, id)
				}
			}
			return out
		}
		c.StatusLine.Left, c.StatusLine.Right = keep(c.StatusLine.Left), keep(c.StatusLine.Right)
	}
	sortErrors(errs)
	return errs
}

// colors は、colors の値を読む（基本設計 7.1。FR-91）。
// 値は、色の名前から属性のオブジェクトへの対応である。属性のオブジェクトは fg、bg、sp（"#rrggbb" か 0〜255 の整数）と、
// bold、italic、underline、undercurl、reverse（真偽値）を持てる。誤りのある名前は除く。
func (s *source) colors(v *hujson.Value, cfg *Config) map[string]Style {
	obj, ok := s.object(v, "colors")
	if !ok {
		return nil
	}
	out := map[string]Style{}
	for _, e := range s.members(obj, "colors") {
		path := join("colors", e.name)
		st, ok := s.style(e.value, path)
		if !ok {
			continue
		}
		out[e.name] = st
		if cfg.pos.colors == nil {
			cfg.pos.colors = map[string]*Error{}
		}
		cfg.pos.colors[e.name] = s.at(e.nameOff, "")
	}
	return out
}

// style は、colors の 1 つの名前の属性のオブジェクトを読む。誤りがあれば、その名前を使わない。
func (s *source) style(v *hujson.Value, path string) (Style, bool) {
	var st Style
	obj, ok := s.object(v, path)
	if !ok {
		return st, false
	}
	ok = true
	for _, m := range s.members(obj, path) {
		p := join(path, m.name)
		var flag *bool
		switch m.name {
		case "fg", "bg", "sp":
			c, cok := s.color(m.value, p)
			if !cok {
				ok = false
				continue
			}
			switch m.name {
			case "fg":
				st.Fg = c
			case "bg":
				st.Bg = c
			default:
				st.Sp = c
			}
			continue
		case "bold":
			flag = &st.Bold
		case "italic":
			flag = &st.Italic
		case "underline":
			flag = &st.Underline
		case "undercurl":
			flag = &st.Undercurl
		case "reverse":
			flag = &st.Reverse
		default:
			s.report(m.nameOff, "未知の項目 %s を無視する", p)
			continue
		}
		b, bok := s.boolean(m.value, p)
		if !bok {
			ok = false
			continue
		}
		*flag = b
	}
	return st, ok
}

// color は、"#rrggbb" の文字列か、0〜255 の整数の色を読む。
func (s *source) color(v *hujson.Value, path string) (Color, bool) {
	if l, ok := v.Value.(hujson.Literal); ok {
		switch l.Kind() {
		case '0':
			if n, err := strconv.ParseUint(string(l), 10, 8); err == nil {
				return Color{Kind: ColorIndexed, Index: uint8(n)}, true
			}
		case '"':
			var str string
			if err := json.Unmarshal(l, &str); err == nil {
				if c, ok := parseHexColor(str); ok {
					return c, true
				}
			}
		}
	}
	s.report(v.StartOffset, `%s は "#rrggbb" の文字列か 0 以上 255 以下の整数にする`, path)
	return Color{}, false
}

// parseHexColor は、"#rrggbb" の色を読む。16 進の数字は大文字と小文字のどちらでもよい。
func parseHexColor(str string) (Color, bool) {
	if len(str) != 7 || str[0] != '#' {
		return Color{}, false
	}
	var rgb [3]uint8
	for i := range rgb {
		n, err := strconv.ParseUint(str[1+2*i:3+2*i], 16, 8)
		if err != nil {
			return Color{}, false
		}
		rgb[i] = uint8(n)
	}
	return Color{Kind: ColorRGB, R: rgb[0], G: rgb[1], B: rgb[2]}, true
}

// symbols は、symbols の値を読む（基本設計 7.1。FR-93）。値は、記号の名前から文字列への対応である。
// 名前と文字列の規則は CheckAppearance で確かめる。
func (s *source) symbols(v *hujson.Value, cfg *Config) map[string]string {
	obj, ok := s.object(v, "symbols")
	if !ok {
		return nil
	}
	out := map[string]string{}
	for _, e := range s.members(obj, "symbols") {
		str, ok := s.str(e.value, join("symbols", e.name))
		if !ok {
			continue
		}
		out[e.name] = str
		if cfg.pos.symbols == nil {
			cfg.pos.symbols = map[string]*Error{}
		}
		cfg.pos.symbols[e.name] = s.at(e.nameOff, "")
	}
	return out
}

// statusLine は、statusline の値を読む（基本設計 7.1。FR-92）。
// 値は left と right の 2 つの項目を持つオブジェクトで、どちらも部品の ID の文字列の配列である。書かなかった側は空の列（部品を出さない）にする。
// 部品の ID は CheckAppearance で確かめる。
func (s *source) statusLine(v *hujson.Value, cfg *Config) *StatusLine {
	obj, ok := s.object(v, "statusline")
	if !ok {
		return nil
	}
	out := &StatusLine{Left: []string{}, Right: []string{}}
	for _, m := range s.members(obj, "statusline") {
		path := join("statusline", m.name)
		var dst *[]string
		switch m.name {
		case "left":
			dst = &out.Left
		case "right":
			dst = &out.Right
		default:
			s.report(m.nameOff, "未知の項目 %s を無視する", path)
			continue
		}
		arr, ok := s.array(m.value, path)
		if !ok {
			continue
		}
		for i := range arr.Elements {
			el := &arr.Elements[i]
			id, ok := s.str(el, path+" の項目")
			if !ok {
				continue
			}
			*dst = append(*dst, id)
			cfg.pos.parts = append(cfg.pos.parts, partPos{id: id, err: s.at(el.StartOffset, "")})
		}
	}
	return out
}

// with は、e の位置で、内容を書き換えた誤りを返す。e が nil（位置を記録していない）なら位置の無い誤りを返す。
func (e *Error) with(format string, args ...any) error {
	out := &Error{Msg: fmt.Sprintf(format, args...)}
	if e != nil {
		out.Path, out.Line, out.Column = e.Path, e.Line, e.Column
	}
	return out
}

// sortErrors は、CheckAppearance の誤りを、ファイルの中の位置の順に並べる（対応表を回る順は決まらないため）。
func sortErrors(errs []error) {
	key := func(err error) (line, col int, msg string) {
		var p *Error
		if errors.As(err, &p) {
			return p.Line, p.Column, p.Msg
		}
		return 0, 0, err.Error()
	}
	slices.SortStableFunc(errs, func(a, b error) int {
		la, ca, ma := key(a)
		lb, cb, mb := key(b)
		return cmp.Or(cmp.Compare(la, lb), cmp.Compare(ca, cb), strings.Compare(ma, mb))
	})
}
