package ui

import (
	"strings"
	"testing"
)

// 既定のアイコンの表は、ディレクトリ（開いたものと閉じたもの）と種類の分からないファイルのアイコンを持ち、
// どのアイコンも CheckIcon を通る（1 つの文字で、表示幅が 1 か 2）。名前はどれもアイコンの名前の形で、色は文字の色を持つ。
//
// req: FR-152
func TestDefaultIcons(t *testing.T) {
	ic := DefaultIcons()
	for _, name := range []string{IconDirectory, IconDirectoryOpen, IconFile} {
		if _, ok := ic[name]; !ok {
			t.Errorf("既定の表に %s が無い", name)
		}
	}
	for name, icon := range ic {
		if ft, ok := strings.CutPrefix(name, IconFileTypePrefix); (!ok || ft == "") && name != IconDirectory && name != IconDirectoryOpen && name != IconFile {
			t.Errorf("%q はアイコンの名前でない", name)
		}
		if err := CheckIcon(icon.Text); err != nil {
			t.Errorf("%s: %q: %v", name, icon.Text, err)
		}
		if icon.Attr.Fg == ColorDefault {
			t.Errorf("%s: 文字の色が無い", name)
		}
	}
	// 写しを返すので、書き換えても次の呼び出しに残らない。
	delete(ic, IconFile)
	if _, ok := DefaultIcons()[IconFile]; !ok {
		t.Error("DefaultIcons が同じ表を返す")
	}
}

// Lookup は、ディレクトリなら開閉で、ファイルならファイルの種類でアイコンを引き、種類が空か表に無ければ IconFile にする。
// With は名前ごとに置き換え（既定の文字と色を混ぜない）、元の表を変えない。Width は表のアイコンの表示幅の最大である。
// WithIcons は、アイコンの色を IconStyle の名前で足す。
//
// req: FR-152
func TestIconsLookup(t *testing.T) {
	ic := Icons{
		IconDirectory: {Text: "d"}, IconDirectoryOpen: {Text: "o"}, IconFile: {Text: "f"},
		IconFileType("go"): {Text: "g", Attr: Attr{Fg: RGB(0, 0xad, 0xd8)}},
	}
	for _, tt := range []struct {
		ft        string
		dir, open bool
		name      string
	}{
		{"go", false, false, "fileTypes.go"},
		{"go", false, true, "fileTypes.go"},
		{"", false, false, IconFile},
		{"cobol", false, false, IconFile},
		{"", true, false, IconDirectory},
		{"go", true, false, IconDirectory},
		{"", true, true, IconDirectoryOpen},
	} {
		name, icon, ok := ic.Lookup(tt.ft, tt.dir, tt.open)
		if !ok || name != tt.name || icon != ic[tt.name] {
			t.Errorf("Lookup(%q, %v, %v) = %q %+v %v, want %q", tt.ft, tt.dir, tt.open, name, icon, ok, tt.name)
		}
	}
	if _, _, ok := (Icons{}).Lookup("go", false, false); ok {
		t.Error("空の表で ok")
	}

	over := ic.With(Icons{IconFileType("go"): {Text: "G"}, IconFileType("lua"): {Text: "L"}})
	if over[IconFileType("go")] != (Icon{Text: "G"}) || over[IconFileType("lua")].Text != "L" || over[IconFile].Text != "f" {
		t.Errorf("With = %+v", over)
	}
	if ic[IconFileType("go")].Text != "g" {
		t.Error("With が元の表を変えた")
	}

	// Width はアイコンの列の幅で、表のアイコンの表示幅の最大である。既定の表は、どのアイコンも 1 列である。
	if w := ic.Width(); w != 1 {
		t.Errorf("Width = %d", w)
	}
	if w := ic.With(Icons{IconFile: {Text: "あ"}}).Width(); w != 2 {
		t.Errorf("Width with a wide icon = %d", w)
	}
	if w, d := (Icons{}).Width(), DefaultIcons().Width(); w != 1 || d != 1 {
		t.Errorf("Width of an empty table = %d, of the default = %d", w, d)
	}

	th := DefaultTheme().WithIcons(ic)
	if th[IconStyle(IconFileType("go"))] != ic[IconFileType("go")].Attr || IsStyleName(IconStyle(IconFile)) {
		t.Errorf("WithIcons: %+v", th[IconStyle(IconFileType("go"))])
	}
	if _, ok := DefaultTheme()[IconStyle(IconFile)]; ok {
		t.Error("WithIcons が元の対応を変えた")
	}
}

// CheckIcon は、1 つの文字（後に続く結合文字を含む）で表示幅が 1 か 2 のものだけを通す。Nerd Font のグリフ（基本多言語面と
// 補助の私用領域の文字）は 1 列と数える（Vim の 'ambiwidth' の既定の single と同じ）。タブと制御文字は通さない。
//
// req: FR-152
func TestCheckIcon(t *testing.T) {
	for _, tt := range []struct {
		text string
		want string // 誤りの文の一部。空なら通す
	}{
		{"\U0000e627", ""},
		{"\U000f0219", ""},
		{"*", ""},
		{"あ", ""},
		{"e\U00000301", ""}, // 結合文字は前の文字と 1 つの文字
		{"", "1 つの文字にする（空である）"},
		{"ab", "1 つの文字にする"},
		{"あa", "1 つの文字にする"},
		// U+F0868 を JSON の \u の書き方で 5 桁の f0868 と書き損じると、U+F086 と "8" の 2 文字になる（PR #305 のレビューの推奨 3）。
		{"\U0000f086" + "8", "サロゲートの組か文字そのもの"},
		{"\t", "タブと制御文字"},
		{"\x01", "タブと制御文字"},
		{"\U0000200b", "タブと制御文字"},
	} {
		err := CheckIcon(tt.text)
		switch {
		case tt.want == "" && err != nil:
			t.Errorf("CheckIcon(%q) = %v", tt.text, err)
		case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
			t.Errorf("CheckIcon(%q) = %v, want %q", tt.text, err, tt.want)
		}
	}
	// サロゲートの組の案内は、\u の 4 桁で書いた私用領域の文字（の後に 16 進の数字が 1 つか 2 つ）に見えるときだけ付ける
	// （PR #305 の再レビューの推奨 1）。ASCII を並べただけのときと、私用領域の文字の後が 16 進の数字でないときは付けない。
	for text, hint := range map[string]bool{
		"\U0000f086" + "8": true, "\U0000f086" + "8a": true, "ab": false, "\U0000f086" + "x": false,
		"\U0000f086" + "123": false, "a8": false,
	} {
		err := CheckIcon(text)
		if err == nil || strings.Contains(err.Error(), "サロゲート") != hint {
			t.Errorf("CheckIcon(%q) = %v, want the surrogate hint %v", text, err, hint)
		}
	}
	for _, s := range []string{"\U0000e5ff", "\U0000e5fe", "\U0000f0f6", "\U000f031b", "\U000f0868"} {
		if w := StringWidth(s, DefaultTabStop); w != 1 {
			t.Errorf("StringWidth(%q) = %d, want 1", s, w)
		}
	}
}
