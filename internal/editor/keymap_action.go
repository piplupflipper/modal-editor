package editor

import (
	"fmt"
	"slices"
	"sort"

	"github.com/piplupflipper/vi-imitation/internal/editcmd"
	"github.com/piplupflipper/vi-imitation/internal/input"
)

// actionModes は、設定の keymap のモードの名前のうち、右辺に操作の ID を書けるものと、その操作の表のモードである（FR-126）。
// commandLine は、コマンドラインの入力が操作の表を通らないので含めない。
var actionModes = map[string]input.MapMode{
	"normal": input.MapNormal,
	"visual": input.MapVisual,
	"insert": input.MapInsert,
}

// KeymapActionCheck は、設定の keymap の右辺に操作の ID を書いた 1 件（モードの名前 mode、左辺の表記 lhs、操作の ID id）を
// 使えるかを確かめる関数を返す。使えなければ理由を返す。leader は設定の leader（Config.Leader）で、読めなければ既定の leader を使う。
//
// 呼び出し側（cmd/vi-imitation）は、設定ファイルを読んだ後に、誤りに位置を付けるために config.Config.CheckKeymapActions へ渡す。
// 確かめるのは 1 件ずつの規則（操作の ID、モード、左辺。input.UserBinding）で、項目どうしの重なりは applyKeymapActions が確かめる。
//
// req: FR-126
func KeymapActionCheck(leader string) func(mode, lhs, id string) error {
	keys, _ := leaderKeys(leader) // leader の誤りは、エディタが起動のときに知らせる
	return func(mode, lhs, id string) error {
		_, err := keymapAction(mode, lhs, id, keys)
		return err
	}
}

// keymapAction は、設定の keymap の 1 件を、操作の表の登録にする。
func keymapAction(mode, lhs, id string, leader []editcmd.Key) (input.Binding, error) {
	mm, ok := actionModes[mode]
	if !ok {
		if _, known := mapModeNames[mode]; known {
			return input.Binding{}, fmt.Errorf("%s には操作を割り当てられない（キーの列を書く）", mode)
		}
		return input.Binding{}, fmt.Errorf("未知のモード %q", mode)
	}
	keys, err := editcmd.ParseKeysLeader(lhs, leader)
	if err != nil {
		return input.Binding{}, fmt.Errorf("キーの列 %q を読めない: %w", lhs, err)
	}
	if len(keys) == 0 {
		return input.Binding{}, fmt.Errorf("キーの列が空です")
	}
	return input.UserBinding(mm, keys, id)
}

// applyKeymapActions は、設定の keymap の右辺に操作の ID を書いた割り当て（Config.KeymapActions）を、操作の表 km に登録する（FR-126）。
// 同じモードの同じキーの列に本体の既定の登録（と拡張機能の登録）があれば、取り除いてから登録する（input.Keymap.Override）。
// 使えない項目は捨て、誤りを返す。
//
// 左辺の <Leader> は leader に置き換える。置き換えた後のキーの列が同じになる項目は、右辺をキーの列で書いた項目（remap）も含めて、
// 1 つだけを使う。キーの列の項目と重なれば、キーの列の項目を使う（展開の層が先にキーを受け取るので、操作の項目は効かないため）。
// 操作の項目どうしで重なれば、左辺の表記をバイトの順に並べて最初の項目を使う（newRemapper と同じ規則）。
//
// req: FR-126
func applyKeymapActions(km *input.Keymap, cfg map[string]map[string]string, leader []editcmd.Key, remap *remapper) []error {
	var errs []error
	modes := make([]string, 0, len(cfg))
	for m := range cfg {
		modes = append(modes, m)
	}
	sort.Strings(modes)
	for _, mode := range modes {
		lhsTexts := make([]string, 0, len(cfg[mode]))
		for t := range cfg[mode] {
			lhsTexts = append(lhsTexts, t)
		}
		sort.Strings(lhsTexts)
		used := map[string]string{}
		for _, lhs := range lhsTexts {
			id := cfg[mode][lhs]
			b, err := keymapAction(mode, lhs, id, leader)
			if err != nil {
				errs = append(errs, fmt.Errorf("keymap.%s.%s: %w", mode, lhs, err))
				continue
			}
			key := editcmd.KeysString(b.Keys)
			if remap != nil && slices.ContainsFunc(remap.maps[mapModeNames[mode]], func(m mapping) bool { return slices.Equal(m.lhs, b.Keys) }) {
				errs = append(errs, fmt.Errorf("keymap.%s: %q はキーの列を書いた項目と同じキーの列 %s になるので、キーの列の項目を使う", mode, lhs, key))
				continue
			}
			if first, ok := used[key]; ok {
				errs = append(errs, fmt.Errorf("keymap.%s: %q は %q と同じキーの列 %s になるので、%q を使う", mode, lhs, first, key, first))
				continue
			}
			used[key] = lhs
			if err := km.Override(b); err != nil {
				errs = append(errs, fmt.Errorf("keymap.%s.%s: %w", mode, lhs, err))
			}
		}
	}
	return errs
}
