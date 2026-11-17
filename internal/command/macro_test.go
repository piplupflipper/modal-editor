package command

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/piplupflipper/modal-editor/internal/buffer"
	"github.com/piplupflipper/modal-editor/internal/command/ex"
	"github.com/piplupflipper/modal-editor/internal/editcmd"
	"github.com/piplupflipper/modal-editor/internal/input"
)

// q{reg} で記録を始め、q で止める。記録したキーの列はレジスターに入り、@ で 1 個ずつ再生される。
//
// req: FR-05
func TestMacroRecord(t *testing.T) {
	s := setup(t, "|ab\ncd\nef")
	feedKeys(s, "qa")
	if s.Recording() != 'a' {
		t.Fatalf("Recording = %q", s.Recording())
	}
	feedKeys(s, "A!<Esc>jq")
	if s.Recording() != 0 {
		t.Fatalf("記録が止まらない")
	}
	reg, _ := s.registers.Get('a')
	if reg.Text != "A!\x1bj" {
		t.Errorf("a = %q", reg.Text)
	}
	feedKeys(s, "@a")
	if got := s.buf.Text(); got != "ab!\ncd!\nef" {
		t.Errorf("text = %q", got)
	}
	// 途中の j が失敗したら、残りの再生を止める。
	err := errOf(feedKeys(s, "5@a"))
	if !errors.Is(err, ErrFailed) {
		t.Errorf("err = %v", err)
	}
	if got := s.buf.Text(); got != "ab!\ncd!\nef!" {
		t.Errorf("text = %q", got)
	}
}

// 自分を呼ぶマクロは、入れ子の上限で止まる。
//
// req: FR-05
func TestMacroRecursion(t *testing.T) {
	s := setup(t, "|abcdefgh")
	s.registers.Set('a', Register{Text: "x@a"})
	err := errOf(feedKeys(s, "@a"))
	if !errors.Is(err, ErrFailed) {
		t.Errorf("err = %v", err)
	}
	if got := s.buf.Text(); got != "" {
		t.Errorf("text = %q", got)
	}
}

// @@ は直前に再生したレジスターを繰り返す。前が無ければ失敗する。
// Vim の @:（直前のコマンドラインを実行し直す）は持たない（要件定義 1.1）。
//
// req: FR-05
func TestMacroRepeat(t *testing.T) {
	s := setup(t, "|a1\na2\na3\na4")
	if err := errOf(feedKeys(s, "@@")); !errors.Is(err, ErrFailed) {
		t.Errorf("@@ err = %v", err)
	}
	feedKeys(s, ":s/a/b/<CR>")
	var ie *InvalidRegisterError
	if err := errOf(feedKeys(s, "j@:")); !errors.As(err, &ie) || ie.Name != ':' {
		t.Errorf("@: err = %v, want E354", err)
	}
	if got := s.buf.Text(); got != "b1\na2\na3\na4" {
		t.Errorf("@: text = %q", got)
	}
	s.registers.Set('q', Register{Text: "x"})
	feedKeys(s, "gg@qj@@")
	if got := s.buf.Text(); got != "1\n2\na3\na4" {
		t.Errorf("text = %q", got)
	}
}

// 記録中に入力した @a は、再生されたキーではなく @a として記録される。
// 記録先の大文字は追記で、記録したキーは無名のレジスターに入らない（無名は再生した x の削除のまま）。
//
// req: FR-04, FR-05
func TestMacroRecordAppend(t *testing.T) {
	s := setup(t, "|abcdef")
	s.registers.Set('b', Register{Text: "x"})
	feedKeys(s, "qa@bqqAlq")
	reg, _ := s.registers.Get('a')
	if reg.Text != "@bl" || !equalKeys(reg.keys, "@bl") {
		t.Errorf("a = %+v", reg)
	}
	if got, _ := s.registers.Get(0); got.Text != "a" {
		t.Errorf("無名 = %+v", got)
	}
}

func equalKeys(keys []editcmd.Key, s string) bool {
	want := editcmd.MustParseKeys(s)
	if len(keys) != len(want) {
		return false
	}
	for i := range keys {
		if keys[i] != want[i] {
			return false
		}
	}
	return true
}

// ビジュアルモードでも、記録中の q で記録を止める。
//
// req: FR-05
func TestMacroStopInVisual(t *testing.T) {
	s := setup(t, "|abc")
	feedKeys(s, "qavlq")
	if s.Recording() != 0 || s.Mode() != ModeVisual {
		t.Fatalf("Recording = %q, mode = %v", s.Recording(), s.Mode())
	}
	if reg, _ := s.registers.Get('a'); reg.Text != "vl" {
		t.Errorf("a = %q", reg.Text)
	}
}

