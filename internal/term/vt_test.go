package term

import (
	"strconv"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/piplupflipper/vi-imitation/internal/ui"
)

// vt は、テストのための小さな端末の模型である。Painter と Terminal が書く制御シーケンス
// （CUP、ED 2、EL 0、ECH、SGR、DECTCEM、DECSCUSR、DECSET と DECRST のいくつか）と文字を解釈し、画面のセルを持つ。
//
// 全角文字の片側を上書きすると、残りの半分を空白にする（xterm と VTE の動き）。
// 行末での折り返しは止めた前提（DECRST 7）で、右端の後に書いた文字は右端のセルを上書きする。
// EL と ECH は、今の属性の背景の色で消す（back color erase）。
//
// tmux を true にすると、tmux の中で動かし、外側の端末が全角文字の片側の上書きで残りの半分を消さない組を写す（#297）。
// 全角文字の片側を上書きすると、残りの半分は、tmux の画面では既定の空白になり、外側の端末には前の文字の半分が残る（stale）。
// 残った半分の列に既定の属性の空白を書くと、tmux は自分の画面と同じなので外側へ送らず、カーソルを進めるだけにする。
// 残った半分は、文字を書くか（既定の空白を除く）、EL か ECH で消すか、ED 2 で消すまで残り、check はそれを誤りにする。
type vt struct {
	w, h          int
	cells         []vtCell
	row, col      int
	attr          ui.Attr
	cursorVisible bool
	shape         int
	// modes は、DECSET で有効にしたモードの番号である。
	modes map[int]bool
	tmux  bool
}

type vtCell struct {
	text  string
	cont  bool // 全角文字の右の半分
	attr  ui.Attr
	stale bool // 外側の端末に前の全角文字の半分が残っている（tmux のとき）
}

func newVT(w, h int) *vt {
	v := &vt{w: w, h: h, cursorVisible: true, modes: map[int]bool{7: true}}
	v.clear()
	return v
}

func (v *vt) clear() {
	v.cells = make([]vtCell, v.w*v.h)
	for i := range v.cells {
		v.cells[i] = vtCell{text: " ", attr: v.attr}
	}
}

func (v *vt) at(r, c int) *vtCell { return &v.cells[r*v.w+c] }

// write は、端末へ書かれたバイト列を解釈する。
func (v *vt) write(t *testing.T, s string) {
	t.Helper()
	for len(s) > 0 {
		if s[0] == 0x1b {
			n := v.escape(t, s)
			s = s[n:]
			continue
		}
		if s[0] < 0x20 {
			t.Fatalf("知らない制御文字 %q", s[0])
		}
		r, size := utf8.DecodeRuneInString(s)
		v.put(string(r))
		s = s[size:]
	}
}

func (v *vt) put(ch string) {
	w := ui.StringWidth(ch, 8)
	if r, _ := utf8.DecodeRuneInString(ch); unicode.In(r, unicode.Mn, unicode.Me) {
		// 結合文字は、直前のセルに足す。
		c := v.col - 1
		if c >= 0 && c < v.w {
			if v.at(v.row, c).cont && c > 0 {
				c--
			}
			v.at(v.row, c).text += ch
		}
		return
	}
	if v.col >= v.w {
		v.col = v.w - 1
	}
	if v.tmux && ch == " " && v.attr == (ui.Attr{}) && v.at(v.row, v.col).stale {
		// tmux の画面では既に既定の空白なので、外側へは送らずにカーソルを進める。
		v.col++
		return
	}
	v.breakWide(v.row, v.col)
	if w == 2 && v.col+1 < v.w {
		v.breakWide(v.row, v.col+1)
	}
	*v.at(v.row, v.col) = vtCell{text: ch, attr: v.attr}
	if w == 2 && v.col+1 < v.w {
		*v.at(v.row, v.col+1) = vtCell{cont: true, attr: v.attr}
	}
	v.col += w
}

// breakWide は、(r, c) のセルを上書きする前に、そこに掛かる全角文字の残りの半分を空白にする。
// tmux のときは、残りの半分を、外側の端末に残った既定の空白にする。
func (v *vt) breakWide(r, c int) {
	broken := func(old vtCell) vtCell {
		if v.tmux {
			return vtCell{text: " ", stale: true}
		}
		return vtCell{text: " ", attr: old.attr}
	}
	cell := v.at(r, c)
	if cell.cont && c > 0 {
		*v.at(r, c-1) = broken(*v.at(r, c-1))
	}
	if !cell.cont && c+1 < v.w && v.at(r, c+1).cont {
		*v.at(r, c+1) = broken(*v.at(r, c+1))
	}
}

// erase は、カーソルの行の、カーソルの列から n 個のセルを、今の属性の背景の色の空白にする（EL と ECH）。
// 消す範囲の両端に掛かる全角文字は、文字ごと消す。カーソルは動かさない。
func (v *vt) erase(n int) {
	c0 := min(v.col, v.w-1)
	c1 := min(c0+n, v.w)
	if v.at(v.row, c0).cont && c0 > 0 {
		c0--
	}
	if c1 < v.w && v.at(v.row, c1).cont {
		c1++
	}
	for c := c0; c < c1; c++ {
		*v.at(v.row, c) = vtCell{text: " ", attr: ui.Attr{Bg: v.attr.Bg}}
	}
}

