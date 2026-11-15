package target

import "testing"

// TestClassOf は、区画の境目の文字の分類を、Vim 9.1 の charclass() の値と比べる。
func TestClassOf(t *testing.T) {
	tests := []struct {
		r    rune
		want int
	}{
		{0x20, 0},
		{0x41, 2},
		{0x5f, 2},
		{0x2d, 1},
		{0xa0, 0},
		{0xa1, 1},
		{0xb5, 2},
		{0xbf, 1},
		{0xc0, 2},
		{0xd7, 2},
		{0xff, 2},
		{0x100, 2},
		{0x37e, 1},
		{0x2000, 0},
		{0x200b, 0},
		{0x200c, 1},
		{0x3000, 0},
		{0x3001, 1},
		{0x3002, 1},
		{0x3005, 1},
		{0x3040, classHiragana},
		{0x3042, classHiragana},
		{0x309f, classHiragana},
		{0x30a0, classKatakana},
		{0x30fc, classKatakana},
		{0x30ff, classKatakana},
		{0x3100, 2},
		{0x4e00, classIdeograph},
		{0x9fff, classIdeograph},
		{0xac00, classHangul},
		{0xd7a3, classHangul},
		{0xd7a4, 2},
		{0xff01, 1},
		{0xff10, 2},
		{0xff21, 2},
		{0xff5b, 1},
		{0xff66, 2},
		{0x1f600, 3},
		{0x1f322, 1},
		{0x20bb7, classIdeograph},
		{0x2fa1f, classIdeograph},
		{0x2fa20, 2},
		{0x10ffff, 2},
		{0, classBlank}, // 行末の位置
		{'\t', classBlank},
	}
	for _, tt := range tests {
		if got := classOf(tt.r, false); got != tt.want {
			t.Errorf("classOf(%U, false) = %d, want %d", tt.r, got, tt.want)
		}
		// W などでは、空白はそのまま、それ以外は記号になる。
		wantBig := classPunct
		if tt.want == classBlank {
			wantBig = classBlank
		}
		if got := classOf(tt.r, true); got != wantBig {
			t.Errorf("classOf(%U, true) = %d, want %d", tt.r, got, wantBig)
		}
	}
}

// TestWideClassesSorted は、表 wideClasses が昇順で重ならず、U+0100 以降だけを持ち、classWord を含まないことを確かめる（二分探索の前提）。
func TestWideClassesSorted(t *testing.T) {
	prev := rune(0xff)
	for i, c := range wideClasses {
		if c.first <= prev || c.last < c.first || c.class == classWord {
			t.Errorf("wideClasses[%d] = %+v (prev last %U)", i, c, prev)
		}
		prev = c.last
	}
}