// インサートモードで終わるマクロの再生は、インサートモードを抜けるまでを 1 単位にする。
// 期待値は Vim 9.1 で :let @a='iX' の後に同じキーを入れた結果である。
//
// req: FR-05, FR-07
func TestMacroEndsInInsert(t *testing.T) {
	for _, tt := range []struct{ keys, want, cursor string }{
		{"@aY<Esc>u", "abc", "0:0"},
		{"@aY<Esc>xu", "XYabc", "0:1"},
	} {
		s := setup(t, "|abc")
		s.registers.Set('a', Register{Text: "iX"})
		feedKeys(s, tt.keys)
		if got := s.buf.Text(); got != tt.want {
			t.Errorf("%s: text = %q, want %q", tt.keys, got, tt.want)
		}
		if got := s.Cursor().String(); got != tt.cursor {
			t.Errorf("%s: cursor = %s, want %s", tt.keys, got, tt.cursor)
		}
	}
}

// Interrupted が true を返すと、大きな回数のマクロの再生は ErrInterrupted で止まる。
//
// req: FR-05
func TestMacroInterrupted(t *testing.T) {
	s := setup(t, "|a")
	calls := 0
	s.Interrupted = func() bool { calls++; return calls > 3 }
	s.registers.Set('b', Register{Text: "Ax<Esc>"})
	if err := errOf(feedKeys(s, "999999999@b")); !errors.Is(err, ErrInterrupted) {
		t.Fatalf("@b err = %v", err)
	}
	// 4 回目の問い合わせ（<Esc> の前）で止まるので、x を入れたインサートモードのまま残る（Vim の <C-c> と同じく途中で止める）。
	if got := s.buf.Text(); got != "ax" {
		t.Errorf("@b: text = %q", got)
	}
}

// マクロの中の Ex コマンドが確認を求めたら、Vim 9.1 と同じくマクロの次のキーを答えとして読む。
// Vim 9.1 で let @q=":3,1d\ryAZ\e" を a b c d の 4 行で @q すると dZ になる（2026-11-16 に確かめた）。
//
// req: FR-05, FR-08
func TestMacroConfirm(t *testing.T) {
	s := setup(t, "|a\nb\nc\nd")
	s.registers.Set('q', Register{Text: ":3,1d\ryAZ\x1b"})
	if err := errOf(feedKeys(s, "@q")); err != nil {
		t.Fatalf("err = %v", err)
	}
	if got := s.buf.Text(); got != "dZ" {
		t.Errorf("y: text = %q", got)
	}
	s = setup(t, "|a\nb\nc\nd")
	s.registers.Set('q', Register{Text: ":3,1d\rnAZ\x1b"})
	feedKeys(s, "@q")
	if got := s.buf.Text(); got != "aZ\nb\nc\nd" {
		t.Errorf("n: text = %q", got)
	}
	// 答えのキーが無ければ、確認は呼び出し側へ渡る。
	s = setup(t, "|a\nb\nc\nd")
	s.registers.Set('q', Register{Text: ":3,1d\r"})
	rs := feedKeys(s, "@q")
	if rs[len(rs)-1].Confirm == nil {
		t.Errorf("Confirm が渡らない: %+v", rs[len(rs)-1])
	}
}

// switchExt は、操作の dup.line で ErrBufferSwitched を返す（再生中に別のバッファへ移った場合）。
type switchExt struct{ fakeExt }

func (f *switchExt) Action(editcmd.Ref, *buffer.Buffer, ActionQuery, QueryInfo) (buffer.Transaction, error) {
	return buffer.Transaction{}, ErrBufferSwitched
}

