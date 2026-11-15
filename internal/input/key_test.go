package input

import (
	"reflect"
	"testing"
)

// マクロはキーの列として記録するので、表記との往復で元のキーに戻ることを確かめる。
//
// req: FR-05
func TestParseKeys(t *testing.T) {
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
		{in: "<C-w>j", want: []Key{{Code: CodeRune, Rune: 'w', Mod: ModCtrl}, Rune('j')}},
		{in: "<C-A-Up>", want: []Key{{Code: CodeUp, Mod: ModCtrl | ModAlt}}},
		{in: "<S-Tab>", want: []Key{{Code: CodeTab, Mod: ModShift}}},
		{in: "<M-x>", want: []Key{{Code: CodeRune, Rune: 'x', Mod: ModAlt}}, str: "<A-x>"},
		{in: "<lt>", want: []Key{Rune('<')}},
		{in: "<C-lt>", want: []Key{{Code: CodeRune, Rune: '<', Mod: ModCtrl}}},
		{in: "<", want: []Key{Rune('<')}, str: "<lt>"},
		{in: "<<", want: []Key{Rune('<'), Rune('<')}, str: "<lt><lt>"},
		{in: "<x>", want: []Key{Rune('<'), Rune('x'), Rune('>')}, str: "<lt>x>"},
		{in: "<Q-x>", want: []Key{Rune('<'), Rune('Q'), Rune('-'), Rune('x'), Rune('>')}, str: "<lt>Q-x>"},
		{in: "<C-", want: []Key{Rune('<'), Rune('C'), Rune('-')}, str: "<lt>C-"},
	}
	for _, tt := range tests {
		got := ParseKeys(tt.in)
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
		if back := ParseKeys(KeysString(got)); !reflect.DeepEqual(back, got) {
			t.Errorf("ParseKeys(KeysString(%+v)) = %+v", got, back)
		}
	}
}

func TestKeyStringUnknownCode(t *testing.T) {
	if got, want := (Key{Code: Code(99)}).String(), "<Code99>"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}
