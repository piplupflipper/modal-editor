package command

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
	"github.com/piplupflipper/vi-imitation/internal/command/ex"
	"github.com/piplupflipper/vi-imitation/internal/editcmd"
	"github.com/piplupflipper/vi-imitation/internal/input"
)

// visualYankDiffs は、Vim 9.1 と突き合わせた表（vimCase）のうち、ビジュアルモードの y と Y の後のカーソル（FR-129）で
// Vim と結果が違うケースの、vi-imitation の結果（バッファとカーソル）である。キーは vimCase の text と keys である。
// runVimCases は、これらのケースのバッファとカーソルを、表の Vim の値ではなくこの値と比べる。レジスターは Vim と同じである。
//
// 値は、Vim 9.1 を `vim -Nu NONE -i NONE -n --not-a-term` で起動し、`xnoremap y ygv<Esc>` と `xnoremap Y Ygv<Esc>`
// （ヤンクの後に選択を選び直して抜け、カーソルを y を押したときの位置へ戻す）を設定してから、vimCase と同じ方法で取った。
// 方法と件数は #263 の PR の説明に書いた。
// 貼り付け（p、P）を続けるケースは、貼る位置がカーソルに依るので、バッファも Vim と違う。
var visualYankDiffs = map[[2]string]struct{ want, cursor string }{
	{"a|bc\nde\nfg", "Vjyu"}:             {"abc\nde\nfg", "1:1"},
	{"x|yz\nab", "v$yp"}:                 {"xyzyz\n\nab", "0:3"},
	{"x|yz\nab", "vjyp"}:                 {"xyz\nabyz\nab", "1:2"},
	{"x|yz\nab", "vjy$p"}:                {"xyz\nabyz\nab", "1:2"},
	{"a|bc def\nghi", "vey"}:             {"abc def\nghi", "0:2"},
	{"abc d|ef\nghi", "vlliwy"}:          {"abc def\nghi", "1:2"},
	{"a|bc", "vlyP"}:                     {"abbcc", "0:3"},
	{"a|bc def", `vl"ay`}:                {"abc def", "0:2"},
	{"a|bc def", "v2ly"}:                 {"abc def", "0:3"},
	{"a|bc\ndef", "Vjy"}:                 {"abc\ndef", "1:1"},
	{"  a|bc\n def", "Vjy"}:              {"  abc\n def", "1:3"},
	{"a (b |c) d", "va(y"}:               {"a (b c) d", "0:6"},
	{"ab|c\nd\ne", "vjy"}:                {"abc\nd\ne", "1:0"},
	{"a|bc\nde\nfg", "vY"}:               {"abc\nde\nfg", "0:1"},
	{"a|bc\nde\nfg", "vjY"}:              {"abc\nde\nfg", "1:1"},
	{"a|bc\nde\nfg", "VjY"}:              {"abc\nde\nfg", "1:1"},
	{"a|bc\nde\nfg", "vjYP"}:             {"abc\nabc\nde\nde\nfg", "1:0"},
	{"a|bc\nde\nfg", `"avjY`}:            {"abc\nde\nfg", "1:1"},
	{"a|bc\nde\nfg", `vj"AY`}:            {"abc\nde\nfg", "1:1"},
	{"a|bc\nde\nfg", "vjYu"}:             {"abc\nde\nfg", "1:1"},
	{"a|bc\nde\nfg", "Vjy"}:              {"abc\nde\nfg", "1:1"},
	{"  ab|cd\n  efgh\n  ijkl", "vY"}:    {"  abcd\n  efgh\n  ijkl", "0:4"},
	{"  ab|cd\n  efgh\n  ijkl", "vjY"}:   {"  abcd\n  efgh\n  ijkl", "1:4"},
	{"  ab|cd\n  efgh\n  ijkl", "VjY"}:   {"  abcd\n  efgh\n  ijkl", "1:4"},
	{"  ab|cd\n  efgh\n  ijkl", "vjYP"}:  {"  abcd\n  abcd\n  efgh\n  efgh\n  ijkl", "1:2"},
	{"  ab|cd\n  efgh\n  ijkl", `"avjY`}: {"  abcd\n  efgh\n  ijkl", "1:4"},
	{"  ab|cd\n  efgh\n  ijkl", `vj"AY`}: {"  abcd\n  efgh\n  ijkl", "1:4"},
	{"  ab|cd\n  efgh\n  ijkl", "vjYu"}:  {"  abcd\n  efgh\n  ijkl", "1:4"},
	{"  ab|cd\n  efgh\n  ijkl", "Vjy"}:   {"  abcd\n  efgh\n  ijkl", "1:4"},
	{"ab|c", "vY"}:                       {"abc", "0:2"},
	{"ab|c", "vjY"}:                      {"abc", "0:2"},
	{"ab|c", "jvkY"}:                     {"abc", "0:2"},
	{"ab|c", "VjY"}:                      {"abc", "0:2"},
	{"ab|c", `"avjY`}:                    {"abc", "0:2"},
	{"ab|c", `vj"AY`}:                    {"abc", "0:2"},
	{"ab|c", "vjYu"}:                     {"abc", "0:2"},
	{"a|b\ncd", "vjY"}:                   {"ab\ncd", "1:1"},
}

