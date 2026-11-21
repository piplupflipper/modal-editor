package editor

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/piplupflipper/vi-imitation/ext"
	"github.com/piplupflipper/vi-imitation/internal/command/target"
	"github.com/piplupflipper/vi-imitation/internal/option"
	"github.com/piplupflipper/vi-imitation/internal/ui"
)

// scrollbarText は、90 行のテキストを返す。i 行目（0 から数える）は "line i" で、marked に含む行は後ろに " foo" を付ける。
func scrollbarText(marked ...int) string {
	var b strings.Builder
	for i := range 90 {
		fmt.Fprintf(&b, "line %d", i)
		for _, m := range marked {
			if m == i {
				b.WriteString(" foo")
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// startScrollbar は、20 列 12 行の端末（タブの行の下の 9 行が表示領域）で、files の a.txt を開いてエディタを起動する。
func startScrollbar(t *testing.T, files map[string]string, opts map[string]option.Value) *harness {
	t.Helper()
	dir := writeFiles(t, files)
	term := newFakeTerm()
	term.w, term.h = 20, 12
	h := startTerm(t, term, Config{WorkDir: dir, Options: opts}, "a.txt")
	h.settle()
	return h
}

// scrollbarOpts は、スクロールバーを出す設定に、extra を足したものを返す。
func scrollbarOpts(extra map[string]option.Value) map[string]option.Value {
	opts := map[string]option.Value{option.ScrollbarShow: option.BoolValue(true)}
	for k, v := range extra {
		opts[k] = v
	}
	return opts
}

// rightColumn は、term の最後の画面の表示領域（タブの行の下の 9 行）の右端の列を、つまみのセル（既定の配色の scrollbar の属性が掛かったセル）は "#"、
// 印のセルはその文字、ほかは "." にして並べた文字列を返す。
func rightColumn(term *fakeTerm) string {
	term.mu.Lock()
	defer term.mu.Unlock()
	g := term.last.Grid
	if g == nil {
		return ""
	}
	thumb := ui.DefaultTheme()[ui.StyleScrollbar]
	x := g.Width() - 1
	var b strings.Builder
	for row := 1; row <= 9; row++ {
		c := g.At(row, x)
		switch {
		case len(c.Text) == 1 && strings.ContainsAny(c.Text, "EWIH="):
			b.WriteString(c.Text)
		case c.Attr.Over(thumb) == c.Attr && c.Attr != (ui.Attr{}):
			b.WriteByte('#')
		default:
			b.WriteByte('.')
		}
	}
	return b.String()
}

// waitColumn は、term の右端の列が want になるまで待つ。
func waitColumn(h *harness, term *fakeTerm, want string) {
	h.t.Helper()
	h.eventually(fmt.Sprintf("右端の列 %q", want), func() bool { return rightColumn(term) == want })
}

// 設定の scrollbar.show が真なら、client の画面のバッファの表示領域の右端に、見えている行の範囲のつまみを出す。偽なら出さない。
// 表示領域に収まるバッファと、scrollbar.excludedFileTypes に書いたファイルの種類のバッファには出さない。
// scrollbar.currentOnly が真なら、操作中の client の画面にだけ出し、偽ならどの client の画面にも出す。
//
// req: FR-186
func TestScrollbarThumb(t *testing.T) {
	files := map[string]string{"a.txt": scrollbarText(), "b.go": scrollbarText(), "short.txt": "a\nb\n"}
	h := startScrollbar(t, files, scrollbarOpts(nil))
	// 90 行のうち 0 行目から 8 行目を見ている。つまみは 0×9÷90 = 0 行目から、9×9÷90 = 0.9 の切り上げの 1 行目の手前まで。
	waitColumn(h, h.term, "#........")
	h.keys("G")
	waitColumn(h, h.term, "........#")

	// 出さない設定にすると消える。描くたびに読むので、設定を変えたときの処理は無い。
	h.keys(":option.set scrollbar.show false<CR>")
	waitColumn(h, h.term, ".........")
	h.keys(":option.set scrollbar.show true<CR>")
	waitColumn(h, h.term, "........#")

	// 表示領域に収まるバッファには出さない。
	h.keys(":e short.txt<CR>")
	waitColumn(h, h.term, ".........")

	// 出さないファイルの種類（b.go は go）には出さない。
	h.keys(":option.set scrollbar.excludedFileTypes help,go<CR>")
	h.keys(":e b.go<CR>")
	waitColumn(h, h.term, ".........")
	h.keys(":option.set scrollbar.excludedFileTypes help<CR>")
	waitColumn(h, h.term, "#........")
}

// scrollbar.currentOnly が真なら、フォーカスのある client（最後にキーを受けた client か、本体がフォーカスを移した先の client）の
// 画面にだけ出す。偽ならどちらにも出す。
//
// req: FR-186
func TestScrollbarCurrentOnly(t *testing.T) {
	files := map[string]string{"a.txt": scrollbarText(), "b.txt": scrollbarText()}
	dir := writeFiles(t, files)
	term := newFakeTerm()
	term.w, term.h = 20, 12
	moved := make(chan string, 4)
	cfg := Config{WorkDir: dir, Options: scrollbarOpts(nil), FocusPane: func(_, to string) error {
		moved <- to
		return nil
	}}
	h := startTerm(t, term, cfg, "a.txt")
	h.settle()
	h.e.do(func() { h.e.clients[0].info = ClientInfo{ID: 1, Pane: "%1"} })
	bt := &exitTerm{fakeTerm: newFakeTerm()}
	bt.w, bt.h = 20, 12
	h.peers = append(h.peers, bt.fakeTerm)
	if err := h.e.Attach(bt, ClientInfo{ID: 2, Pane: "%2", WorkDir: dir, Files: []string{filepath.Join(dir, "b.txt")}}); err != nil {
		t.Fatal(err)
	}
	b := &peer{h: h, term: bt}
	b.keys("l")
	h.settle()
	waitColumn(h, h.term, "#........")
	waitColumn(h, b.term.fakeTerm, "#........")

	h.e.do(func() {
		d, _ := option.Lookup(option.ScrollbarCurrentOnly)
		if err := h.e.options.Set(d, option.BoolValue(true)); err != nil {
			t.Fatal(err)
		}
	})
	b.keys("l")
	waitColumn(h, h.term, ".........")
	waitColumn(h, b.term.fakeTerm, "#........")
	h.keys("l")
	waitColumn(h, h.term, "#........")
	waitColumn(h, b.term.fakeTerm, ".........")

	// 別の client のタブのファイルを開こうとすると、本体がその client のペインへフォーカスを移す（FR-63）。
	// キーを打つ前でも、バーはフォーカスを移した先の client の画面へ移る。
	h.keys(":e b.txt<CR>")
	if to := <-moved; to != "%2" {
		t.Fatalf("フォーカスを移した先 = %s, want %%2", to)
	}
	waitColumn(h, h.term, ".........")
	waitColumn(h, b.term.fakeTerm, "#........")
}

// 印は、診断の範囲の始まりの行と、一致に色を付けている検索のパターンの一致のある行に出す。
// 同じセルでは、誤り、警告、検索の一致、情報、ヒントの順に優先する。出す種類は scrollbar.marks で選ぶ。
// 一致に色を付けていない（search.highlight が偽、:nohlsearch で消した）間は、検索の一致の印を出さない。
//
// req: FR-187
func TestScrollbarMarks(t *testing.T) {
	files := map[string]string{"a.txt": scrollbarText(5, 25, 45, 85)}
	h := startScrollbar(t, files, scrollbarOpts(map[string]option.Value{option.SearchHighlight: option.BoolValue(true)}))
	setDiags(h, "lsp", []ext.Diagnostic{
		{Range: ext.Range{Start: ext.Position{Line: 41}, End: ext.Position{Line: 41}}, Severity: ext.SeverityInfo, Message: "i"},
		{Range: ext.Range{Start: ext.Position{Line: 47}, End: ext.Position{Line: 47}}, Severity: ext.SeverityWarning, Message: "w"},
		{Range: ext.Range{Start: ext.Position{Line: 62}, End: ext.Position{Line: 62}}, Severity: ext.SeverityHint, Message: "h"},
		{Range: ext.Range{Start: ext.Position{Line: 66}, End: ext.Position{Line: 66}}, Severity: ext.SeverityError, Message: "e"},
		{Range: ext.Range{Start: ext.Position{Line: 81}, End: ext.Position{Line: 81}}, Severity: ext.SeverityInfo, Message: "i"},
	})
	// 行 l は表示領域の floor(l×9÷90) 行目、つまり l÷10 行目に出る。4 行目は警告が情報と検索の一致より、
	// 6 行目は誤りがヒントより、8 行目は検索の一致が情報より上である。0 行目はつまみの上の印である。
	h.keys("l")
	waitColumn(h, h.term, "#...W.E.I")
	h.keys("/foo<CR>")
	waitColumn(h, h.term, "=.=.W.E.=")

	// 検索の一致の印だけにする。
	h.keys(":option.set scrollbar.marks search<CR>")
	waitColumn(h, h.term, "=.=.=...=")
	// 一致の色を消すと、検索の一致の印も消える。
	h.keys(":nohlsearch<CR>")
	waitColumn(h, h.term, "#........")
	// 診断の印だけにする。
	h.keys(":option.set scrollbar.marks diagnostic<CR>")
	waitColumn(h, h.term, "#...W.E.I")
	// 知らない名前は読み飛ばす。空なら印を出さない。
	h.keys(":option.set search.highlight true<CR>")
	h.keys(":option.set scrollbar.marks git,search<CR>")
	waitColumn(h, h.term, "=.=.=...=")
	h.keys(":option.set scrollbar.marks <CR>")
	waitColumn(h, h.term, "#........")
}

// 検索の一致のある行は、描画の中で集めず、イベントループの外で集め、パターンとバッファとバージョン番号が同じ間は集め直さない。
// 時間の上限を超えた結果も覚え、描くたびに集め直さない。編集してバージョン番号が変わったら、集め直す。
//
// req: FR-188
func TestScrollbarSearchCache(t *testing.T) {
	files := map[string]string{"a.txt": scrollbarText(5, 85)}
	h := startScrollbar(t, files, scrollbarOpts(map[string]option.Value{option.SearchHighlight: option.BoolValue(true)}))
	seq := func() uint64 {
		var n uint64
		h.e.do(func() { n = h.e.cur.scrollSearch.seq })
		return n
	}
	h.keys("/foo<CR>")
	waitColumn(h, h.term, "=.......=")
	n := seq()
	// カーソルを動かしても、描くたびには集め直さない。
	h.keys("jjkk")
	h.settle()
	waitColumn(h, h.term, "=.......=")
	if got := seq(); got != n {
		t.Errorf("動かしただけで集め直した: seq = %d, want %d", got, n)
	}
	// 編集すると、今のバージョン番号で集め直す。
	h.keys("ggA foo<Esc>")
	h.settle()
	waitColumn(h, h.term, "=.......=")
	if got := seq(); got <= n {
		t.Errorf("編集の後に集め直していない: seq = %d", got)
	}

	// 時間の上限を超えたら印を出さず、パターンもバージョン番号も同じ間は集め直さない。
	h.e.do(func() { scrollbarSearchTimeout = 0 })
	t.Cleanup(func() { h.e.do(func() { scrollbarSearchTimeout = 2 * time.Second }) })
	h.keys("/line<CR>")
	waitColumn(h, h.term, "#........")
	n = seq()
	h.keys("jjkk")
	h.settle()
	if got := seq(); got != n {
		t.Errorf("時間の上限を超えた後に集め直した: seq = %d, want %d", got, n)
	}
}

// スクロールバーのつまみと印のセルを押してもカーソルは動かず、ホイールはテキストの上と同じく表示領域を送る。
// 同じ列でも、つまみも印も描いていないセルと、スクロールバーを出していないときの同じセルは本文で、押した行の最後の文字へカーソルが移る。
//
// req: FR-189
func TestScrollbarMouse(t *testing.T) {
	h := startScrollbar(t, map[string]string{"a.txt": scrollbarText()}, scrollbarOpts(nil))
	waitColumn(h, h.term, "#........")
	// 画面の 1 行目（表示領域の 0 行目）がつまみである。
	h.click(1, 19)
	if got := h.cursor(); got.Line != 0 || got.Byte != 0 {
		t.Errorf("つまみを押した後のカーソル = %+v, want 0:0", got)
	}
	// つまみも印も無い 3 行目（"line 2"）は本文で、行の最後の文字へ移る。
	h.click(3, 19)
	if got := h.cursor(); got.Line != 2 || got.Byte != 5 {
		t.Errorf("何も描いていないセルを押した後のカーソル = %+v, want 2:5", got)
	}
	h.mouse("<ScrollWheelDown>", 1, 19)
	if top := h.viewTop(); top != 3 {
		t.Errorf("つまみの上のホイールの後の最初の行 = %d, want 3", top)
	}
	// 3 行目から 11 行目を見ているので、つまみは 0 行目と 1 行目（12×9÷90 の切り上げの 2 行目の手前まで）である。
	waitColumn(h, h.term, "##.......")
	before := h.cursor()
	h.click(2, 19)
	if got := h.cursor(); got != before {
		t.Errorf("ホイールの後につまみを押した後のカーソル = %+v, want %+v のまま", got, before)
	}

	// スクロールバーを出していなければ、同じセル（画面の 2 行目、"line 4"）は本文である。
	h.keys(":option.set scrollbar.show false<CR>")
	waitColumn(h, h.term, ".........")
	h.click(2, 19)
	if got := h.cursor(); got.Line != 4 || got.Byte != 5 {
		t.Errorf("スクロールバーを出していない同じセルを押した後のカーソル = %+v, want 4:5", got)
	}
}

// コンマで区切った設定の値は、前後の空白を除き、空の名前を読み飛ばす。
func TestOptionList(t *testing.T) {
	if got := optionList(" help, go ,,qf "); fmt.Sprint(got) != "[help go qf]" {
		t.Errorf("optionList = %q", got)
	}
	if got := optionList(""); got != nil {
		t.Errorf("空 = %q", got)
	}
}

// matchLines は、一致する所のある行（空の一致を含む）を小さい順に返し、取り消されたら誤りを返す。
//
// req: FR-188
func TestMatchLines(t *testing.T) {
	src := target.LineSnapshot{"ab", "", "xb", "b"}
	got, err := matchLines(context.Background(), src, regexp.MustCompile(`b`))
	if err != nil || fmt.Sprint(got) != "[0 2 3]" {
		t.Errorf("b = %v, %v", got, err)
	}
	if got, _ := matchLines(context.Background(), src, regexp.MustCompile(`^$`)); fmt.Sprint(got) != "[1]" {
		t.Errorf("^$ = %v", got)
	}
	big := make(target.LineSnapshot, 10)
	for i := range big {
		big[i] = strings.Repeat("x", scrollSearchCheckBytes)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := matchLines(ctx, big, regexp.MustCompile(`x`)); err == nil {
		t.Error("取り消した検索が誤りを返さない")
	}
}
