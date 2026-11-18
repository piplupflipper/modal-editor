package config

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// colors は、色の名前から属性への対応として読む。色は "#rrggbb" か 0〜255 の整数で、誤りのある名前は除く。
//
// req: FR-91
func TestParseColors(t *testing.T) {
	src := `{
  "colors": {
    "keyword": { "fg": "#84A0c6", "bold": true },
    "statusline": { "fg": 7, "bg": 0, "reverse": false },
    "diagnostic.error": { "sp": "#e27878", "undercurl": true, "italic": true, "underline": false },
    "bad.color": { "fg": "#12345" },
    "bad.index": { "bg": 256 },
    "bad.flag": { "bold": "yes" },
    "unknown.attr": { "fg": 1, "blink": true },
    "not.object": "#ffffff"
  }
}`
	cfg, errs := Parse("config.jsonc", []byte(src))
	want := map[string]Style{
		"keyword":          {Fg: Color{Kind: ColorRGB, R: 0x84, G: 0xa0, B: 0xc6}, Bold: true},
		"statusline":       {Fg: Color{Kind: ColorIndexed, Index: 7}, Bg: Color{Kind: ColorIndexed, Index: 0}},
		"diagnostic.error": {Sp: Color{Kind: ColorRGB, R: 0xe2, G: 0x78, B: 0x78}, Undercurl: true, Italic: true},
		"unknown.attr":     {Fg: Color{Kind: ColorIndexed, Index: 1}},
	}
	if !reflect.DeepEqual(cfg.Colors, want) {
		t.Errorf("Colors = %+v, want %+v", cfg.Colors, want)
	}
	wantErrors(t, errs, []struct {
		line, col int
		msg       string
	}{
		{6, 26, `"#rrggbb"`},
		{7, 26, "255 以下"},
		{8, 27, "true か false"},
		{9, 32, `未知の項目 colors["unknown.attr"].blink`},
		{10, 19, "オブジェクト"},
	})
}

// symbols と statusline を読み、CheckAppearance で、描画が知らない名前と使えない値を位置付きの誤りにして除く。
//
// req: FR-91, FR-92, FR-93
func TestCheckAppearance(t *testing.T) {
	src := `{
  "colors": { "keyword": { "fg": 1 }, "keywrod": { "fg": 2 } },
  "symbols": { "readonly": "RO", "sign.breakpoint": "abc" },
  "statusline": { "left": ["mode", "zzzz", "readonly"], "right": ["ruler", "zzzz"] }
}`
	cfg, errs := Parse("config.jsonc", []byte(src))
	if len(errs) != 0 {
		t.Fatalf("Parse の誤り: %v", errs)
	}
	check := AppearanceCheck{
		Color: func(n string) bool { return n == "keyword" },
		Symbol: func(_, v string) error {
			if len(v) > 2 {
				return errors.New("too wide")
			}
			return nil
		},
		StatusPart: func(id string) bool { return id != "zzzz" },
	}
	errs = cfg.CheckAppearance(check)
	wantErrors(t, errs, []struct {
		line, col int
		msg       string
	}{
		{2, 39, "colors の keywrod は未知の色の名前"},
		{3, 34, "symbols の sign.breakpoint を使えない: too wide"},
		{4, 36, "statusline の部品 zzzz は未知"},
	})
	if _, ok := cfg.Colors["keywrod"]; ok || len(cfg.Colors) != 1 {
		t.Errorf("Colors = %v", cfg.Colors)
	}
	if want := map[string]string{"readonly": "RO"}; !reflect.DeepEqual(cfg.Symbols, want) {
		t.Errorf("Symbols = %v, want %v", cfg.Symbols, want)
	}
	if want := (&StatusLine{Left: []string{"mode", "readonly"}, Right: []string{"ruler"}}); !reflect.DeepEqual(cfg.StatusLine, want) {
		t.Errorf("StatusLine = %+v, want %+v", cfg.StatusLine, want)
	}
}

// statusline は left と right を持つオブジェクトで、書かなかった側は空の列（部品を出さない）にする。
// 書かなかったときは nil（本体の既定の並び）である。
//
// req: FR-92
func TestParseStatusLine(t *testing.T) {
	cfg, errs := Parse("config.jsonc", []byte(`{"statusline": {"left": ["mode", 3], "center": []}}`))
	if want := (&StatusLine{Left: []string{"mode"}, Right: []string{}}); !reflect.DeepEqual(cfg.StatusLine, want) {
		t.Errorf("StatusLine = %+v, want %+v", cfg.StatusLine, want)
	}
	if len(errs) != 2 || !strings.Contains(errs[0].Error(), "文字列にする") || !strings.Contains(errs[1].Error(), "未知の項目 statusline.center") {
		t.Errorf("誤り = %v", errs)
	}
	cfg, _ = Parse("config.jsonc", []byte(`{}`))
	if cfg.StatusLine != nil {
		t.Errorf("StatusLine = %+v, want nil", cfg.StatusLine)
	}
}
