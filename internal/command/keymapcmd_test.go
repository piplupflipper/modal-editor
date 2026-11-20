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

// ビジュアルモードで呼ぶと、選択を抜けてから、範囲を付けずに実行する（'< と '> は記録する）。
//
// req: FR-154
func TestKeymapCommandVisual(t *testing.T) {
	s, opts, _ := newKeymapCommandState(t, "ab\ncd\nef")
	r := feedKeys(s, "vj<C-n>")
	if err := r[len(r)-1].Err; err != nil || !slices.Equal(opts.sets, []string{"lineNumber.show=true"}) {
		t.Fatalf("err = %v, sets = %q", err, opts.sets)
	}
	if s.Mode() != ModeNormal {
		t.Errorf("mode = %v", s.Mode())
	}
	if l, ok := s.mark('<'); !ok || l != 0 {
		t.Errorf("'< = %d %v", l, ok)
	}
	if l, ok := s.mark('>'); !ok || l != 1 {
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
