package editcmd

import (
	"fmt"
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
		{in: "<Insert><F1><F12><F20>", want: []Key{Special(CodeInsert), Special(CodeF1), Special(CodeF1 + 11), Special(CodeF1 + 19)}},
		{in: "<f5><C-F2><insert>", want: []Key{Special(CodeF1 + 4), {Code: CodeF1 + 1, Mod: ModCtrl}, Special(CodeInsert)}, str: "<F5><C-F2><Insert>"},
		// マウスの出来事（FR-184）。複数回のクリックは修飾キーの後に回数を書く。
		{in: "<LeftMouse><LeftDrag><LeftRelease><ScrollWheelUp><ScrollWheelRight>", want: []Key{
			Special(CodeLeftMouse), Special(CodeLeftDrag), Special(CodeLeftRelease), Special(CodeScrollWheelUp), Special(CodeScrollWheelRight),
		}},
		{in: "<2-LeftMouse><3-leftmouse><S-4-RightMouse>", want: []Key{
			{Code: CodeLeftMouse, Mod: ModClick2}, {Code: CodeLeftMouse, Mod: ModClick3}, {Code: CodeRightMouse, Mod: ModShift | ModClick4},
		}, str: "<2-LeftMouse><3-LeftMouse><S-4-RightMouse>"},
		{in: "<2-C-MiddleMouse>", want: []Key{{Code: CodeMiddleMouse, Mod: ModCtrl | ModClick2}}, str: "<C-2-MiddleMouse>"},
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
	for _, in := range []string{"<x>", "<Foo>", "a<Spcae>", "<Q-x>", "<>", "<C-Foo>", "<F0>", "<F21>", "<2-a>", "<2-LeftDrag>", "<3-Esc>"} {
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

func TestFunctionKey(t *testing.T) {
	for n := 1; n <= MaxFunctionKey; n++ {
		c, ok := FunctionKey(n)
		if !ok || c != CodeF1+Code(n-1) {
			t.Fatalf("FunctionKey(%d) = %v, %v", n, c, ok)
		}
		if got, want := Special(c).String(), fmt.Sprintf("<F%d>", n); got != want {
			t.Errorf("FunctionKey(%d) の表記 = %q, want %q", n, got, want)
		}
	}
	for _, n := range []int{0, -1, MaxFunctionKey + 1} {
		if c, ok := FunctionKey(n); ok {
			t.Errorf("FunctionKey(%d) = %v, true, want false", n, c)
		}
	}
}

// マウスの出来事のキーの種類と、クリックの回数の読み書き（FR-184）。
//
// req: FR-184
func TestMouseKey(t *testing.T) {
	for c := CodeLeftMouse; c <= CodeScrollWheelRight; c++ {
		if !Special(c).IsMouse() {
			t.Errorf("%v.IsMouse() = false", Special(c))
		}
	}
	for _, k := range []Key{Esc, Rune('a'), Special(CodeF1 + MaxFunctionKey - 1)} {
		if k.IsMouse() {
			t.Errorf("%v.IsMouse() = true", k)
		}
	}
	k := Special(CodeLeftMouse)
	for n := 1; n <= 4; n++ {
		if got := k.WithClicks(n).Clicks(); got != n {
			t.Errorf("WithClicks(%d).Clicks() = %d", n, got)
		}
	}
	if got := k.WithClicks(9).Clicks(); got != 4 {
		t.Errorf("WithClicks(9).Clicks() = %d, want 4", got)
	}
	if got := (Key{Code: CodeLeftMouse, Mod: ModShift}).WithClicks(2); got.Mod != ModShift|ModClick2 {
		t.Errorf("修飾キーを残さない: %+v", got)
	}
	if !Special(CodeRightMouse).IsMousePress() || Special(CodeLeftDrag).IsMousePress() {
		t.Error("IsMousePress の判定が違う")
	}
}

func TestKeyStringUnknownCode(t *testing.T) {
	if got, want := (Key{Code: Code(99)}).String(), "<Code99>"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}