// visualYankDiffs のどのケースも、Vim と突き合わせた表のどれかにある（表のケースを消したときに、使われない値が残らない）。
func TestVisualYankDiffsUsed(t *testing.T) {
	seen := map[[2]string]bool{}
	for _, cases := range [][]vimCase{
		vimCases, shiftCases, emptyRegisterCases, shorthandCases, undoCursorCases,
		visualEOLCases, visualLinesCases, visualDotCases,
	} {
		for _, tc := range cases {
			seen[[2]string{tc.text, tc.keys}] = true
		}
	}
	for k := range visualYankDiffs {
		if !seen[k] {
			t.Errorf("%q %s が表に無い", k[0], k[1])
		}
	}
}

// setupSelectAll は、setup と同じく text（"|" がカーソル）のバッファの State を作り、
// ノーマルモードとビジュアルモードの <C-a> に select-all を割り当てる（利用者の設定の keymap と同じ登録。FR-126）。
func setupSelectAll(t *testing.T, text string) *State {
	t.Helper()
	s := setup(t, text)
	km := input.DefaultKeymap()
	for _, m := range []input.MapMode{input.MapNormal, input.MapVisual} {
		b, err := input.UserBinding(m, editcmd.MustParseKeys("<C-a>"), "select-all")
		if err != nil {
			t.Fatal(err)
		}
		if err := km.Bind(b); err != nil {
			t.Fatal(err)
		}
	}
	s.parser = input.NewParser(km)
	return s
}