// 再生の途中で別のバッファへ移ったら、残りのキーを Remaining に入れて止まり、移った先の State の ContinueReplay で続く。
// 残りには、再生が終わったときのドットリピートの対象（FR-57）の印も入る。
//
// req: FR-05, FR-57
func TestReplayHandoff(t *testing.T) {
	s := New(newBuffer("ab\ncd"), input.NewParser(extKeymap(t)))
	s.Extensions = &switchExt{}
	s.registers.Set('a', Register{Text: "xgDx"})
	s.registers.Set('b', Register{Text: "@aj"})
	res := feedKeys(s, "2@b")
	last := res[len(res)-1]
	if !errors.Is(last.Err, ErrBufferSwitched) {
		t.Fatalf("err = %v", last.Err)
	}
	if got := s.buf.Text(); got != "b\ncd" {
		t.Errorf("移る前: text = %q", got)
	}
	o := New(newBuffer("123\n456\n789"), input.NewParser(extKeymap(t)))
	o.registers = s.registers
	o.Extensions = &switchExt{}
	// 残りは @a の x、@a の終わりの印、@b の j、@b の 2 回目、@b の終わりの印。
	if got := segmentsString(last.Remaining); got != "x*1,@a,j*1,@aj*1,2@b" {
		t.Errorf("移る前: Remaining = %s", got)
	}
	// 2 回目の @a の gD でまた移るので、そこで止まり、残りを返す。
	r := o.ContinueReplay(last.Remaining)
	if !errors.Is(r.Err, ErrBufferSwitched) {
		t.Fatalf("ContinueReplay: err = %v", r.Err)
	}
	if got := o.buf.Text(); got != "23\n56\n789" {
		t.Errorf("移った先: text = %q", got)
	}
	if got := segmentsString(r.Remaining); got != "x*1,@a,j*1,2@b" {
		t.Errorf("Remaining = %s", got)
	}
}

// recordNav は、RunActions に渡った動作を覚え、ActionBuffer を受けたら別のバッファへ移ったこと（ErrBufferSwitched）にする。
type recordNav struct{ got [][]ex.Action }

func (n *recordNav) MarkJump(buffer.Position) {}

func (n *recordNav) Navigate(ex.Action, buffer.Position) (buffer.Position, bool, error) {
	return buffer.Position{}, false, errors.New("unexpected")
}

func (n *recordNav) RunActions(acts []ex.Action) error {
	n.got = append(n.got, acts)
	for _, a := range acts {
		if a.Kind == ex.ActionBuffer {
			return ErrBufferSwitched
		}
	}
	return nil
}

// マクロの再生の途中の Ex コマンドの動作（:w、:bn など）は、Navigator.RunActions でその場で行い、Result.Actions には残さない（#172）。
// 別のバッファへ移ったら、残りのキーを Remaining に入れて止まる。再生の外の Ex コマンドの動作は、これまでどおり Result.Actions で返す。
//
// req: FR-05
func TestReplayExActions(t *testing.T) {
	s := setup(t, "|abc")
	nav := &recordNav{}
	s.Navigator = nav
	s.registers.Set('a', Register{Text: ":w\rx:w|bn\rx"})
	res := feedKeys(s, "@a")
	last := res[len(res)-1]
	if !errors.Is(last.Err, ErrBufferSwitched) {
		t.Fatalf("err = %v", last.Err)
	}
	if len(last.Actions) != 0 {
		t.Errorf("Actions = %+v", last.Actions)
	}
	// 1 行目の :w は x の前に行い、2 行目の :w|bn は 2 つの動作を 1 回で渡す。
	if got := fmt.Sprint(len(nav.got), len(nav.got[0]), len(nav.got[len(nav.got)-1])); got != "2 1 2" {
		t.Errorf("RunActions の呼び出し = %+v", nav.got)
	}
	if got := s.buf.Text(); got != "bc" {
		t.Errorf("text = %q", got)
	}
	if got := segmentsString(last.Remaining); got != "x*1,@a" {
		t.Errorf("Remaining = %s", got)
	}

	nav.got = nil
	res = feedKeys(s, ":bn<CR>")
	if last := res[len(res)-1]; len(last.Actions) != 1 || len(nav.got) != 0 {
		t.Errorf("再生の外: Actions = %+v, RunActions = %+v", last.Actions, nav.got)
	}
}

// segmentsString は、再生の残りを、キーの区切りは「キー*回数」、ドットリピートの印は「回数@名前」でつないだ文字列にする。
func segmentsString(segs []ReplaySegment) string {
	var rest []string
	for _, seg := range segs {
		if seg.Dot != nil {
			rest = append(rest, dotString(seg.Dot))
			continue
		}
		rest = append(rest, fmt.Sprintf("%s*%d", editcmd.KeysString(seg.Keys), seg.Count))
	}
	return strings.Join(rest, ",")
}

