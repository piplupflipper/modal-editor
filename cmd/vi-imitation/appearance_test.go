package main

import (
	"os"
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/config"
	"github.com/piplupflipper/vi-imitation/internal/ui"
)

// 同梱する設定の例（config.example.jsonc）は、誤り無く読め、見た目の項目がすべて描画に渡る。
//
// req: FR-91, FR-92, FR-93
func TestConfigExample(t *testing.T) {
	data, err := os.ReadFile("../../config.example.jsonc")
	if err != nil {
		t.Fatal(err)
	}
	cfg, errs := config.Parse("config.example.jsonc", data)
	errs = append(errs, cfg.CheckAppearance(appearanceCheck)...)
	if len(errs) != 0 {
		t.Fatalf("設定の例の誤り: %v", errs)
	}
	colors := uiColors(cfg.Colors)
	if got, want := colors[ui.StyleKeyword], (ui.Attr{Fg: ui.RGB(0x84, 0xa0, 0xc6)}); got != want {
		t.Errorf("keyword = %+v, want %+v", got, want)
	}
	if len(colors) != len(cfg.Colors) || len(cfg.Symbols) == 0 || cfg.StatusLine == nil {
		t.Errorf("colors %d 件, symbols %v, statusline %+v", len(colors), cfg.Symbols, cfg.StatusLine)
	}
	if got := uiColor(config.Color{Kind: config.ColorIndexed, Index: 3}); got != ui.Indexed(3) {
		t.Errorf("uiColor(3) = %v", got)
	}
	for show, want := range map[string]ui.TabLineMode{"": ui.TabLineAlways, config.TabLineAlways: ui.TabLineAlways, config.TabLineMultiple: ui.TabLineMultiple} {
		if got := uiTabLine(show); got != want {
			t.Errorf("uiTabLine(%q) = %v, want %v", show, got, want)
		}
	}
}
