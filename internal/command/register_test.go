package command

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/piplupflipper/modal-editor/internal/editcmd"
)

// historyTexts は、履歴のテキストを新しい順に並べる。
func historyTexts(r *Registers) []string {
	out := []string{}
	for _, reg := range r.History() {
		out = append(out, reg.Text)
	}
	return out
}

// 名前は a〜z（A〜Z は追記）、_、名前の省略だけで、それ以外の名前は読み書きできない。
//
// req: FR-04, FR-50
func TestRegistersSetGet(t *testing.T) {
	var r Registers
	if !r.Set('a', Register{Text: "x"}) || !r.Set('A', Register{Text: "y"}) {
		t.Fatal("a と A に書けない")
	}
	if got, _ := r.Get('a'); got.Text != "xy" || got.Linewise {
		t.Errorf("a = %+v", got)
	}
	if got, _ := r.Get(0); got.Text != "xy" {
		t.Errorf("無名 = %+v（追記した後の内容全体が履歴の先頭）", got)
	}
	r.Set('A', Register{Text: "z", Linewise: true})
	if got, _ := r.Get('A'); got.Text != "xy\nz" || !got.Linewise {
		t.Errorf("行単位の追記 = %+v", got)
	}
	if !r.Set('_', Register{Text: "gone"}) {
		t.Error("_ に書けない")
	}
	if got, _ := r.Get('_'); !got.empty() {
		t.Errorf("_ = %+v", got)
	}
	for _, name := range []rune{'"', '0', '1', '9', '-', '.', ':', '+', '*', '%', '/', '#', '='} {
		if r.Set(name, Register{Text: "x"}) {
			t.Errorf("%q に書けた", name)
		}
		if _, ok := r.Get(name); ok {
			t.Errorf("%q を読めた", name)
		}
	}
	if got := historyTexts(&r); !slices.Equal(got, []string{"xy\nz", "xy", "x"}) {
		t.Errorf("履歴 = %q", got)
	}
}

// 無い名前を指定した操作は失敗し、ErrFailed を包まない誤り（画面に出すメッセージ）を返す。バッファもレジスターも変えない。
//
// req: FR-50
func TestInvalidRegisterName(t *testing.T) {
	for _, keys := range []string{`"0p`, `"1p`, `"-p`, `".p`, `":p`, `""p`, `"+yy`, `"0dd`, `"-x`, `"=cw`, "q0", `q"`, "@0", `@"`, "@-"} {
		t.Run(keys, func(t *testing.T) {
			s := setup(t, "|abc\ndef")
			s.registers.history = []Register{{Text: "U", filled: true}}
			var err error
			for _, r := range feedKeys(s, keys) {
				if r.Err != nil && err == nil {
					err = r.Err
				}
			}
			var ie *InvalidRegisterError
			if !errors.As(err, &ie) || errors.Is(err, ErrFailed) {
				t.Fatalf("err = %v, want *InvalidRegisterError", err)
			}
			if !strings.HasPrefix(err.Error(), "E354: ") {
				t.Errorf("message = %q", err.Error())
			}
			if got := s.buf.Text(); got != "abc\ndef" {
				t.Errorf("text = %q", got)
			}
			if got := historyTexts(&s.registers); !slices.Equal(got, []string{"U"}) {
				t.Errorf("履歴 = %q", got)
			}
			if s.Recording() != 0 {
				t.Errorf("記録している: %q", s.Recording())
			}
		})
	}
}

// 基本設計 4.5 の表のとおり、操作ごとに名前付きのレジスターと履歴が変わる。
//
// req: FR-51
func TestRegistersHistoryTable(t *testing.T) {
	cases := []struct {
		name, keys string
		history    []string // 新しい順
		a          string
	}{
		{"名前を省略したヤンクは積む", "ywwyw", []string{"two ", "one ", "U"}, "A"},
		{"名前を省略した削除と変更は積む", "dwcwX<Esc>", []string{"two", "one ", "U"}, "A"},
		{"名前を省略した x は積む", "x", []string{"o", "U"}, "A"},
		{"名前を指定したヤンクは a に書いて積む", `"ayw`, []string{"one ", "U"}, "one "},
		{"名前を指定した削除は a に書いて積む", `"adw`, []string{"one ", "U"}, "one "},
		{"大文字での追記は追記した後の全体を積む", `"Ayw`, []string{"Aone ", "U"}, "Aone "},
		{"_ は何も変えない", `"_dw"_yw`, []string{"U"}, "A"},
		{"マクロの記録は積まない", "qawq", []string{"U"}, "w"},
		{"マクロの記録の追記も積まない", "qAwq", []string{"U"}, "Aw"},
		{"貼り付けは履歴の順を変えない", `yw"apP`, []string{"one ", "U"}, "A"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := setup(t, "|one two three")
			s.registers.history = []Register{{Text: "U", filled: true}}
			s.registers.named[0] = Register{Text: "A", filled: true}
			feedKeys(s, tc.keys)
			if got := historyTexts(&s.registers); !slices.Equal(got, tc.history) {
				t.Errorf("履歴 = %q, want %q", got, tc.history)
			}
			if got, _ := s.registers.Get('a'); got.Text != tc.a {
				t.Errorf("a = %q, want %q", got.Text, tc.a)
			}
			if got, _ := s.registers.Get(0); got.Text != tc.history[0] {
				t.Errorf("無名 = %q, want 履歴の先頭 %q", got.Text, tc.history[0])
			}
		})
	}
}

