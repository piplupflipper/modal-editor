package command

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/piplupflipper/modal-editor/internal/buffer"
	"github.com/piplupflipper/modal-editor/internal/editcmd"
	"github.com/piplupflipper/modal-editor/internal/input"
)

// newExtCommandState は、fakeExt の dup.line をコマンドラインから :dup.line で呼べる State を返す。
func newExtCommandState(t *testing.T, text string, ext Extensions) *State {
	t.Helper()
	s := New(newBuffer(text), input.NewParser(extKeymap(t)))
	s.Extensions = ext
	s.Ex.ExtCommand = func(id string) (string, bool) {
		if id == "dup.line" {
			return "dup", true
		}
		return "", false
	}
	return s
}

// コマンドラインから呼んだ拡張機能のコマンドは、ID の後の文字列を ActionQuery.Text で渡して問い合わせ、
// 返った編集をアンドゥの 1 単位で適用する。Vim の Ex コマンドと同じく、ドットリピートには記録しない。
// マクロに記録したコマンドラインは、再生中の問い合わせ（Replaying）として呼び直す。
//
// req: FR-11
func TestExtCommandLine(t *testing.T) {
	ext := &fakeExt{}
	s := newExtCommandState(t, "ab\ncd", ext)
	for _, r := range feedKeys(s, ":dup.line この行 | を<CR>") {
		if r.Err != nil {
			t.Fatalf("err = %v", r.Err)
		}
	}
	if got := s.buf.Text(); got != "ab\nab\ncd" {
		t.Errorf("text = %q", got)
	}
	if want := []string{"action dup.line この行 | を"}; !slices.Equal(ext.calls, want) {
		t.Errorf("calls = %q, want %q", ext.calls, want)
	}
	if s.Mode() != ModeNormal {
		t.Errorf("mode = %v", s.Mode())
	}
	feedKeys(s, "j.")
	if got := s.buf.Text(); got != "ab\nab\ncd" || len(ext.calls) != 1 {
		t.Errorf(". を繰り返した: text = %q, calls = %q", got, ext.calls)
	}
	feedKeys(s, "u")
	if got := s.buf.Text(); got != "ab\ncd" {
		t.Errorf("u: text = %q", got)
	}

	ext.calls, ext.replays = nil, nil
	feedKeys(s, "qq:dup.line x<CR>q@q")
	if want := []string{"action dup.line x", "action dup.line x"}; !slices.Equal(ext.calls, want) {
		t.Errorf("マクロ: calls = %q, want %q", ext.calls, want)
	}
	if want := []bool{false, true}; !slices.Equal(ext.replays, want) {
		t.Errorf("マクロ: replays = %v, want %v", ext.replays, want)
	}

	// 拡張機能が失敗したら、操作の失敗にする。
	s.Extensions = failingAction{}
	r := s.ExecuteCommandLine("dup.line y")
	if r.Err == nil || !strings.Contains(r.Err.Error(), "boom") {
		t.Errorf("失敗: err = %v", r.Err)
	}
}

// failingAction は、操作の問い合わせに誤りを返す実装である。
type failingAction struct{ alwaysDefer }

func (failingAction) Action(editcmd.Ref, *buffer.Buffer, ActionQuery, QueryInfo) (buffer.Transaction, error) {
	return buffer.Transaction{}, errors.New("boom")
}

// 期限 0 でコマンドラインのコマンドの結果を保留したら、Deferred で返り、ApplyDeferred で同じ引数のコマンドとして適用する。
// 問い合わせの後にカーソルが動いていれば破棄する。
//
// req: FR-18
func TestExtCommandLineDeferred(t *testing.T) {
	for _, tt := range []struct {
		name, between, want string
		stale               bool
	}{
		{"適用", "", "ab\nab\ncd", false},
		{"カーソルが動いた", "j", "ab\ncd", true},
	} {
		ext := &fakeExt{deferring: true}
		s := newExtCommandState(t, "ab\ncd", ext)
		r := s.ExecuteCommandLine("dup.line z")
		if r.Err != nil || r.Deferred == nil {
			t.Fatalf("%s: Result = %+v", tt.name, r)
		}
		feedKeys(s, tt.between)
		got := &fakeExt{}
		r = s.ApplyDeferred(*r.Deferred, got)
		if tt.stale != errors.Is(r.Err, ErrStale) {
			t.Errorf("%s: err = %v", tt.name, r.Err)
		}
		if s.buf.Text() != tt.want {
			t.Errorf("%s: text = %q, want %q", tt.name, s.buf.Text(), tt.want)
		}
		if !tt.stale && !slices.Equal(got.calls, []string{"action dup.line z"}) {
			t.Errorf("%s: calls = %q", tt.name, got.calls)
		}
	}
}
