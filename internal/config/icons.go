package config

import "github.com/piplupflipper/vi-imitation/internal/option"

// 設定の icons（FR-152。基本設計 7.1）。
//
//	"icons": {
//	  "show": true,
//	  "directory": { "text": "D", "fg": "#73cef4" },
//	  "directoryOpen": { "text": "O", "fg": "#73cef4" },
//	  "file": { "text": "F", "fg": "#6d8086" },
//	  "fileTypes": { "go": { "text": "G", "fg": "#00add8", "bold": true } }
//	}
//
// text はふつう Nerd Font のグリフ（既定の表は internal/ui の icons_default.go）を、文字そのものか JSON の \u の書き方で書く。
// show は設定の表（internal/option）の項目 icons.show で、ほかの項目はアイコンの表を名前ごとに置き換える。
// 設定の表の項目の最上位の段は Parse のほかの項目と重ねない決まり（基本設計 7.3）だが、icons だけは、
// 出すかどうかと表の項目を同じ所に書けるように、表の項目（show）とアイコンの表の項目を同じ段に置く。

// Icon は、icons の 1 つのアイコンである（FR-152）。
type Icon struct {
	// Text は、描く文字列である。表示幅の規則は CheckAppearance で確かめる（AppearanceCheck.Icon）。
	Text string
	// Style は、Text の色と飾りで、colors の属性と同じ書き方（fg、bg、sp、bold など）である。
	Style Style
}

// アイコンの名前のうち、ファイルの種類によらないもの。internal/ui の IconDirectory などと同じ値にする。
var iconNames = map[string]bool{"directory": true, "directoryOpen": true, "file": true}

// iconFileTypePrefix は、ファイルの種類のアイコンの名前の接頭辞である。internal/ui の IconFileTypePrefix と同じ値にする。
const iconFileTypePrefix = "fileTypes."

// icons は、icons の値を読む（FR-152）。show は設定の表の項目 icons.show として Options に入れ、
// directory、directoryOpen、file と、fileTypes の下のファイルの種類ごとのアイコンを Icons に入れる。
// Icons の名前は icons の下の位置（"directory"、"fileTypes.go"）である。誤りのあるアイコンは入れない。
//
// req: FR-152
func (s *source) icons(m member, cfg *Config) {
	obj, ok := s.object(m.value, "icons")
	if !ok {
		return
	}
	for _, e := range s.members(obj, "icons") {
		switch {
		case iconNames[e.name]:
			s.icon(e, "icons", e.name, cfg)
		case e.name == "fileTypes":
			fts, ok := s.object(e.value, "icons.fileTypes")
			if !ok {
				continue
			}
			for _, f := range s.members(fts, "icons.fileTypes") {
				if f.name == "" {
					s.report(f.nameOff, "icons.fileTypes のファイルの種類の名前が空である")
					continue
				}
				s.icon(f, "icons.fileTypes", iconFileTypePrefix+f.name, cfg)
			}
		default:
			if !s.option(e, "icons", option.Defs(), cfg) {
				s.report(e.nameOff, "未知の項目 %s を無視する", join("icons", e.name))
			}
		}
	}
}

// icon は、アイコン 1 つ（位置は path の下の m）を読み、Icons の name に入れる。
// 値は text（文字列。必ず書く）と、colors と同じ属性の項目を持つオブジェクトである。
// 既定のアイコンとは混ぜずに置き換えるので（colors と同じ）、text を省いたアイコンは誤りにして使わない。
func (s *source) icon(m member, path, name string, cfg *Config) {
	p := join(path, m.name)
	obj, ok := s.object(m.value, p)
	if !ok {
		return
	}
	var ic Icon
	hasText := false
	ok = true
	var textPos *Error
	for _, f := range s.members(obj, p) {
		if f.name == "text" {
			t, tok := s.str(f.value, join(p, "text"))
			if !tok {
				ok = false
				continue
			}
			ic.Text, hasText, textPos = t, true, s.at(f.value.StartOffset, "")
			continue
		}
		known, fok := s.styleField(f, p, &ic.Style)
		if !known {
			s.report(f.nameOff, "未知の項目 %s を無視する", join(p, f.name))
			continue
		}
		ok = ok && fok
	}
	if !ok {
		return
	}
	if !hasText {
		s.report(m.nameOff, "%s に text が無い（既定のアイコンとは混ぜずに置き換えるので、text を書く）", p)
		return
	}
	if cfg.Icons == nil {
		cfg.Icons = map[string]Icon{}
	}
	cfg.Icons[name] = ic
	if cfg.pos.icons == nil {
		cfg.pos.icons = map[string]*Error{}
	}
	cfg.pos.icons[name] = textPos
}