// :d も名前を省略すれば履歴に積み、名前を指定すればそのレジスターに書いて積む。
//
// req: FR-51
func TestRegistersHistoryExDelete(t *testing.T) {
	s := setup(t, "|a\nb\nc")
	feedKeys(s, ":d<CR>:d b<CR>")
	if got := historyTexts(&s.registers); !slices.Equal(got, []string{"b", "a"}) {
		t.Errorf("履歴 = %q", got)
	}
	if got, _ := s.registers.Get('b'); got.Text != "b" || !got.Linewise {
		t.Errorf("b = %+v", got)
	}
}

// deleteToHistory が偽なら、名前を省略した削除と変更は履歴も無名のレジスターも変えない。
// 名前を指定した削除は、deleteToHistory によらず積む。
//
// req: FR-52
func TestRegistersDeleteToHistoryOff(t *testing.T) {
	s := setup(t, "|one two three\nfour")
	s.registers.SetConfig(RegisterConfig{DeleteToHistory: new(false)})
	feedKeys(s, "yw")
	feedKeys(s, "dwxcwX<Esc>")
	feedKeys(s, "j:d<CR>")
	if got := historyTexts(&s.registers); !slices.Equal(got, []string{"one "}) {
		t.Errorf("削除の後の履歴 = %q", got)
	}
	if got, _ := s.registers.Get(0); got.Text != "one " {
		t.Errorf("無名 = %q, want 直前のヤンク", got.Text)
	}
	feedKeys(s, `gg0"cdw`)
	if got := historyTexts(&s.registers); !slices.Equal(got, []string{"X ", "one "}) {
		t.Errorf("名前を指定した削除の後の履歴 = %q", got)
	}
	if got, _ := s.registers.Get('c'); got.Text != "X " {
		t.Errorf("c = %q", got.Text)
	}
}

// 履歴は historySize 件までで、超えたら最も古いものから捨てる。
//
// req: FR-53
func TestRegistersHistorySize(t *testing.T) {
	var r Registers
	r.SetConfig(RegisterConfig{HistorySize: 3})
	for _, s := range []string{"1", "2", "3", "4", "5"} {
		r.Set(0, Register{Text: s})
	}
	if got := historyTexts(&r); !slices.Equal(got, []string{"5", "4", "3"}) {
		t.Errorf("履歴 = %q", got)
	}
	r.SetConfig(RegisterConfig{HistorySize: 2})
	if got := historyTexts(&r); !slices.Equal(got, []string{"5", "4"}) {
		t.Errorf("件数を減らした後の履歴 = %q", got)
	}
	var d Registers
	for i := range DefaultHistorySize + 5 {
		d.Set(0, Register{Text: fmt.Sprint(i)})
	}
	if got := len(d.History()); got != DefaultHistorySize {
		t.Errorf("既定の件数 = %d", got)
	}
}

// maxEntryBytes を超えたテキストは履歴に積まずに無名のレジスターにだけ置き、積まなかったことを知らせる。
// 名前付きのレジスターには大きさによらず書く。次に履歴に積むと、上限を超えた無名のレジスターは捨てる。
//
// req: FR-53
func TestRegistersMaxEntryBytes(t *testing.T) {
	s := setup(t, "|abcdef gh")
	s.registers.SetConfig(RegisterConfig{MaxEntryBytes: 4})
	feedKeys(s, "yl")
	pushes := s.registers.Pushes()
	res := feedKeys(s, "yw")
	if msg := res[len(res)-1].Message; !msg.Warning || !strings.Contains(msg.Text, "積みませんでした") {
		t.Errorf("message = %+v", msg)
	}
	if got := historyTexts(&s.registers); !slices.Equal(got, []string{"a"}) {
		t.Errorf("履歴 = %q", got)
	}
	if s.registers.Pushes() != pushes {
		t.Errorf("上限を超えたテキストで履歴の先頭が変わった")
	}
	feedKeys(s, "$p")
	if got := s.buf.Text(); got != "abcdef ghabcdef " {
		t.Errorf("p は上限を超えた無名のレジスターを貼る: text = %q", got)
	}
	feedKeys(s, `0"ayw`)
	if got, _ := s.registers.Get('a'); got.Text != "abcdef " {
		t.Errorf("a = %q（名前付きのレジスターには書く）", got.Text)
	}
	feedKeys(s, "yl")
	if got, _ := s.registers.Get(0); got.Text != "a" {
		t.Errorf("積んだ後の無名 = %q（上限を超えた無名のレジスターは捨てる）", got.Text)
	}
	if s.registers.Pushes() != pushes+1 {
		t.Errorf("Pushes = %d, want %d", s.registers.Pushes(), pushes+1)
	}
}

// マクロとして記録したキーは、Vim と同じ生のテキストでレジスターに入り、キーの列へ戻せる。
//
// req: FR-04, FR-05
func TestRawText(t *testing.T) {
	keys := editcmd.MustParseKeys("ix<Tab><CR><BS><Esc><C-r>")
	text := rawText(keys)
	if text != "ix\t\r\x08\x1b\x12" {
		t.Errorf("rawText = %q", text)
	}
	if got := keysFromText(text); !reflect.DeepEqual(got, keys) {
		t.Errorf("keysFromText = %v, want %v", got, keys)
	}
	// 生の文字にできないキーは Vim の表記で書くが、記録したキーの列はそのまま保つ。
	up := editcmd.MustParseKeys("<Up>")
	if got := rawText(up); got != "<Up>" {
		t.Errorf("rawText(<Up>) = %q", got)
	}
	if got := (Register{Text: "<Up>", keys: up}).macroKeys(); !reflect.DeepEqual(got, up) {
		t.Errorf("macroKeys = %v", got)
	}
	// 行単位のレジスターを再生すると、最後に <CR> が付く。
	if got := (Register{Text: "x", Linewise: true}).macroKeys(); !reflect.DeepEqual(got, editcmd.MustParseKeys("x<CR>")) {
		t.Errorf("行単位の macroKeys = %v", got)
	}
}
