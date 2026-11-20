package editor

import (
	"fmt"
	"slices"
	"sort"

	"github.com/piplupflipper/vi-imitation/internal/command"
	"github.com/piplupflipper/vi-imitation/internal/command/ex"
	"github.com/piplupflipper/vi-imitation/internal/config"
	"github.com/piplupflipper/vi-imitation/internal/editcmd"
	"github.com/piplupflipper/vi-imitation/internal/exthost"
	"github.com/piplupflipper/vi-imitation/internal/input"
)

// actionModes は、設定の keymap のモードの名前のうち、右辺に操作の ID（FR-126）と Ex コマンド（FR-154）を書けるものと、
// その操作の表のモードである。commandLine は、コマンドラインの入力が操作の表を通らないので含めない。
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

// KeymapCommandCheck は、設定の keymap の右辺に Ex コマンドを書いた 1 件（FR-154）を使えるかを確かめる関数を返す。
// 使えなければ理由を返す。leader は設定の leader、specs は Config.Extensions に渡す拡張機能の定義、
// aliases は Config.CommandAliases に渡す Ex コマンドの別名の表（設定の commandAliases を足した後のもの）である。
//
// 呼び出し側（cmd/vi-imitation）は、宣言ファイルを読み、別名を足した後に、誤りに位置を付けるために
// config.Config.CheckKeymapCommands へ渡す。確かめるのは 1 件ずつの規則（コマンドの名前、モード、左辺）で、
// 項目どうしの重なりは applyKeymapActions が確かめる。
//
// req: FR-154
func KeymapCommandCheck(leader string, specs []exthost.Spec, aliases *ex.Aliases) func(mode, lhs string, c config.KeymapCommand) error {
	keys, _ := leaderKeys(leader) // leader の誤りは、エディタが起動のときに知らせる
	ids := keymapExtCommands(specs)
	return func(mode, lhs string, c config.KeymapCommand) error {
		_, err := keymapCommand(mode, lhs, c, keys, aliases, ids)
		return err
	}
}