// dotString は、マクロの再生の値を「回数@名前」（回数が無ければ「@名前」）にする。再生でない値は "-" にする。
func dotString(d *editcmd.Command) string {
	if d == nil || d.Kind != editcmd.KindPlay {
		return "-"
	}
	if d.CountGiven {
		return fmt.Sprintf("%d@%c", d.Count, d.Char)
	}
	return fmt.Sprintf("@%c", d.Char)
}

// マクロの再生（@a、@@）はドットリピートの対象で、. はマクロ全体を繰り返す（Vim の、マクロの中の最後の変更を繰り返す
// 仕様は引き継がない。要件定義 1.1）。
//
// req: FR-57
func TestMacroDotRepeat(t *testing.T) {
	tests := []struct {
		name, text, keys string
		regs             map[rune]string
		want, cursor     string
		dot              string // 最後のドットリピートの対象（dotString）
		fails            bool   // 最後のキーまでのどこかで失敗するか
	}{
		{"マクロ全体", "|abcdefgh", "qaxxq@a.", nil, "gh", "0:0", "@a", false},
		{"アンドゥの 1 単位", "|abcdefgh", "qaxxq@a.u", nil, "efgh", "0:0", "@a", false},
		{"移動だけのマクロ", "|ab\ncd\nef", "xqajq@agg.", nil, "b\ncd\nef", "1:0", "@a", false},
		// 移動だけで記録し直すと、. は対象の @a のまま、新しい中身（l）を再生する。
		{"今の中身", "|abcdef", "qaxq@aqalq.", nil, "cdef", "0:2", "@a", false},
		{"回数", "|abcdefghij", "qaxq3@a.", nil, "hij", "0:0", "3@a", false},
		{"回数の置き換え", "|abcdefghij", "qaxq3@a.2.", nil, "j", "0:0", "2@a", false},
		{"@@ の読み替え", "|abcdefgh", "@a@@", map[rune]string{'a': "x"}, "cdefgh", "0:0", "@a", false},
		{"@@ の後の .", "|abcdefgh", "@a@@.", map[rune]string{'a': "x"}, "defgh", "0:0", "@a", false},
		// マクロの中で変更の前の . は、再生を始める前の対象（x）を繰り返す。
		{"マクロの中の . (変更の前)", "|abcdef", "x@b", map[rune]string{'b': "l."}, "bdef", "0:1", "@b", false},
		// マクロの中で変更の後の . は、マクロの中の直前の変更（~）を繰り返す。マクロの外の対象は @b になる。
		{"マクロの中の . (変更の後)", "|abcdefg", "x@b", map[rune]string{'b': "l.~l."}, "bDeFg", "0:4", "@b", false},
		// a の中の @b の後の . は @b を繰り返し、a の後の . は a 全体を繰り返す。
		{"入れ子", "|abcdef", "~@a.", map[rune]string{'a': "@b.", 'b': "x"}, "Af", "0:1", "@a", false},
		// j の失敗で止まっても、対象は @a になる。途中の x の変更は残る。
		{"失敗", "|abc", "@a.", map[rune]string{'a': "xj~"}, "c", "0:0", "@a", true},
	}
	for _, tt := range tests {
		s := setup(t, tt.text)
		for name, text := range tt.regs {
			s.registers.Set(name, Register{Text: text})
		}
		err := errOf(feedKeys(s, tt.keys))
		if tt.fails != errors.Is(err, ErrFailed) || !tt.fails && err != nil {
			t.Errorf("%s: err = %v", tt.name, err)
		}
		if got := s.buf.Text(); got != tt.want {
			t.Errorf("%s: text = %q, want %q", tt.name, got, tt.want)
		}
		if got := s.Cursor().String(); got != tt.cursor {
			t.Errorf("%s: cursor = %s, want %s", tt.name, got, tt.cursor)
		}
		if got := dotString(s.dot); got != tt.dot {
			t.Errorf("%s: dot = %s, want %s", tt.name, got, tt.dot)
		}
	}
}

