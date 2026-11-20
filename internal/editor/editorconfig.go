package editor

import (
	"strconv"

	"github.com/piplupflipper/vi-imitation/internal/editorconfig"
	"github.com/piplupflipper/vi-imitation/internal/option"
)

// .editorconfig（FR-164。基本設計 7.3）。
//
// バッファを開くとき（newBuffer）、ファイルから開き直すとき（:e。resetBufferOptions）、名前の無いバッファに名前を付けたとき
// （renameBufferOptions）に、バッファのファイルに当たる項目を読み（internal/editorconfig）、バッファの bufferState.editorConfig に覚える。
// 開いた後に .editorconfig を書き換えても、そのバッファには効かない。:e で開き直すと効く（Neovim 0.12.5 の plugin/editorconfig.lua が、
// BufNewFile、BufRead、BufFilePost でだけ読むのと同じ。ただし Neovim は :w ファイル で名前を付けたときは読まない。docs/vim-differences.md）。
// 読むのは indent_style、indent_size、tab_width の 3 つで、ほかの項目（end_of_line、charset など）は読まない（#318 の決め）。

// readEditorConfig は、パス path のファイルに当たる .editorconfig の項目を読む。
// 名前の無いバッファと、設定 editorConfig.enabled が false のときは読まずに nil を返す。
// 読めない .editorconfig があれば、ログに残し、読めたファイルの項目を返す。
//
// req: FR-164
func (e *Editor) readEditorConfig(path string) editorconfig.Properties {
	if path == "" || !e.options.Get(option.EditorConfigEnabled).Bool() {
		return nil
	}
	props, err := editorconfig.Lookup(path)
	if err != nil {
		e.log.Warn(".editorconfig を読めない", "path", path, "err", err)
	}
	return props
}

// editorConfigOptions は、.editorconfig の項目 props を、本体の設定の項目の値にする（初めの値の段の .editorconfig の段。applyOptionLayers）。
// 写し方は Neovim 0.12.5 の runtime/lua/editorconfig.lua に合わせた。
//
//   - indent_style = space は indent.useSpaces を true、tab は false にする。tab で indent_size が無ければ、indent.width を 0（tab.width と同じ）にする
//   - indent_size = <整数> は indent.width にし、tab_width が無ければ tab.width も同じ値にする。indent_size = tab は indent.width を 0 にする
//   - tab_width = <整数> は tab.width にする
//
// 値が unset の項目は、書かなかったものとして扱う。正しくない値（space でも tab でもない indent_style、整数でない幅、項目の範囲の外の幅）は、
// その項目だけを使わず、ログに残す（Neovim も、正しくない値の項目だけを当てずに知らせる）。
//
// req: FR-164
func (e *Editor) editorConfigOptions(props editorconfig.Properties) map[string]option.Value {
	get := func(name string) (string, bool) {
		v, ok := props[name]
		return v, ok && v != editorconfig.Unset
	}
	out := map[string]option.Value{}
	set := func(prop, name string, v option.Value) {
		d, _ := option.Lookup(name)
		if err := d.Check(v); err != nil {
			e.log.Warn(".editorconfig の値が正しくない", "property", prop, "value", v.String(), "err", err)
			return
		}
		out[name] = v
	}
	size, hasSize := get("indent_size")
	tab, hasTab := get("tab_width")
	if style, ok := get("indent_style"); ok {
		switch style {
		case "space":
			set("indent_style", option.IndentUseSpaces, option.BoolValue(true))
		case "tab":
			set("indent_style", option.IndentUseSpaces, option.BoolValue(false))
			if !hasSize {
				set("indent_style", option.IndentWidth, option.IntValue(0))
			}
		default:
			e.log.Warn(".editorconfig の値が正しくない", "property", "indent_style", "value", style)
		}
	}
	if hasSize {
		if size == "tab" {
			set("indent_size", option.IndentWidth, option.IntValue(0))
		} else if n, err := strconv.Atoi(size); err == nil {
			set("indent_size", option.IndentWidth, option.IntValue(n))
			if !hasTab {
				set("indent_size", option.TabWidth, option.IntValue(n))
			}
		} else {
			e.log.Warn(".editorconfig の値が正しくない", "property", "indent_size", "value", size)
		}
	}
	if hasTab {
		if n, err := strconv.Atoi(tab); err == nil {
			set("tab_width", option.TabWidth, option.IntValue(n))
		} else {
			e.log.Warn(".editorconfig の値が正しくない", "property", "tab_width", "value", tab)
		}
	}
	return out
}