// escape は、ESC で始まる s の先頭の列を解釈し、そのバイト数を返す。
func (v *vt) escape(t *testing.T, s string) int {
	t.Helper()
	if len(s) < 2 || s[1] != '[' {
		t.Fatalf("知らない列 %q", s[:min(len(s), 8)])
	}
	i := 2
	for i < len(s) && (s[i] >= 0x30 && s[i] <= 0x3f || s[i] == ' ') {
		i++
	}
	if i >= len(s) {
		t.Fatalf("途中で切れた列 %q", s)
	}
	params, final := s[2:i], s[i]
	switch {
	case final == 'H':
		r, c := 1, 1
		if params != "" {
			a, b, _ := strings.Cut(params, ";")
			r, _ = strconv.Atoi(a)
			c, _ = strconv.Atoi(b)
		}
		v.row, v.col = r-1, c-1
	case final == 'J' && params == "2":
		v.clear()
	case final == 'K' && (params == "" || params == "0"):
		v.erase(v.w)
	case final == 'X':
		n := 1
		if params != "" {
			n, _ = strconv.Atoi(params)
		}
		v.erase(max(n, 1))
	case final == 'm':
		v.sgr(t, params)
	case final == 'q' && strings.HasSuffix(params, " "):
		v.shape, _ = strconv.Atoi(strings.TrimSuffix(params, " "))
	case (final == 'h' || final == 'l') && strings.HasPrefix(params, "?"):
		for p := range strings.SplitSeq(params[1:], ";") {
			n, _ := strconv.Atoi(p)
			if n == 25 {
				v.cursorVisible = final == 'h'
			}
			v.modes[n] = final == 'h'
		}
	default:
		t.Fatalf("知らない列 %q", s[:i+1])
	}
	return i + 1
}

func (v *vt) sgr(t *testing.T, params string) {
	t.Helper()
	ps := strings.Split(params, ";")
	u8 := func(s string) uint8 {
		n, err := strconv.ParseUint(s, 10, 8)
		if err != nil {
			t.Fatalf("色の成分 %q: %v", s, err)
		}
		return uint8(n)
	}
	color := func(j *int) ui.Color {
		switch ps[*j+1] {
		case "5":
			n := u8(ps[*j+2])
			*j += 2
			return ui.Indexed(n)
		case "2":
			r, g, b := u8(ps[*j+2]), u8(ps[*j+3]), u8(ps[*j+4])
			*j += 4
			return ui.RGB(r, g, b)
		}
		t.Fatalf("知らない色 %q", params)
		return 0
	}
	for j := 0; j < len(ps); j++ {
		p := ps[j]
		n, _ := strconv.Atoi(p)
		switch {
		case p == "" || p == "0":
			v.attr = ui.Attr{}
		case p == "1":
			v.attr.Bold = true
		case p == "3":
			v.attr.Italic = true
		case p == "4":
			v.attr.Underline = true
		case p == "4:3":
			v.attr.Undercurl = true
		case p == "7":
			v.attr.Reverse = true
		case strings.HasPrefix(p, "58:5:"):
			v.attr.Sp = ui.Indexed(u8(strings.TrimPrefix(p, "58:5:")))
		case strings.HasPrefix(p, "58:2::"):
			c := strings.Split(strings.TrimPrefix(p, "58:2::"), ":")
			if len(c) != 3 {
				t.Fatalf("下線の色 %q", p)
			}
			v.attr.Sp = ui.RGB(u8(c[0]), u8(c[1]), u8(c[2]))
		case n >= 30 && n <= 37:
			v.attr.Fg = ui.Indexed(uint8(n - 30))
		case n >= 90 && n <= 97:
			v.attr.Fg = ui.Indexed(uint8(n - 90 + 8))
		case n >= 40 && n <= 47:
			v.attr.Bg = ui.Indexed(uint8(n - 40))
		case n >= 100 && n <= 107:
			v.attr.Bg = ui.Indexed(uint8(n - 100 + 8))
		case n == 38:
			v.attr.Fg = color(&j)
		case n == 48:
			v.attr.Bg = color(&j)
		default:
			t.Fatalf("知らない SGR %q", params)
		}
	}
}

// check は、模型の画面が s と一致することを確かめる。
func (v *vt) check(t *testing.T, s ui.Screen) {
	t.Helper()
	g := s.Grid
	for r := range g.Height() {
		for c, want := range g.Row(r) {
			got := v.at(r, c)
			if got.stale {
				t.Fatalf("(%d,%d) に前の全角文字の半分が残っている\n模型:\n%s\n格子:\n%s", r, c, v.String(), g.String())
			}
			if want.Width == 0 {
				if !got.cont || got.attr != want.Attr {
					t.Fatalf("(%d,%d) は全角文字の右の半分のはず: 模型 %+v, 格子 %+v\n模型:\n%s\n格子:\n%s", r, c, *got, want, v.String(), g.String())
				}
				continue
			}
			if got.cont || got.text != want.Text || got.attr != want.Attr {
				t.Fatalf("(%d,%d) が違う: 模型 %+v, 格子 %+v\n模型:\n%s\n格子:\n%s", r, c, *got, want, v.String(), g.String())
			}
		}
	}
	if v.cursorVisible != s.Cursor.Visible {
		t.Fatalf("カーソルの表示 = %v, want %v", v.cursorVisible, s.Cursor.Visible)
	}
	if s.Cursor.Visible {
		if v.row != s.Cursor.Row || v.col != s.Cursor.Col {
			t.Fatalf("カーソルの位置 = (%d,%d), want (%d,%d)", v.row, v.col, s.Cursor.Row, s.Cursor.Col)
		}
		if want := decscusr(s.Cursor.Shape); v.shape != want {
			t.Fatalf("カーソルの形 = %d, want %d", v.shape, want)
		}
	}
}

func (v *vt) String() string {
	var b strings.Builder
	for r := range v.h {
		for c := range v.w {
			if !v.at(r, c).cont {
				b.WriteString(v.at(r, c).text)
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}