// ビジュアルモードの y と Y は、ヤンクの後もカーソルを y を押したときの位置に残す。行末の後にあれば最後の文字へ寄せる。
// d と、ノーマルモードのヤンク（yiw）は、Vim と同じく範囲の始まりへ移す。マーク '< と '> は Vim と同じである。
// 期待値の「Vim では」は、Vim 9.1 で確かめた値である（#263 の表）。
//
// req: FR-129
func TestVisualYankKeepsCursor(t *testing.T) {
	const four = "abcdef\nabcdef\nabcdef\nabcdef"
	tests := []struct {
		text, keys   string
		cursor       string
		reg          string
		markS, markE int
		markSet      bool
		wantText     string
	}{
		// Vim では 0:2。
		{"ab|cdef\nabcdef\nabcdef\nabcdef", "vjjy", "2:2", "v:cdef\nabcdef\nabc", 0, 2, true, four},
		// Vim では 0:0。
		{"ab|cdef\nabcdef\nabcdef\nabcdef", "Vjjy", "2:2", "V:abcdef\nabcdef\nabcdef\n", 0, 2, true, four},
		// Vim では 1:2。
		{"abcdef\nabcdef\nabcdef\nab|cdef", "vkky", "1:2", "v:cdef\nabcdef\nabc", 1, 3, true, four},
		// Vim では 1:4（単語の先頭）。単語の末尾に残る。
		{"xx\nfoo ba|r baz", "viwy", "1:6", "v:bar", 1, 1, true, "xx\nfoo bar baz"},
		// Y（行単位のヤンク）も同じ。Vim では 0:0。
		{"ab|cdef\nabcdef\nabcdef\nabcdef", "vjY", "1:2", "V:abcdef\nabcdef\n", 0, 1, true, four},
		// 行末の後（v$）は、最後の文字へ寄せる。Vim では 0:1。
		{"a|bc\nabcdef", "v$y", "0:2", "v:bc\n", 0, 0, true, "abc\nabcdef"},
		// d は Vim と同じく範囲の始まりへ移す（'> は消した後の最後の行に丸める）。
		{"ab|cdef\nabcdef\nabcdef\nabcdef", "vjjd", "0:2", "v:cdef\nabcdef\nabc", 0, 1, true, "abdef\nabcdef"},
		// ノーマルモードのヤンクは Vim と同じく範囲の始まりへ移す（マークは付かない）。
		{"xx\nfoo ba|r baz", "yiw", "1:4", "v:bar", 0, 0, false, "xx\nfoo bar baz"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%q/%s", tt.text, tt.keys), func(t *testing.T) {
			s := setup(t, tt.text)
			feedKeys(s, tt.keys)
			if got := s.Cursor().String(); got != tt.cursor {
				t.Errorf("cursor = %s, want %s", got, tt.cursor)
			}
			if s.Mode() != ModeNormal {
				t.Errorf("mode = %v", s.Mode())
			}
			if got := s.buf.Text(); got != tt.wantText {
				t.Errorf("text = %q, want %q", got, tt.wantText)
			}
			if r, _ := s.registers.Get(0); vimReg(r) != tt.reg {
				t.Errorf("register = %q, want %q", vimReg(r), tt.reg)
			}
			ls, okS := s.mark('<')
			le, okE := s.mark('>')
			if okS != tt.markSet || okE != tt.markSet || (tt.markSet && (ls != tt.markS || le != tt.markE)) {
				t.Errorf("marks = %d %v, %d %v, want %d, %d (%v)", ls, okS, le, okE, tt.markS, tt.markE, tt.markSet)
			}
		})
	}
}

// selectAllCase は、select-all（<C-a>）を含む keys を打った後の状態である。
// sel は、ビジュアルモードのままなら選択の両端（"0:0-2:5"）、ノーマルモードなら空である。
type selectAllCase struct {
	text, keys   string
	want, cursor string
	mode         Mode
	sel          string
	reg          string // 空なら比べない
}

// selectAllText は、select-all のケースの既定のバッファである（カーソルは 2 行目の 2 文字目）。
const selectAllText = "abc\nd|e\nfghij"

