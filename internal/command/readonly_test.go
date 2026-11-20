package command

import (
	"errors"
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
	"github.com/piplupflipper/vi-imitation/internal/command/ex"
	"github.com/piplupflipper/vi-imitation/internal/input"
)

// readOnlyText は、編集の禁止の突き合わせに使うバッファの内容である（Vim 9.1 で確かめたときと同じ）。
const readOnlyText = "|foo bar\n  baz qux\nthree"

// readOnlyCases は、Vim 9.1 で 'modifiable' を切ったバッファに keys を入れたとき、E21 になるか（refused）である。
// Vim の結果は、`vim -Nu NONE -n --not-a-term` で setline() の後に :set nomodifiable とし、
// `:silent! exe "normal " . keys . "\<Esc>"` の後の v:errmsg、" のレジスター、バッファの内容を見て取った（PR の説明に方法を書いた）。
// E21 になったものはどれも、バッファもレジスターも変わらなかった。
var readOnlyCases = []struct {
	keys    string
	refused bool
}{
	{"x", true},
	{"X", true},
	{"dw", true},
	{"dd", true},
	{"D", true},
	{"C", true},
	{"cw", true},
	{"S", true},
	{"rX", true},
	{"~", true},
	{"g~w", true},
	{"gUw", true},
	{"guw", true},
	{"J", true},
	{"p", true},
	{"P", true},
	{">>", true},
	{"<lt><lt>", true},
	{"u", true},
	{"<C-r>", true},
	{"U", true},
	{"i", true},
	{"a", true},
	{"I", true},
	{"A", true},
	{"o", true},
	{"O", true},
	{"vd", true},
	{"Vd", true},
	{"v>", true},
	{"vU", true},
	{"v~", true},
	{"yy", false},
	{"yw", false},
	{"vy", false},
	{"Y", false},
	{"w", false},
	{"j$", false},
	{"/bar<CR>", false},
	{"qaq", false},
	// Vim の > と < はオペレーターで次のキーを待つ（v:errmsg は空）。このエディタの > と < は >> と << と同じなので断る（FR-76）。
	{">", true},
	{"<lt>", true},
	// sa、sd、sr は vim-sandwich の操作で、Vim 9.1 だけでは確かめられない。テキストを変えるので断る。
	// Vim の s（cl と同じ）、ビジュアルモードの J と p はこのエディタに無いので表から除いた（Vim ではどれも E21）。
	{"saiw)", true},
	{"sd(", true},
	{"sr([", true},
}

// 編集を禁止したバッファでは、テキストを変える操作とインサートモードへ移る操作を、実行の前に E21 で断る。
// バッファ、レジスター、モードは変わらず、移動、検索、ヤンク、ビジュアルモードは使える。
//
// req: FR-85
func TestReadOnlyRefusesChanges(t *testing.T) {
	for _, tc := range readOnlyCases {
		t.Run(tc.keys, func(t *testing.T) {
			s := setup(t, readOnlyText)
			// u と <C-r> が「取り消すものが無い」で失敗するのではないことを確かめるため、履歴を作ってから禁止する。
			feedKeys(s, "xu")
			s.registers.history = []Register{{Text: "U", filled: true}}
			s.registers.named[0] = Register{Text: "x"}
			s.buf.SetReadOnly(true)
			v := s.buf.Version()
			err := errOf(feedKeys(s, tc.keys+"<Esc>"))
			if tc.refused != errors.Is(err, buffer.ErrReadOnly) {
				t.Errorf("err = %v, want refused %v", err, tc.refused)
			}
			if got := s.buf.Text(); got != "foo bar\n  baz qux\nthree" || s.buf.Version() != v {
				t.Errorf("text = %q (version %d -> %d)", got, v, s.buf.Version())
			}
			if unnamed, _ := s.registers.Get(0); tc.refused && unnamed.Text != "U" {
				t.Errorf(`register " = %q, want "U"`, unnamed.Text)
			}
			if s.Mode() != ModeNormal {
				t.Errorf("mode = %v", s.Mode())
			}
		})
	}
}

