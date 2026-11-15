package command

import (
	"errors"
	"testing"

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

func equalKeys(keys []input.Key, s string) bool {
	want := input.MustParseKeys(s)
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