// select-all は、カーソルを動かさずに文字単位のビジュアルモードでバッファの全体を選ぶ。その後の各キーは、
// ビジュアルモードの汎用の規則（選択の範囲は head から作り、モーションは head をカーソルに合わせる）のとおりに働く（#263 の表）。
//
// req: FR-130
func TestSelectAll(t *testing.T) {
	tests := []selectAllCase{
		// 選ぶだけ。カーソルは動かない。選択の終わりは、最後の行の行末の後（ggvG$ の後のカーソルと同じ位置）である。
		{selectAllText, "<C-a>", "abc\nde\nfghij", "1:1", ModeVisual, "0:0-2:5", ""},
		// 行単位のビジュアルモードの中では、文字単位に切り替える。
		{selectAllText, "V<C-a>", "abc\nde\nfghij", "1:1", ModeVisual, "0:0-2:5", ""},
		// ビジュアルモードの中で、もう一度使っても同じ。
		{selectAllText, "vj<C-a>", "abc\nde\nfghij", "2:1", ModeVisual, "0:0-2:5", ""},
		// y は全体をヤンクし、カーソルは元の位置（FR-129）。最後の行の改行は含めない。
		{selectAllText, "<C-a>y", "abc\nde\nfghij", "1:1", ModeNormal, "", "v:abc\nde\nfghij"},
		{selectAllText, "<C-a>Y", "abc\nde\nfghij", "1:1", ModeNormal, "", "V:abc\nde\nfghij\n"},
		// d、x は全体を消す。カーソルは 1 行目の 1 桁目。
		{selectAllText, "<C-a>d", "", "0:0", ModeNormal, "", "v:abc\nde\nfghij"},
		{selectAllText, "<C-a>x", "", "0:0", ModeNormal, "", "v:abc\nde\nfghij"},
		// c は全体を消してインサートモードに入る。
		{selectAllText, "<C-a>cX<Esc>", "X", "0:0", ModeNormal, "", "v:abc\nde\nfghij"},
		// > と < は全体をずらし、選択を保つ。カーソルは元の位置（FR-77）。最後の文字は選択に残る（ggvG$>y と同じ）。
		{selectAllText, "<C-a>>", "\tabc\n\tde\n\tfghij", "1:1", ModeVisual, "0:0-2:5", ""},
		{selectAllText, "<C-a>>y", "\tabc\n\tde\n\tfghij", "1:1", ModeNormal, "", "v:\tabc\n\tde\n\tfghij"},
		// モーションは、カーソルから動き、head をカーソルに合わせる。
		{selectAllText, "<C-a>j", "abc\nde\nfghij", "2:1", ModeVisual, "0:0-2:1", ""},
		{selectAllText, "<C-a>k", "abc\nde\nfghij", "0:1", ModeVisual, "0:0-0:1", ""},
		{selectAllText, "<C-a>w", "abc\nde\nfghij", "2:0", ModeVisual, "0:0-2:0", ""},
		{selectAllText, "<C-a>jy", "abc\nde\nfghij", "2:1", ModeNormal, "", "v:abc\nde\nfg"},
		// 失敗したモーションは、カーソルも head も動かさない。
		{"abc\nde\nfg|hij", "<C-a>jy", "abc\nde\nfghij", "2:2", ModeNormal, "", "v:abc\nde\nfghij"},
		// o は、カーソルを 1 行目の最初の文字へ移し、普通のビジュアルモードになる。
		{selectAllText, "<C-a>o", "abc\nde\nfghij", "0:0", ModeVisual, "0:0-2:5", ""},
		{selectAllText, "<C-a>oj", "abc\nde\nfghij", "1:0", ModeVisual, "1:0-2:5", ""},
		// もう一度 o を打つと、カーソルは最後の行の行末の後（ビジュアルモードの $ の後と同じ）へ移る。
		{selectAllText, "<C-a>oo", "abc\nde\nfghij", "2:5", ModeVisual, "0:0-2:5", ""},
		// <Esc> はノーマルモードへ戻る。カーソルは元の位置。
		{selectAllText, "<C-a><Esc>", "abc\nde\nfghij", "1:1", ModeNormal, "", ""},
		// : の '<,'> はバッファの全体を指す。
		{selectAllText, "<C-a>:s/^/#/<CR>", "#abc\n#de\n#fghij", "2:0", ModeNormal, "", ""},
		// テキストオブジェクトは head の次の文字から広げる。バッファの末尾の後には無いので失敗する。
		{selectAllText, "<C-a>iw", "abc\nde\nfghij", "1:1", ModeVisual, "0:0-2:5", ""},
		// 空のバッファ、1 行だけのバッファ、多バイト文字で終わる最後の行、空の最後の行。
		{"|", "<C-a>", "", "0:0", ModeVisual, "0:0-0:0", ""},
		{"|", "<C-a>d", "", "0:0", ModeNormal, "", ""},
		{"ab|c", "<C-a>", "abc", "0:2", ModeVisual, "0:0-0:3", ""},
		{"ab|c", "<C-a>y", "abc", "0:2", ModeNormal, "", "v:abc"},
		{"ab|c", "<C-a>d", "", "0:0", ModeNormal, "", "v:abc"},
		{"abc\nあ|いう", "<C-a>", "abc\nあいう", "1:3", ModeVisual, "0:0-1:9", ""},
		{"abc\nあ|いう", "<C-a>y", "abc\nあいう", "1:3", ModeNormal, "", "v:abc\nあいう"},
		{"abc\nあ|いう", "<C-a>d", "", "0:0", ModeNormal, "", "v:abc\nあいう"},
		// 最後の行が空の行なら、その前の行の改行までを選ぶ（Vim の ggvG$y も "abc\n"）。
		{"a|bc\n", "<C-a>y", "abc\n", "0:1", ModeNormal, "", "v:abc\n"},
		// . での繰り返し。d と c の後に u で戻し、. で同じ大きさの範囲（全体）にもう一度働く。
		{selectAllText, "<C-a>du.", "", "0:0", ModeNormal, "", "v:abc\nde\nfghij"},
		{selectAllText, "<C-a>cX<Esc>u.", "X", "0:0", ModeNormal, "", "v:abc\nde\nfghij"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%q/%s", tt.text, tt.keys), func(t *testing.T) {
			s := setupSelectAll(t, tt.text)
			want := s.Want()
			feedKeys(s, tt.keys)
			if got := s.buf.Text(); got != tt.want {
				t.Errorf("text = %q, want %q", got, tt.want)
			}
			if got := s.Cursor().String(); got != tt.cursor {
				t.Errorf("cursor = %s, want %s", got, tt.cursor)
			}
			if s.Mode() != tt.mode {
				t.Errorf("mode = %v, want %v", s.Mode(), tt.mode)
			}
			sel := ""
			if start, end, _, ok := s.Selection(); ok {
				sel = start.String() + "-" + end.String()
			}
			if sel != tt.sel {
				t.Errorf("selection = %q, want %q", sel, tt.sel)
			}
			if tt.reg != "" {
				if r, _ := s.registers.Get(0); vimReg(r) != tt.reg {
					t.Errorf("register = %q, want %q", vimReg(r), tt.reg)
				}
			}
			if tt.keys == "<C-a>" && s.Want() != want {
				t.Errorf("want = %d, want %d（望みの列を変えた）", s.Want(), want)
			}
		})
	}
}

