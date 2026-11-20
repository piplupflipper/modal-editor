package command

import (
	"errors"
	"slices"
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
	"github.com/piplupflipper/vi-imitation/internal/command/ex"
	"github.com/piplupflipper/vi-imitation/internal/editcmd"
	"github.com/piplupflipper/vi-imitation/internal/input"
	"github.com/piplupflipper/vi-imitation/internal/option"
)

// toggleOptions は、設定の値を読み書きする口（ex.Options）の偽物である。Set した "<名前>=<値>" を順に記録する。
type toggleOptions struct {
	vals map[string]option.Value
	sets []string
}

func (o *toggleOptions) Get(d option.Def) option.Value {
	if v, ok := o.vals[d.Name]; ok {
		return v
	}
	return d.Default
}

func (o *toggleOptions) Set(d option.Def, v option.Value) error {
	if o.vals == nil {
		o.vals = map[string]option.Value{}
	}
	o.vals[d.Name] = v
	o.sets = append(o.sets, d.Name+"="+v.String())
	return nil
}

func (o *toggleOptions) Revision(option.Def) uint64 { return uint64(len(o.sets)) }

// newKeymapCommandState は、設定の keymap の Ex コマンド（FR-154）を割り当てた State を返す。
// 割り当ては internal/editor の keymapCommand が作るものと同じ形（Ref.Extension が ExCommandExtension、Ref.ID がコマンドライン）にする。
//
//	ノーマルとビジュアルの <C-n>、インサートの <C-n>  option.toggle lineNumber.show
//	ノーマルの <C-e>                                  option.toggle filetype（E488 になる）
//	ノーマルの <C-d>                                  dup.line x（fakeExt の拡張機能のコマンド）
func newKeymapCommandState(t *testing.T, text string) (*State, *toggleOptions, *fakeExt) {
	t.Helper()
	km := extKeymap(t)
	for _, b := range []struct {
		modes input.MapMode
		keys  string
		line  string
	}{
		{input.MapNormal | input.MapVisual, "<C-n>", "option.toggle lineNumber.show"},
		{input.MapInsert, "<C-n>", "option.toggle lineNumber.show"},
		{input.MapNormal, "<C-e>", "option.toggle filetype"},
		{input.MapNormal, "<C-d>", "dup.line x"},
		{input.MapInsert, "<C-d>", "dup.line x"},
	} {
		err := km.Override(input.Binding{
			Kind: input.BindAction, Modes: b.modes, Keys: editcmd.MustParseKeys(b.keys),
			Ref: editcmd.Ref{ID: b.line, Extension: ExCommandExtension}, User: true,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	ext := &fakeExt{}
	s := newExtCommandState(t, text, ext)
	s.parser = input.NewParser(km)
	opts := &toggleOptions{}
	s.Ex.Options = opts
	return s, opts, ext
}

// 設定の keymap に書いた Ex コマンドは、コマンドラインモードへ移らずに、コマンドラインで打ったときと同じ口で実行する。
// 誤りは : で打ったときと同じ誤りになる。拡張機能のコマンドも呼べる。
//
// req: FR-154
func TestKeymapCommandNormal(t *testing.T) {
	s, opts, ext := newKeymapCommandState(t, "ab\ncd")
	r := s.Feed(editcmd.MustParseKeys("<C-n>")[0])
	if r.Err != nil || !slices.Equal(opts.sets, []string{"lineNumber.show=true"}) {
		t.Fatalf("<C-n>: err = %v, sets = %q", r.Err, opts.sets)
	}
	feedKeys(s, "<C-n>")
	if !slices.Equal(opts.sets, []string{"lineNumber.show=true", "lineNumber.show=false"}) {
		t.Errorf("2 回目の <C-n>: sets = %q", opts.sets)
	}
	if s.Mode() != ModeNormal || s.CommandLine() != "" {
		t.Errorf("mode = %v, cmdline = %q", s.Mode(), s.CommandLine())
	}

	// 誤りは、コマンドラインで打ったときと同じ *ex.Error になる。
	r = s.Feed(editcmd.MustParseKeys("<C-e>")[0])
	want := s.ExecuteCommandLine("option.toggle filetype").Err
	var got, typed *ex.Error
	if !errors.As(r.Err, &got) || !errors.As(want, &typed) || *got != *typed || got.Code != "E488" {
		t.Errorf("<C-e>: err = %v, : で打った err = %v", r.Err, want)
	}

	// 拡張機能のコマンド。ドットリピートには記録しない（: で打ったときと同じ）。
	feedKeys(s, "<C-d>")
	if got := s.buf.Text(); got != "ab\nab\ncd" || !slices.Equal(ext.calls, []string{"action dup.line x"}) {
		t.Errorf("<C-d>: text = %q, calls = %q", got, ext.calls)
	}
	feedKeys(s, "x<C-n>j.")
	if got := s.buf.Text(); got != "b\nb\ncd" || len(ext.calls) != 1 {
		t.Errorf("x<C-n>j.: text = %q, calls = %q", got, ext.calls)
	}
}

// ビジュアルモードで呼ぶと、選択を残したまま、範囲を付けずに実行する（Vim 9.1 の <Cmd> の割り当てと同じ）。
// 行末の位置のカーソル（vj$）も動かさない。
//
// req: FR-154
func TestKeymapCommandVisual(t *testing.T) {
	s, opts, _ := newKeymapCommandState(t, "abc\ndefgh\nij")
	r := feedKeys(s, "lvj$<C-n>")
	if err := r[len(r)-1].Err; err != nil || !slices.Equal(opts.sets, []string{"lineNumber.show=true"}) {
		t.Fatalf("err = %v, sets = %q", err, opts.sets)
	}
	start, end, _, ok := s.Selection()
	if s.Mode() != ModeVisual || !ok || start != (buffer.Position{Line: 0, Byte: 1}) || end.Line != 1 {
		t.Errorf("mode = %v, selection = %v-%v %v", s.Mode(), start, end, ok)
	}
	if c := s.Cursor(); c.Line != 1 || c.Byte < 4 {
		t.Errorf("cursor = %v", c)
	}
	// 続けて選択を操作できる（$ で行末を越えて選んだので、改行も消す。Vim と同じ）。
	feedKeys(s, "d")
	if got := s.buf.Text(); got != "aij" {
		t.Errorf("d: text = %q", got)
	}
}

// インサートモードのまま別のバッファへ移ったら（keymap の Ex コマンドの :tab.new）、移る前のバッファの入力を終え、
// 移った先でインサートモードを続ける（SuspendMode と ResumeMode。Vim 9.1 の <Cmd>tabnew<CR> と同じ）。
// 移る前のバッファはノーマルモードに戻り、カーソルは左へ戻さない。回数の繰り返しとドットリピートは、移る前の入力と合わせて行う。
// ビジュアルモードのまま移ったら、選択を抜ける。
//
// req: FR-154
func TestKeymapCommandSwitchMode(t *testing.T) {
	// 期待値は、2026-11-20 に Vim 9.1 で 3ifoo<C-x>bar<Esc>（<C-x> は <Cmd>tabnew<CR>）を abcd の行で打った結果である。
	old := New(newBuffer("abcd"), input.NewParser(input.DefaultKeymap()))
	next := New(newBuffer(""), input.NewParser(input.DefaultKeymap()))
	feedKeys(old, "3ifoo")
	carry := old.SuspendMode()
	next.TakeOver(old)
	next.ResumeMode(carry)
	if old.Mode() != ModeNormal || next.Mode() != ModeInsert {
		t.Fatalf("mode: old %v, next %v", old.Mode(), next.Mode())
	}
	if c := old.Cursor(); c != (buffer.Position{Line: 0, Byte: 3}) {
		t.Errorf("old cursor = %v", c)
	}
	feedKeys(next, "bar<Esc>")
	if got, want := next.buf.Text(), "barfoobarfoobar"; got != want {
		t.Errorf("next text = %q, want %q", got, want)
	}
	if got := old.buf.Text(); got != "fooabcd" {
		t.Errorf("old text = %q", got)
	}
	feedKeys(old, "u")
	feedKeys(next, "u")
	if old.buf.Text() != "abcd" || next.buf.Text() != "" {
		t.Errorf("u: old %q, next %q", old.buf.Text(), next.buf.Text())
	}
	feedKeys(next, ".")
	if got := next.buf.Text(); got != "foobarfoobarfoobar" {
		t.Errorf(". = %q", got)
	}

	// ビジュアルモードのまま移ると、選択を抜けて '< と '> を記録する。移った先はノーマルモードのままである。
	v := New(newBuffer("ab\ncd"), input.NewParser(input.DefaultKeymap()))
	feedKeys(v, "vj")
	carry = v.SuspendMode()
	other := New(newBuffer("x"), input.NewParser(input.DefaultKeymap()))
	other.ResumeMode(carry)
	if v.Mode() != ModeNormal || other.Mode() != ModeNormal {
		t.Errorf("visual: mode %v, other %v", v.Mode(), other.Mode())
	}
	if l, ok := v.mark('>'); !ok || l != 1 {
		t.Errorf("'> = %d %v", l, ok)
	}
}

// インサートモードで呼ぶと、インサートモードのまま、入力を区切らずに実行する。
// Vim 9.1 の <Cmd> の割り当てと同じく、ifoo<C-n>bar<Esc> の u は foobar を一度に戻し、. は foobar を入れる。
// 行末の位置のカーソルは、行末の位置のままにする。
//
// req: FR-154
func TestKeymapCommandInsert(t *testing.T) {
	s, opts, _ := newKeymapCommandState(t, "a\nb")
	feedKeys(s, "ifoo<C-n>")
	if s.Mode() != ModeInsert || !slices.Equal(opts.sets, []string{"lineNumber.show=true"}) {
		t.Fatalf("mode = %v, sets = %q", s.Mode(), opts.sets)
	}
	if c := s.Cursor(); c != (buffer.Position{Line: 0, Byte: 3}) {
		t.Errorf("cursor = %v", c)
	}
	feedKeys(s, "bar<Esc>j0.")
	if got := s.buf.Text(); got != "foobara\nfoobarb" {
		t.Errorf("text = %q", got)
	}
	feedKeys(s, "uku")
	if got := s.buf.Text(); got != "a\nb" {
		t.Errorf("u の後: text = %q", got)
	}

	// 行末の位置。
	feedKeys(s, "A<C-n>")
	if c := s.Cursor(); c != (buffer.Position{Line: 0, Byte: 1}) || s.Mode() != ModeInsert {
		t.Errorf("A<C-n>: cursor = %v, mode = %v", c, s.Mode())
	}
	feedKeys(s, "x<Esc>")
	if got := s.buf.Text(); got != "ax\nb" {
		t.Errorf("A<C-n>x: text = %q", got)
	}
}

// インサートモードで呼んだ拡張機能のコマンドが期限までに応えず保留されても（ErrDeferred）、届いた結果は、
// インサートモードのままでバッファとカーソルが変わっていなければ適用する（ノーマルモードの保留と同じ規則）。
//
// req: FR-154
func TestKeymapCommandInsertDeferred(t *testing.T) {
	s, _, ext := newKeymapCommandState(t, "ab")
	ext.deferring = true
	r := feedKeys(s, "Afoo<C-d>")
	d := r[len(r)-1].Deferred
	if d == nil || s.Mode() != ModeInsert {
		t.Fatalf("Deferred = %v, mode = %v", d, s.Mode())
	}
	res := s.ApplyDeferred(*d, &fakeExt{})
	if res.Err != nil || s.buf.Text() != "abfoo\nabfoo" || s.Mode() != ModeInsert {
		t.Errorf("ApplyDeferred: err = %v, text = %q, mode = %v", res.Err, s.buf.Text(), s.Mode())
	}
	feedKeys(s, "!<Esc>")
	if got := s.buf.Text(); got != "abfoo!\nabfoo" {
		t.Errorf("続けて入力した後の text = %q", got)
	}
}

// マクロには打ったキーを記録し、再生ではキーから同じ割り当てを引き直して実行する（操作の割り当てと同じ）。
//
// req: FR-154
func TestKeymapCommandMacro(t *testing.T) {
	s, opts, ext := newKeymapCommandState(t, "ab")
	feedKeys(s, "qa<C-n><C-d>q@a")
	if want := []string{"lineNumber.show=true", "lineNumber.show=false"}; !slices.Equal(opts.sets, want) {
		t.Errorf("sets = %q, want %q", opts.sets, want)
	}
	if want := []bool{false, true}; !slices.Equal(ext.replays, want) {
		t.Errorf("replays = %v, want %v", ext.replays, want)
	}
	if got := s.buf.Text(); got != "ab\nab\nab" {
		t.Errorf("text = %q", got)
	}
	if reg, _ := s.Registers().Get('a'); reg.Text != "\x0e\x04" {
		t.Errorf("レジスター a = %q", reg.Text)
	}
}
