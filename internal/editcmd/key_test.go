package editcmd

import (
	"reflect"
	"testing"
)

// マクロはキーの列として記録するので、表記との往復で元のキーに戻ることを確かめる。
//
// req: FR-05
func TestParseKeys(t *testing.T) {
	ctrl := func(r rune) Key { return Key{Code: CodeRune, Rune: r, Mod: ModCtrl} }
	tests := []struct {
		in   string
		want []Key
		str  string // KeysString の結果。in と同じなら空
	}{
		{in: "d2w", want: []Key{Rune('d'), Rune('2'), Rune('w')}},
		{in: "", want: nil},
		{in: "あi", want: []Key{Rune('あ'), Rune('i')}},
		{in: "d<Esc>", want: []Key{Rune('d'), Esc}},
		{in: "<esc>", want: []Key{Esc}, str: "<Esc>"},
		{in: "<C-w>j", want: []Key{ctrl('w'), Rune('j')}},
		{in: "<C-A-Up>", want: []Key{{Code: CodeUp, Mod: ModCtrl | ModAlt}}},
		{in: "<S-Tab>", want: []Key{{Code: CodeTab, Mod: ModShift}}},
		{in: "<M-x>", want: []Key{{Code: CodeRune, Rune: 'x', Mod: ModAlt}}, str: "<A-x>"},
		// 基本設計 7 章の設定の例
		{in: "<Space>w", want: []Key{Rune(' '), Rune('w')}},
		{in: ":w<CR>", want: []Key{Rune(':'), Rune('w'), Special(CodeEnter)}},
		{in: " ", want: []Key{Rune(' ')}, str: "<Space>"},
		{in: "<C-Space>", want: []Key{ctrl(' ')}},
		{in: "<Bar><Bslash>", want: []Key{Rune('|'), Rune('\\')}, str: `|\`},
		{in: "<lt>", want: []Key{Rune('<')}},
		{in: "<C-lt>", want: []Key{ctrl('<')}},
		{in: "<C->>", want: []Key{ctrl('>')}},
		{in: "<C-->", want: []Key{ctrl('-')}},
		{in: "<", want: []Key{Rune('<')}, str: "<lt>"},
		{in: "<<", want: []Key{Rune('<'), Rune('<')}, str: "<lt><lt>"},
		{in: "<C-", want: []Key{Rune('<'), Rune('C'), Rune('-')}, str: "<lt>C-"},
		// 正規化
		{in: "<S-a>", want: []Key{Rune('A')}, str: "A"},
		{in: "<C-W>", want: []Key{ctrl('w')}, str: "<C-w>"},
		{in: "<C-S-a>", want: []Key{{Code: CodeRune, Rune: 'a', Mod: ModCtrl | ModShift}}},
		{in: "<A-S-a>", want: []Key{{Code: CodeRune, Rune: 'A', Mod: ModAlt}}, str: "<A-A>"},
		{in: "<S-1>", want: []Key{{Code: CodeRune, Rune: '1', Mod: ModShift}}},
	}
	for _, tt := range tests {
		got, err := ParseKeys(tt.in)
		if err != nil {
			t.Errorf("ParseKeys(%q): %v", tt.in, err)
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("ParseKeys(%q) = %+v, want %+v", tt.in, got, tt.want)
		}
		wantStr := tt.str
		if wantStr == "" {
			wantStr = tt.in
		}
		if s := KeysString(got); s != wantStr {
			t.Errorf("KeysString(ParseKeys(%q)) = %q, want %q", tt.in, s, wantStr)
		}
		if back, err := ParseKeys(KeysString(got)); err != nil || !reflect.DeepEqual(back, got) {
			t.Errorf("ParseKeys(KeysString(%+v)) = %+v, %v", got, back, err)
		}
	}
}

func TestParseKeysError(t *testing.T) {
	for _, in := range []string{"<x>", "<Foo>", "a<Spcae>", "<Q-x>", "<>", "<C-Foo>"} {
		if keys, err := ParseKeys(in); err == nil {
			t.Errorf("ParseKeys(%q) = %+v, want エラー", in, keys)
		}
	}
}

func TestMustParseKeysPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("MustParseKeys が panic しない")
		}
	}()
	MustParseKeys("<Foo>")
}

func TestKeyNormalize(t *testing.T) {
	tests := []struct{ in, want Key }{
		{Key{Code: CodeRune, Rune: 'a', Mod: ModShift}, Rune('A')},
		{Key{Code: CodeRune, Rune: 'W', Mod: ModCtrl}, Key{Code: CodeRune, Rune: 'w', Mod: ModCtrl}},
		{Key{Code: CodeRune, Rune: 'ä', Mod: ModShift}, Rune('Ä')},
		{Key{Code: CodeUp, Mod: ModShift}, Key{Code: CodeUp, Mod: ModShift}},
		{Rune('a'), Rune('a')},
	}
	for _, tt := range tests {
		if got := tt.in.Normalize(); got != tt.want {
			t.Errorf("%+v.Normalize() = %+v, want %+v", tt.in, got, tt.want)
		}
	}
}

func TestKeyStringUnknownCode(t *testing.T) {
	if got, want := (Key{Code: Code(99)}).String(), "<Code99>"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}