// keepsTextActions の ID は、どれも本体の操作の表にある（綴りの誤りで、テキストを変えない操作を断らないようにする）。
func TestKeepsTextActionsExist(t *testing.T) {
	for id := range keepsTextActions {
		if _, ok := actions[id]; !ok {
			t.Errorf("keepsTextActions has unknown action %q", id)
		}
	}
}

// 禁止中の端末からの貼り付けは E21 で断り、貼ったテキストを捨てる。ドットリピートとマクロの記録は変えない（PR #199 のレビューの必須 1）。
// Vim 9.1 を tmux 3.4 の paste-buffer -p で動かした結果: abc、def で x、禁止して ZZZ を貼り、解いて j. の後は bc、ef。
//
// req: FR-85
func TestReadOnlyPaste(t *testing.T) {
	s := setup(t, "|abc\ndef")
	feedKeys(s, "x")
	s.buf.SetReadOnly(true)
	feedKeys(s, "qa")
	if res := s.Paste("ZZZ"); !errors.Is(res.Err, buffer.ErrReadOnly) {
		t.Fatalf("Paste err = %v", res.Err)
	}
	feedKeys(s, "q")
	if s.Mode() != ModeNormal || s.buf.Text() != "bc\ndef" {
		t.Errorf("after paste: mode %v text %q", s.Mode(), s.buf.Text())
	}
	if reg, _ := s.registers.Get('a'); reg.Text != "" {
		t.Errorf("recorded macro = %q, want empty", reg.Text)
	}
	s.buf.SetReadOnly(false)
	feedKeys(s, "j.")
	if got := s.buf.Text(); got != "bc\nef" {
		t.Errorf("text after j. = %q, want %q", got, "bc\nef")
	}

	// ビジュアルモードの貼り付けも断り、モードと選択は残る。
	s.buf.SetReadOnly(true)
	feedKeys(s, "vl")
	if res := s.Paste("ZZZ"); !errors.Is(res.Err, buffer.ErrReadOnly) {
		t.Errorf("visual Paste err = %v", res.Err)
	}
	if s.Mode() != ModeVisual || s.buf.Text() != "bc\nef" {
		t.Errorf("after visual paste: mode %v text %q", s.Mode(), s.buf.Text())
	}
}

// 禁止を解けば、禁止の前のアンドゥの履歴で戻れ、編集できる。
//
// req: FR-85
func TestReadOnlyKeepsUndoHistory(t *testing.T) {
	s := setup(t, "|abc")
	feedKeys(s, "x")
	s.buf.SetReadOnly(true)
	if err := errOf(feedKeys(s, "u")); !errors.Is(err, buffer.ErrReadOnly) {
		t.Fatalf("u err = %v", err)
	}
	s.buf.SetReadOnly(false)
	feedKeys(s, "u")
	if got := s.buf.Text(); got != "abc" {
		t.Errorf("text after u = %q", got)
	}
}

// ドットリピートとマクロの再生は、禁止中のバッファを変える操作が断られた時点で止まる（FR-19 と同じく失敗で止まる）。
//
// req: FR-85
func TestReadOnlyStopsReplay(t *testing.T) {
	s := setup(t, "|ab\ncd\nef")
	feedKeys(s, "qajxq")
	feedKeys(s, "gg")
	s.buf.SetReadOnly(true)
	before := s.buf.Text()
	err := errOf(feedKeys(s, "@a"))
	if !errors.Is(err, buffer.ErrReadOnly) {
		t.Fatalf("@a err = %v", err)
	}
	// j は動き、x で止まる。
	if s.Cursor().Line != 1 || s.buf.Text() != before {
		t.Errorf("after @a: cursor %v text %q", s.Cursor(), s.buf.Text())
	}
	if err := errOf(feedKeys(s, ".")); !errors.Is(err, buffer.ErrReadOnly) {
		t.Errorf(". err = %v", err)
	}
	if s.buf.Text() != before {
		t.Errorf("text after . = %q", s.buf.Text())
	}
}