// jumpCountNav は、MarkJump の回数を数える Navigator である。
type jumpCountNav struct{ marks int }

func (n *jumpCountNav) MarkJump(buffer.Position) { n.marks++ }

func (n *jumpCountNav) Navigate(ex.Action, buffer.Position) (buffer.Position, bool, error) {
	return buffer.Position{}, false, errors.New("unexpected")
}

func (n *jumpCountNav) RunActions([]ex.Action) error { return nil }

// select-all は、画面をスクロールせず（表示領域の最初の行を変えず）、ジャンプリストに記録しない。
// その後のページ単位のスクロールは、カーソルを動かす他の操作と同じく head をカーソルに合わせる。
//
// req: FR-130
func TestSelectAllKeepsView(t *testing.T) {
	text := strings.Repeat("x\n", 29) + "x|y"
	s := setupSelectAll(t, text)
	vp := &fakeViewport{top: 25, rows: 10}
	nav := &jumpCountNav{}
	s.Viewport, s.Navigator = vp, nav
	feedKeys(s, "<C-a>")
	if vp.top != 25 || nav.marks != 0 || s.Cursor().String() != "29:1" {
		t.Errorf("top = %d, jumps = %d, cursor = %s", vp.top, nav.marks, s.Cursor())
	}
	// 比べのために、gg はジャンプリストに記録することを確かめる（記録の口がつながっている）。
	feedKeys(s, "<Esc>gg")
	if nav.marks != 1 {
		t.Fatalf("gg の jumps = %d", nav.marks)
	}
	feedKeys(s, "<C-a><PageDown>")
	start, end, _, ok := s.Selection()
	if !ok || start.String() != "0:0" || end != s.Cursor() || end.Line == 0 {
		t.Errorf("selection = %s-%s, cursor = %s", start, end, s.Cursor())
	}
	// 呼び出し側が同じ位置へ置き直したとき（外からの編集の後の SetCursor）は、カーソルが動かないので選択は全体のまま。
	feedKeys(s, "<Esc><C-a>")
	s.SetCursor(s.Cursor())
	if start, end, _, _ := s.Selection(); start.String() != "0:0" || end.String() != "29:2" {
		t.Errorf("同じ位置の SetCursor の後の selection = %s-%s", start, end)
	}
	// 呼び出し側がカーソルを動かしたとき（SetCursor）は、head をカーソルに合わせる。
	s.SetCursor(pos(1, 0))
	if start, end, _, _ := s.Selection(); start.String() != "0:0" || end.String() != "1:0" {
		t.Errorf("SetCursor の後の selection = %s-%s", start, end)
	}
}

