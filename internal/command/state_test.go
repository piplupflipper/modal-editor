package command

import (
	"errors"
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/editcmd"
)

// errOf は、結果の列の最初のエラーを返す。
func errOf(rs []Result) error {
	for _, r := range rs {
		if r.Err != nil {
			return r.Err
		}
	}
	return nil
}

// キー列を入れると、バッファの内容とカーソルが変わる（issue #32 の受入条件の例）。
// 失敗した操作はバッファを変えずに ErrFailed を返し、次のキーからは普通に続けられる。
//
// req: FR-02
func TestFeedOperator(t *testing.T) {
	tests := []struct {
		text, keys string
		want       string
		cursor     string
		fail       bool
	}{
		{"|abc def", "dw", "def", "0:0", false},
		{"abc |def", "d2w", "abc ", "0:3", false},
		{"|abc def", "dfz", "abc def", "0:0", true},
		{"|abc def", "dq", "abc def", "0:0", true},
		{"|abc def", "dfzx", "bc def", "0:0", true},
		{"a\n|b", "2dd", "a\nb", "1:0", true},
		{"|a", "u", "a", "0:0", true},
		{"|abc def", "y<Esc>dw", "def", "0:0", false},
	}
	for _, tt := range tests {
		s := setup(t, tt.text)
		err := errOf(feedKeys(s, tt.keys))
		if got := s.buf.Text(); got != tt.want {
			t.Errorf("%q %s: text = %q, want %q", tt.text, tt.keys, got, tt.want)
		}
		if got := s.Cursor().String(); got != tt.cursor {
			t.Errorf("%q %s: cursor = %s, want %s", tt.text, tt.keys, got, tt.cursor)
		}
		if tt.fail != errors.Is(err, ErrFailed) {
			t.Errorf("%q %s: err = %v, want failure %v", tt.text, tt.keys, err, tt.fail)
		}
	}
}

// 編集した結果の Changes は、適用した順に、バージョン番号がつながって並ぶ（拡張機能へ差分を送る材料）。
func TestFeedChanges(t *testing.T) {
	s := setup(t, "|a\nb\nc")
	r := s.Feed(editcmd.Rune('J'))
	if len(r.Changes) != 1 || r.Changes[0].Before != 0 || r.Changes[0].After != 1 {
		t.Fatalf("J の Changes = %+v", r.Changes)
	}
	// インサートモードでは、キーごとに変更が返る。
	feedKeys(s, "A")
	for i, k := range editcmd.MustParseKeys("xy<BS>") {
		r := s.Feed(k)
		if len(r.Changes) != 1 || r.Changes[0].Before != uint64(i+1) {
			t.Errorf("インサートモードの %v の Changes = %+v", k, r.Changes)
		}
	}
	if r := s.Feed(editcmd.Esc); len(r.Changes) != 0 {
		t.Errorf("<Esc> の Changes = %+v", r.Changes)
	}
	if got := s.buf.Text(); got != "a bx\nc" {
		t.Errorf("text = %q", got)
	}
}

// モードは、i、v、V、: で移り、<Esc> でノーマルモードへ戻る。
//
// req: FR-01
func TestModes(t *testing.T) {
	tests := []struct {
		keys string
		want Mode
	}{
		{"i", ModeInsert},
		{"i<Esc>", ModeNormal},
		{"cw", ModeInsert},
		{"v", ModeVisual},
		{"V", ModeVisualLine},
		{"vV", ModeVisualLine},
		{"vv", ModeNormal},
		{"v<Esc>", ModeNormal},
		{`v"<Esc>`, ModeVisual}, // 解釈の途中の <Esc> は解釈だけを捨てる
		{"vd", ModeNormal},
		{"vc", ModeInsert},
		{":", ModeCommandLine},
		{":<Esc>", ModeNormal},
		{":<BS>", ModeNormal},
		{"v:", ModeCommandLine},
	}
	for _, tt := range tests {
		s := setup(t, "|abc def")
		feedKeys(s, tt.keys)
		if s.Mode() != tt.want {
			t.Errorf("%s: mode = %v, want %v", tt.keys, s.Mode(), tt.want)
		}
	}
}

// インサートモードの入力 1 回分は、c の削除や回数の繰り返しも含めて、アンドゥの 1 単位になる。
// ドットリピートで再実行したインサートモードの入力も 1 単位である。
//
// req: FR-07
func TestInsertUndoUnit(t *testing.T) {
	tests := []struct {
		text, keys, want string
	}{
		{"|abc", "ixy<CR>z<Esc>u", "abc"},
		{"|abc def", "cwxy<Esc>u", "abc def"},
		{"|abc", "3ixy<Esc>u", "abc"},
		{"|abc\ndef", "3oxy<Esc>u", "abc\ndef"},
		{"|abc\ndef", "cwxy<Esc>j0.u", "xy\ndef"},
		{"|abc", "ix<Esc>ay<Esc>uu", "abc"},
		{"|abc", "ix<Esc>ay<Esc>u", "xabc"},
		{"|abc", "ixy<Esc>u<C-r>", "xyabc"},
	}
	for _, tt := range tests {
		s := setup(t, tt.text)
		feedKeys(s, tt.keys)
		if got := s.buf.Text(); got != tt.want {
			t.Errorf("%q %s: text = %q, want %q", tt.text, tt.keys, got, tt.want)
		}
	}
}

// 999999999p のような、メモリを使い切る量の挿入は失敗する。
func TestPutTooLarge(t *testing.T) {
	s := setup(t, "|abc")
	if err := errOf(feedKeys(s, "yl999999999p")); !errors.Is(err, ErrFailed) {
		t.Errorf("err = %v", err)
	}
	if got := s.buf.Text(); got != "abc" {
		t.Errorf("text = %q", got)
	}
}