// Ex コマンドのうちテキストを変えるもの（:s、:d、:r、:{range}!）は、範囲と引数を確かめる前に E21 で断る（Vim 9.1 と同じ）。
// :g は断らず、cmd の中のコマンドが断られる。:w は断らない。
//
// req: FR-85
func TestReadOnlyExCommands(t *testing.T) {
	tests := []struct {
		cmdline string
		code    string
	}{
		{"d", "E21"},
		{"5d", "E21"},
		{"2,1d", "E21"},
		{"d x", "E21"},
		{"s/foo/x/", "E21"},
		{"s/zzz/x/", "E21"},
		// 印の付く行の無い :g は、禁止と関わりなく E486 になる（このエディタの :g の動作。Vim 9.1 でも E21 にはならない）。
		{"g/foo/d", "E21"},
		{"g/zzz/d", "E486"},
		{"g/foo/s/o/0/", "E21"},
		{"r !echo hi", "E21"},
		{"%!sort", "E21"},
		{"w", ""},
	}
	for _, tt := range tests {
		t.Run(tt.cmdline, func(t *testing.T) {
			s := setup(t, readOnlyText)
			s.buf.SetReadOnly(true)
			res := s.ExecuteCommandLine(tt.cmdline)
			var e *ex.Error
			code := ""
			if errors.As(res.Err, &e) {
				code = e.Code
			} else if res.Err != nil {
				t.Fatalf("err = %v", res.Err)
			}
			if code != tt.code {
				t.Errorf("code = %q, want %q (err %v)", code, tt.code, res.Err)
			}
			if res.Shell != nil {
				t.Error("shell requested on a read-only buffer")
			}
			if got := s.buf.Text(); got != "foo bar\n  baz qux\nthree" {
				t.Errorf("text = %q", got)
			}
		})
	}
}

// :readonly は編集の禁止を切り替え、切り替えた後の状態を通知で知らせる。
//
// req: FR-86
func TestReadOnlyCommand(t *testing.T) {
	s := setup(t, "|abc")
	res := s.ExecuteCommandLine("readonly")
	if res.Err != nil || !s.buf.ReadOnly() || res.Message.Text != "編集を禁止しました" {
		t.Fatalf(":readonly: err %v, ReadOnly %v, message %q", res.Err, s.buf.ReadOnly(), res.Message.Text)
	}
	if err := errOf(feedKeys(s, "x")); !errors.Is(err, buffer.ErrReadOnly) {
		t.Errorf("x err = %v", err)
	}
	res = s.ExecuteCommandLine("reado")
	if res.Err != nil || s.buf.ReadOnly() || res.Message.Text != "編集の禁止を解きました" {
		t.Fatalf(":reado: err %v, ReadOnly %v, message %q", res.Err, s.buf.ReadOnly(), res.Message.Text)
	}
	feedKeys(s, "x")
	if got := s.buf.Text(); got != "bc" {
		t.Errorf("text = %q", got)
	}
	// :rea は :read のまま（:reado から :readonly）。
	if res := s.ExecuteCommandLine("rea"); res.Err == nil {
		t.Error(":rea succeeded")
	}
	if res := s.ExecuteCommandLine("readonly x"); res.Err == nil {
		t.Error(":readonly x succeeded")
	}
}

// 拡張機能のオペレーターと操作は、問い合わせた結果のトランザクションを buffer.Buffer.Apply が断る。
// テキストを変えない拡張機能の操作（空のトランザクション）は、禁止中でも使える。
//
// req: FR-85
func TestReadOnlyExtension(t *testing.T) {
	ext := &fakeExt{}
	s := New(newBuffer("ab cd"), input.NewParser(extKeymap(t)))
	s.Extensions = ext
	s.buf.SetReadOnly(true)
	for _, keys := range []string{"ysiw)", "gD"} {
		if err := errOf(feedKeys(s, keys)); !errors.Is(err, buffer.ErrReadOnly) {
			t.Errorf("%s: err = %v", keys, err)
		}
	}
	if got := s.buf.Text(); got != "ab cd" {
		t.Errorf("text = %q", got)
	}
	if err := errOf(feedKeys(s, "gx")); err != nil && !errors.Is(err, ErrFailed) {
		t.Errorf("gx (motion) err = %v", err)
	}
}
