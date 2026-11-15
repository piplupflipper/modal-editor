package command

import (
	"reflect"
	"testing"

	"github.com/piplupflipper/modal-editor/internal/input"
)

// 名前ごとの読み書きの規則（大文字の追記、_、読み出しだけの名前、クリップボードの未対応）。
//
// req: FR-04
func TestRegistersSetGet(t *testing.T) {
	var r Registers
	if !r.Set('a', Register{Text: "x"}) || !r.Set('A', Register{Text: "y"}) {
		t.Fatal("a と A に書けない")
	}
	if got, _ := r.Get('a'); got.Text != "xy" || got.Linewise {
		t.Errorf("a = %+v", got)
	}
	if got, _ := r.Get(0); got.Text != "xy" {
		t.Errorf("無名 = %+v（最後に書いたレジスターを指す）", got)
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
	for _, name := range []rune{':', '.', '+', '*', '%'} {
		if r.Set(name, Register{Text: "x"}) {
			t.Errorf("%q に書けた", name)
		}
	}
	for _, name := range []rune{'+', '*', '%', '!'} {
		if _, ok := r.Get(name); ok {
			t.Errorf("%q を読めた", name)
		}
	}
}

// 削除の履歴は 1〜9 をずれ、9 の先は捨てる。小さな削除は - に入り、履歴をずらさない。
//
// req: FR-04
func TestRegistersDeleteHistory(t *testing.T) {
	s := setup(t, "|1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11")
	feedKeys(s, "dddddddddddddddddddd") // 10 回の dd
	for i, want := range []string{"", "10", "9", "8", "7", "6", "5", "4", "3", "2"} {
		if got := s.registers.numbered[i]; i > 0 && (got.Text != want || !got.Linewise) {
			t.Errorf("%d = %+v, want %q", i, got, want)
		}
	}
	feedKeys(s, "x")
	if s.registers.small.Text != "1" || s.registers.numbered[1].Text != "10" {
		t.Errorf("x の後: - = %+v, 1 = %+v", s.registers.small, s.registers.numbered[1])
	}
}

// . は直前にインサートモードで入力したテキスト、: は直前のコマンドラインを読み出せる。
//
// req: FR-04
func TestRegistersReadOnly(t *testing.T) {
	s := setup(t, "|abc")
	feedKeys(s, "ixy<BS>z<Esc>")
	feedKeys(s, `".p`)
	if got := s.buf.Text(); got != "xzxzabc" {
		t.Errorf(".p: text = %q", got)
	}
	feedKeys(s, ":s/q/r/e<CR>$")
	feedKeys(s, `":p`)
	if got := s.buf.Text(); got != "xzxzabcs/q/r/e" {
		t.Errorf(":p: text = %q", got)
	}
}

// マクロとして記録したキーは、Vim と同じ生のテキストでレジスターに入り、キーの列へ戻せる。
//
// req: FR-04, FR-05
func TestRawText(t *testing.T) {
	keys := input.MustParseKeys("ix<Tab><CR><BS><Esc><C-r>")
	text := rawText(keys)
	if text != "ix\t\r\x08\x1b\x12" {
		t.Errorf("rawText = %q", text)
	}
	if got := keysFromText(text); !reflect.DeepEqual(got, keys) {
		t.Errorf("keysFromText = %v, want %v", got, keys)
	}
	// 生の文字にできないキーは Vim の表記で書くが、記録したキーの列はそのまま保つ。
	up := input.MustParseKeys("<Up>")
	if got := rawText(up); got != "<Up>" {
		t.Errorf("rawText(<Up>) = %q", got)
	}
	if got := (Register{Text: "<Up>", keys: up}).macroKeys(); !reflect.DeepEqual(got, up) {
		t.Errorf("macroKeys = %v", got)
	}
	// 行単位のレジスターを再生すると、最後に <CR> が付く。
	if got := (Register{Text: "x", Linewise: true}).macroKeys(); !reflect.DeepEqual(got, input.MustParseKeys("x<CR>")) {
		t.Errorf("行単位の macroKeys = %v", got)
	}
}
