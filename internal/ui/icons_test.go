package ui

import (
	"strings"
	"testing"
)

// 既定のアイコンの表は、ディレクトリ（開いたものと閉じたもの）と種類の分からないファイルのアイコンを持ち、
// どのアイコンも CheckIcon を通る（表示幅が 1 か 2）。名前はどれもアイコンの名前の形で、色は文字の色を持つ。
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
// With は名前ごとに置き換え（既定の文字と色を混ぜない）、元の表を変えない。WithIcons は、アイコンの色を IconStyle の名前で足す。
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

	th := DefaultTheme().WithIcons(ic)
	if th[IconStyle(IconFileType("go"))] != ic[IconFileType("go")].Attr || IsStyleName(IconStyle(IconFile)) {
		t.Errorf("WithIcons: %+v", th[IconStyle(IconFileType("go"))])
	}
	if _, ok := DefaultTheme()[IconStyle(IconFile)]; ok {
		t.Error("WithIcons が元の対応を変えた")
	}
}

// CheckIcon は、表示幅が 1 か 2 の文字列だけを通す。Nerd Font のグリフ（基本多言語面と補助の私用領域の文字）は 1 列と数える
// （Vim の 'ambiwidth' の既定の single と同じ）。タブと制御文字は通さない。
//
// req: FR-152
func TestCheckIcon(t *testing.T) {
	for _, tt := range []struct {
		text string
		want string // 誤りの文の一部。空なら通す
	}{
		{"", ""},
		{"\U000f0219", ""},
		{"*", ""},
		{"ab", ""},
		{"あ", ""},
		{"", "表示幅は 1 か 2"},
		{"abc", "表示幅は 1 か 2"},
		{"あa", "表示幅は 1 か 2"},
		{"\t", "タブと制御文字"},
		{"a\x01", "タブと制御文字"},
	} {
		err := CheckIcon(tt.text)
		switch {
		case tt.want == "" && err != nil:
			t.Errorf("CheckIcon(%q) = %v", tt.text, err)
		case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
			t.Errorf("CheckIcon(%q) = %v, want %q", tt.text, err, tt.want)
		}
	}
	for _, s := range []string{"", "", "", "\U000f031b", "\U000f0868"} {
		if w := StringWidth(s, DefaultTabStop); w != 1 {
			t.Errorf("StringWidth(%q) = %d, want 1", s, w)
		}
	}
}