// select-all の後の拡張機能の操作、モーション、オペレーターも、ビジュアルモードの汎用の規則で働く。
// カーソルを動かさない操作は選択を残し、モーションは head をカーソルに合わせ、保留したオペレーターは全体に働く。
//
// req: FR-130
func TestSelectAllExtension(t *testing.T) {
	tests := []struct {
		name, text, keys string
		deferred         bool
		want, cursor     string
		mode             Mode
		sel              string
	}{
		// dup.line（操作）はカーソルを動かさないので、head をカーソルへ合わせない。
		// 拡張機能の操作の編集では選択の両端をずらさない（main からの anchor と同じ）ので、head は元の 2:5 のままで、
		// Selection はそれを編集の後のバッファの中（3 行目の de の行末の後）に丸めて返す。
		{"操作", selectAllText, "<C-a>gD", false, "abc\nde\nde\nfghij", "1:1", ModeVisual, "0:0-2:2"},
		// jump.x（モーション）は、カーソルから動き、head をカーソルに合わせる。
		{"モーション", "abc\nd|e x\nfghij", "<C-a>gx", false, "abc\nde x\nfghij", "1:3", ModeVisual, "0:0-1:3"},
		// surround.add（オペレーター）を保留し、後から適用すると、選択の全体を囲む。
		{"保留したオペレーター", selectAllText, "<C-a>ys]", true, "[abc\nde\nfghij]", "0:0", ModeNormal, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			km := extKeymap(t)
			for _, m := range []input.MapMode{input.MapNormal, input.MapVisual} {
				b, err := input.UserBinding(m, editcmd.MustParseKeys("<C-a>"), "select-all")
				if err != nil {
					t.Fatal(err)
				}
				if err := km.Bind(b); err != nil {
					t.Fatal(err)
				}
			}
			// dup.line をビジュアルモードでも使えるようにする（extKeymap の登録はノーマルモードだけ）。
			if err := km.Bind(input.Binding{Kind: input.BindAction, Modes: input.MapVisual, Keys: editcmd.MustParseKeys("gD"), Ref: editcmd.Ref{Extension: "dup", ID: "line"}}); err != nil {
				t.Fatal(err)
			}
			s := setup(t, tt.text)
			s.parser = input.NewParser(km)
			s.Extensions = &fakeExt{deferring: tt.deferred}
			var d *Deferred
			for _, r := range feedKeys(s, tt.keys) {
				if r.Err != nil {
					t.Fatalf("err = %v", r.Err)
				}
				if r.Deferred != nil {
					d = r.Deferred
				}
			}
			if tt.deferred {
				if d == nil {
					t.Fatal("保留されなかった")
				}
				if r := s.ApplyDeferred(*d, &fakeExt{}); r.Err != nil {
					t.Fatalf("ApplyDeferred: %v", r.Err)
				}
			}
			if got := s.buf.Text(); got != tt.want {
				t.Errorf("text = %q, want %q", got, tt.want)
			}
			if got := s.Cursor().String(); got != tt.cursor {
				t.Errorf("cursor = %s, want %s", got, tt.cursor)
			}
			if s.Mode() != tt.mode {
				t.Errorf("mode = %v, want %v", s.Mode(), tt.mode)
			}
			sel := ""
			if start, end, _, ok := s.Selection(); ok {
				sel = start.String() + "-" + end.String()
			}
			if sel != tt.sel {
				t.Errorf("selection = %q, want %q", sel, tt.sel)
			}
		})
	}
}

// select-all の後の端末からの貼り付け（bracketed paste）は、ビジュアルモードの貼り付けと同じく選択（全体）を置き換える。
//
// req: FR-130
func TestSelectAllPaste(t *testing.T) {
	s := setupSelectAll(t, selectAllText)
	feedKeys(s, "<C-a>")
	s.Paste("XY")
	if got := s.buf.Text(); got != "XY" || s.Mode() != ModeNormal || s.Cursor().String() != "0:1" {
		t.Errorf("text = %q, mode = %v, cursor = %s", got, s.Mode(), s.Cursor())
	}
}