// keymapExtCommands は、設定の keymap の右辺の Ex コマンドに書ける拡張機能のコマンドの ID（宣言ファイルの contributes.commands と
// provides の操作の名前。buildExCommands）を返す（FR-154）。本体が拡張機能のコマンドと同じ口から呼ぶ :help、:keys、:ext、:messages は、
// 正規の名前の無い本体のコマンドなので含めない。
func keymapExtCommands(specs []exthost.Spec) []string {
	cmds, _ := buildExCommands(specs) // 重なりの誤りは New が知らせる
	ids := make([]string, 0, len(cmds))
	for id := range cmds {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// keymapModeKeys は、設定の keymap のモードの名前 mode と左辺の表記 lhs を、操作の表のモードとキーの列にする。
// 操作と Ex コマンドを書けないモードと、読めない左辺は誤りにする。
func keymapModeKeys(mode, lhs string, leader []editcmd.Key) (input.MapMode, []editcmd.Key, error) {
	mm, ok := actionModes[mode]
	if !ok {
		if _, known := mapModeNames[mode]; known {
			return 0, nil, fmt.Errorf("%s には操作もコマンドも割り当てられない（キーの列を書く）", mode)
		}
		return 0, nil, fmt.Errorf("未知のモード %q", mode)
	}
	keys, err := editcmd.ParseKeysLeader(lhs, leader)
	if err != nil {
		return 0, nil, fmt.Errorf("キーの列 %q を読めない: %w", lhs, err)
	}
	if len(keys) == 0 {
		return 0, nil, fmt.Errorf("キーの列が空です")
	}
	return mm, keys, nil
}

// keymapAction は、設定の keymap の右辺に操作の ID を書いた 1 件を、操作の表の登録にする。
func keymapAction(mode, lhs, id string, leader []editcmd.Key) (input.Binding, error) {
	mm, keys, err := keymapModeKeys(mode, lhs, leader)
	if err != nil {
		return input.Binding{}, err
	}
	return input.UserBinding(mm, keys, id)
}

// keymapCommand は、設定の keymap の右辺に Ex コマンドを書いた 1 件 c を、操作の表の登録にする（FR-154）。
// 登録は、Ref.Extension を command.ExCommandExtension、Ref.ID を実行するコマンドライン（ex.KeymapCommandLine）にした操作で、
// キーで呼ばれると internal/command の State がコマンドラインで打ったときと同じ口で実行する。
// 書ける名前は ex.KeymapCommandLine が、左辺の規則（インサートモードでは 1 つのキーにするなど）は input.Keymap.Bind が確かめる。
// extCommands は keymapExtCommands の値である。
func keymapCommand(mode, lhs string, c config.KeymapCommand, leader []editcmd.Key, aliases *ex.Aliases, extCommands []string) (input.Binding, error) {
	mm, keys, err := keymapModeKeys(mode, lhs, leader)
	if err != nil {
		return input.Binding{}, err
	}
	line, err := ex.KeymapCommandLine(aliases, c.Name, c.Args, extCommands)
	if err != nil {
		return input.Binding{}, err
	}
	b := input.Binding{
		Kind: input.BindAction, Modes: mm, Keys: keys, Ref: editcmd.Ref{ID: line, Extension: command.ExCommandExtension},
		Description: ":" + line, User: true,
	}
	if err := input.NewKeymap().Bind(b); err != nil {
		return input.Binding{}, err
	}
	return b, nil
}

// userKeymap は、設定の keymap のうち、操作の表に登録する項目（右辺がオブジェクトの項目）である（applyKeymapActions）。
type userKeymap struct {
	// actions は、右辺に操作の ID を書いた項目（Config.KeymapActions。FR-126）である。
	actions map[string]map[string]string
	// commands は、右辺に Ex コマンドを書いた項目（Config.KeymapCommands。FR-154）である。
	commands map[string]map[string]config.KeymapCommand
	// aliases と extCommands は、commands に書ける名前を決める Ex コマンドの別名の表と拡張機能のコマンドの ID である（keymapCommand）。
	aliases     *ex.Aliases
	extCommands []string
}

// applyKeymapActions は、設定の keymap の右辺に操作の ID（Config.KeymapActions。FR-126）と Ex コマンド（Config.KeymapCommands。FR-154）を
// 書いた割り当てを、操作の表 km に登録する。
// 同じモードの同じキーの列に本体の既定の登録（と拡張機能の登録）があれば、取り除いてから登録する（input.Keymap.Override）。
// 使えない項目は捨て、誤りを返す。
//
// 左辺の <Leader> は leader に置き換える。置き換えた後のキーの列が同じになる項目は、右辺をキーの列で書いた項目（remap）も含めて、
// 1 つだけを使う。キーの列の項目と重なれば、キーの列の項目を使う（展開の層が先にキーを受け取るので、操作の項目は効かないため）。
// 操作と Ex コマンドの項目どうしで重なれば、左辺の表記をバイトの順に並べて最初の項目を使う（newRemapper と同じ規則）。
//
// req: FR-126, FR-154
func applyKeymapActions(km *input.Keymap, cfg userKeymap, leader []editcmd.Key, remap *remapper) []error {
	var errs []error
	modeSet := map[string]bool{}
	for m := range cfg.actions {
		modeSet[m] = true
	}
	for m := range cfg.commands {
		modeSet[m] = true
	}
	modes := make([]string, 0, len(modeSet))
	for m := range modeSet {
		modes = append(modes, m)
	}
	sort.Strings(modes)
	for _, mode := range modes {
		lhsTexts := make([]string, 0, len(cfg.actions[mode])+len(cfg.commands[mode]))
		for t := range cfg.actions[mode] {
			lhsTexts = append(lhsTexts, t)
		}
		for t := range cfg.commands[mode] {
			if _, dup := cfg.actions[mode][t]; !dup {
				lhsTexts = append(lhsTexts, t)
			}
		}
		sort.Strings(lhsTexts)
		used := map[string]string{}
		for _, lhs := range lhsTexts {
			var b input.Binding
			var err error
			if id, ok := cfg.actions[mode][lhs]; ok {
				b, err = keymapAction(mode, lhs, id, leader)
			} else {
				b, err = keymapCommand(mode, lhs, cfg.commands[mode][lhs], leader, cfg.aliases, cfg.extCommands)
			}
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
