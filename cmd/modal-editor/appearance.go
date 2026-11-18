package main

import (
	"github.com/piplupflipper/modal-editor/internal/config"
	"github.com/piplupflipper/modal-editor/internal/ui"
)

// appearanceCheck は、設定の見た目の項目（colors、symbols、statusline）を、描画（internal/ui）が知る名前と規則で確かめる関数の組である。
// internal/config は同じ層の internal/ui を import しないので、ここでつなぐ（基本設計 2 章）。
var appearanceCheck = config.AppearanceCheck{
	Color:      ui.IsStyleName,
	Symbol:     ui.CheckSymbol,
	StatusPart: ui.IsStatusPart,
}

// uiColors は、設定の colors を描画の属性の対応に写す（FR-91）。
func uiColors(colors map[string]config.Style) map[string]ui.Attr {
	if len(colors) == 0 {
		return nil
	}
	out := make(map[string]ui.Attr, len(colors))
	for name, s := range colors {
		out[name] = ui.Attr{
			Fg: uiColor(s.Fg), Bg: uiColor(s.Bg), Sp: uiColor(s.Sp),
			Bold: s.Bold, Italic: s.Italic, Underline: s.Underline, Undercurl: s.Undercurl, Reverse: s.Reverse,
		}
	}
	return out
}

// uiColor は、設定の 1 つの色を描画の色に写す。書いていない色は端末の既定の色である。
func uiColor(c config.Color) ui.Color {
	switch c.Kind {
	case config.ColorIndexed:
		return ui.Indexed(c.Index)
	case config.ColorRGB:
		return ui.RGB(c.R, c.G, c.B)
	case config.ColorUnset:
	}
	return ui.ColorDefault
}

// uiStatusLine は、設定の statusline を描画の並びに写す（FR-92）。nil なら nil（既定の並び）を返す。
func uiStatusLine(s *config.StatusLine) *ui.StatusLine {
	if s == nil {
		return nil
	}
	return &ui.StatusLine{Left: s.Left, Right: s.Right}
}