// マクロの中で変更の前の . は、対象が再生（@a）であっても、再生ではない直前の変更を繰り返す。
// 自分自身を再生し直して入れ子の上限まで回ることはない（PR #165 のレビューの 4 例。Vim 9.1 と同じ行数が消える）。
//
// req: FR-57
func TestMacroDotRepeatNotRecursive(t *testing.T) {
	lines := func(prefix string, n int, numbered bool) string {
		var ls []string
		for i := 1; i <= n; i++ {
			if numbered {
				ls = append(ls, fmt.Sprintf("%s%d", prefix, i))
			} else {
				ls = append(ls, prefix)
			}
		}
		return "|" + strings.Join(ls, "\n")
	}
	tests := []struct {
		name, text, keys string
		regs             map[rune]string
		want             string // 結果のバッファ。空なら wantLines で行数だけ比べる
		wantLines        int
	}{
		{
			"j. を 2 回", lines("abc", 11, true), "dd@a@a",
			map[rune]string{'a': "j."},
			"abc2\nabc4\nabc6\nabc7\nabc8\nabc9\nabc10\nabc11", 0,
		},
		{"記録した j. を 2 回", lines("abc", 301, false), "ddqaj.q@a@a", nil, "", 297},
		{". だけを記録し直す", "|abcdefghij", "qaxq@aqa.q@a", nil, "efghij", 0},
		{". だけのマクロを 2 回", lines("abc", 301, false), "dd@a@a", map[rune]string{'a': "."}, "", 298},
	}
	for _, tt := range tests {
		s := setup(t, tt.text)
		for name, text := range tt.regs {
			s.registers.Set(name, Register{Text: text})
		}
		if err := errOf(feedKeys(s, tt.keys)); err != nil {
			t.Errorf("%s: err = %v", tt.name, err)
		}
		got := s.buf.Text()
		if tt.want != "" && got != tt.want {
			t.Errorf("%s: text = %q, want %q", tt.name, got, tt.want)
		}
		if n := strings.Count(got, "\n") + 1; tt.want == "" && n != tt.wantLines {
			t.Errorf("%s: 行数 = %d, want %d", tt.name, n, tt.wantLines)
		}
		if got := dotString(s.dot); got != "@a" {
			t.Errorf("%s: dot = %s", tt.name, got)
		}
	}
}

// インサートモードで終わったマクロの後の <Esc> は、再生の外の利用者の入力なので、その挿入をドットリピートの対象にする。
//
// req: FR-57
func TestMacroEndsInInsertDot(t *testing.T) {
	s := setup(t, "|abcd")
	s.registers.Set('a', Register{Text: "iZ"})
	feedKeys(s, "x@a")
	if got := dotString(s.dot); got != "@a" {
		t.Errorf("再生の直後: dot = %s", got)
	}
	feedKeys(s, "<Esc>l.")
	if got := s.buf.Text(); got != "ZZbcd" {
		t.Errorf("text = %q", got)
	}
}

// . から再生したマクロの中のインサートモードは、ドットリピートの再実行としてではなく、マクロのキーのとおりに入力する。
//
// req: FR-57
func TestMacroDotRepeatInsert(t *testing.T) {
	s := setup(t, "|a\nb")
	feedKeys(s, "qaAx<Esc>jq.")
	if got := s.buf.Text(); got != "ax\nbx" {
		t.Errorf("text = %q", got)
	}
	if s.Mode() != ModeNormal {
		t.Errorf("mode = %v", s.Mode())
	}
}

// 別のバッファへ移ってから続けた再生が終わると、ドットリピートの対象は、いちばん外側の再生（2@b）になる。
// 移った先で失敗で止まった場合も同じである。
//
// req: FR-57
func TestReplayHandoffDot(t *testing.T) {
	segs := func() []ReplaySegment {
		s := New(newBuffer("ab\ncd"), input.NewParser(extKeymap(t)))
		s.Extensions = &switchExt{}
		s.registers.Set('a', Register{Text: "xgDx"})
		s.registers.Set('b', Register{Text: "@aj"})
		res := feedKeys(s, "x2@b")
		return res[len(res)-1].Remaining
	}
	tests := []struct {
		name, text string
		segs       []ReplaySegment
		want       string
	}{
		// 移った先の fakeExt の gD は移らない（行を複製する）ので、残りを 2@b の印まで進める。
		{"完走", "123\n456\n789", segs(), "2@b"},
		// 移った先の 1 行目で j が失敗すると、残りの印のうち最後の 2@b を対象にする。
		{"失敗", "123", segs(), "2@b"},
	}
	for _, tt := range tests {
		o := New(newBuffer(tt.text), input.NewParser(extKeymap(t)))
		o.registers.Set('a', Register{Text: "xgDx"})
		o.registers.Set('b', Register{Text: "@aj"})
		o.Extensions = &fakeExt{}
		o.ContinueReplay(tt.segs)
		if got := dotString(o.dot); got != tt.want {
			t.Errorf("%s: dot = %s, want %s", tt.name, got, tt.want)
		}
	}
}
