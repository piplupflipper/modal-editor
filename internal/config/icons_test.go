package config

import (
	"errors"
	"reflect"
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/option"
)

// icons の show は設定の表の項目 icons.show として Options に入り、directory、directoryOpen、file と fileTypes の下の種類ごとの
// アイコンは、icons の下の位置を名前にして Icons に入る。アイコンは text と colors と同じ属性の項目を持ち、
// text の無いもの、知らない項目、読めない値、空の種類の名前は誤りにして、そのアイコンだけを使わない。
//
// req: FR-152
func TestParseIcons(t *testing.T) {
	src := `{
  "icons": {
    "show": false,
    "directory": { "text": "D", "fg": "#73cef4", "bold": true },
    "file": { "text": "F" },
    "fileTypes": {
      "go": { "text": "G", "fg": 38 },
      "lua": { "fg": "#51a0cf" },
      "bad": { "text": 1 },
      "": { "text": "E" },
      "odd": { "text": "O", "blink": true }
    },
    "folder": { "text": "x" }
  }
}`
	cfg, errs := Parse("config.jsonc", []byte(src))
	if v, ok := cfg.Options[option.IconsShow]; !ok || v != option.BoolValue(false) {
		t.Errorf("Options[icons.show] = %v, %v", v, ok)
	}
	want := map[string]Icon{
		"directory":     {Text: "D", Style: Style{Fg: Color{Kind: ColorRGB, R: 0x73, G: 0xce, B: 0xf4}, Bold: true}},
		"file":          {Text: "F"},
		"fileTypes.go":  {Text: "G", Style: Style{Fg: Color{Kind: ColorIndexed, Index: 38}}},
		"fileTypes.odd": {Text: "O"},
	}
	if !reflect.DeepEqual(cfg.Icons, want) {
		t.Errorf("Icons = %+v, want %+v", cfg.Icons, want)
	}
	wantErrors(t, errs, []struct {
		line, col int
		msg       string
	}{
		{8, 7, "icons.fileTypes.lua に text が無い"},
		{9, 24, "icons.fileTypes.bad.text は文字列"},
		{10, 7, "ファイルの種類の名前が空"},
		{11, 29, "未知の項目 icons.fileTypes.odd.blink"},
		{13, 5, "未知の項目 icons.folder"},
	})

	// icons がオブジェクトでなければ誤りにする。
	if _, errs := Parse("config.jsonc", []byte(`{"icons": true}`)); len(errs) != 1 {
		t.Errorf("icons: true: %v", errs)
	}
}

// CheckAppearance は、icons のアイコンの文字列を AppearanceCheck.Icon で確かめ、使えないアイコンを text の位置の誤りにして除く。
//
// req: FR-152
func TestCheckAppearanceIcons(t *testing.T) {
	src := `{
  "icons": { "file": { "text": "F" }, "fileTypes": { "go": { "text": "toolong" } } }
}`
	cfg, errs := Parse("config.jsonc", []byte(src))
	if len(errs) != 0 {
		t.Fatalf("Parse の誤り: %v", errs)
	}
	errs = cfg.CheckAppearance(AppearanceCheck{Icon: func(text string) error {
		if len(text) > 2 {
			return errors.New("幅が広い")
		}
		return nil
	}})
	if _, ok := cfg.Icons["fileTypes.go"]; ok || len(cfg.Icons) != 1 {
		t.Errorf("Icons = %+v", cfg.Icons)
	}
	wantErrors(t, errs, []struct {
		line, col int
		msg       string
	}{
		{2, 70, "icons の fileTypes.go の text を使えない: 幅が広い"},
	})
}
