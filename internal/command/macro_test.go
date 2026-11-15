package command

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/piplupflipper/modal-editor/internal/buffer"
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

// @@ は直前に再生したレジスターを、@: は直前のコマンドラインを繰り返す。どちらも前が無ければ失敗する。
//
// req: FR-05
func TestMacroRepeat(t *testing.T) {
	s := setup(t, "|a1\na2\na3\na4")
	if err := errOf(feedKeys(s, "@@")); !errors.Is(err, ErrFailed) {
		t.Errorf("@@ err = %v", err)
	}
	if err := errOf(feedKeys(s, "@:")); !errors.Is(err, ErrFailed) {
		t.Errorf("@: err = %v", err)
	}
	feedKeys(s, ":s/a/b/<CR>j@:j2@:")
	if got := s.buf.Text(); got != "b1\nb2\nb3\na4" {
		t.Errorf("text = %q", got)
	}
	s.registers.Set('q', Register{Text: "x"})
	feedKeys(s, "gg@qj@@")
	if got := s.buf.Text(); got != "1\n2\nb3\na4" {
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
	if s.registers.unnamed.Text != "a" {
		t.Errorf("無名 = %+v", s.registers.unnamed)
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
	feedKeys(s, ":s/a/aa/<CR>")
	if err := errOf(feedKeys(s, "999999999@:")); !errors.Is(err, ErrInterrupted) {
		t.Fatalf("@: err = %v", err)
	}
	if got := s.buf.Text(); got != "aaaaa" {
		t.Errorf("@: text = %q", got)
	}
	calls = 0
	s.registers.Set('b', Register{Text: "Ax<Esc>"})
	if err := errOf(feedKeys(s, "999999999@b")); !errors.Is(err, ErrInterrupted) {
		t.Fatalf("@b err = %v", err)
	}
	// 4 回目の問い合わせ（<Esc> の前）で止まるので、x を入れたインサートモードのまま残る（Vim の <C-c> と同じく途中で止める）。
	if got := s.buf.Text(); got != "aaaaax" {
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
//
// req: FR-05
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
	// 残りは @a の x、@b の j、@b の 2 回目。2 回目の @a の gD でまた移るので、そこで止まり、残りを返す。
	r := o.ContinueReplay(last.Remaining)
	if !errors.Is(r.Err, ErrBufferSwitched) {
		t.Fatalf("ContinueReplay: err = %v", r.Err)
	}
	if got := o.buf.Text(); got != "23\n56\n789" {
		t.Errorf("移った先: text = %q", got)
	}
	var rest []string
	for _, seg := range r.Remaining {
		rest = append(rest, fmt.Sprintf("%s*%d", editcmd.KeysString(seg.Keys), seg.Count))
	}
	if got := strings.Join(rest, ","); got != "x*1,j*1" {
		t.Errorf("Remaining = %s", got)
	}
}
