package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/piplupflipper/vi-imitation/internal/command/ex"
	"github.com/piplupflipper/vi-imitation/internal/config"
	"github.com/piplupflipper/vi-imitation/internal/editcmd"
	"github.com/piplupflipper/vi-imitation/internal/editor"
	"github.com/piplupflipper/vi-imitation/internal/input"
	"github.com/piplupflipper/vi-imitation/internal/option"
)

// cell は、s を Markdown の表のセルに書ける形（| を \| にする）にする。
func cell(s string) string {
	return strings.ReplaceAll(s, "|", `\|`)
}

// code は、s をコードの区画（`s`）にする。s が ` を含めば “ で囲む。
func code(s string) string {
	if strings.Contains(s, "`") {
		return "`` " + s + " ``"
	}
	return "`" + s + "`"
}

// writeOptions は、設定の表（option.Defs）を、名前、型、範囲、既定値、取れる値の表にする。
func writeOptions(w *bytes.Buffer) {
	types := map[option.Type]string{option.Bool: "真偽", option.Int: "整数", option.String: "文字列"}
	scopes := map[option.Scope]string{option.ScopeEditor: "エディタ全体", option.ScopeBuffer: "バッファごと"}
	fmt.Fprintln(w, "| 名前 | 型 | 範囲 | 既定値 | 取れる値と注意 |")
	fmt.Fprintln(w, "|---|---|---|---|---|")
	for _, d := range option.Defs() {
		var note []string
		if d.Range != nil {
			note = append(note, fmt.Sprintf("%d から %d", d.Range.Min, d.Range.Max))
		}
		if d.Detected {
			note = append(note, "設定ファイルに書けない（開いたファイルから判定する）")
		}
		fmt.Fprintf(w, "| %s | %s | %s | %s | %s |\n", code(d.Name), types[d.Type], scopes[d.Scope], code(jsonValue(d.Default)), cell(strings.Join(note, "。")))
	}
}

// jsonValue は、設定の値を、設定ファイルに書く JSON の形にする。
func jsonValue(v option.Value) string {
	if v.Type() == option.String {
		b, _ := json.Marshal(v.String()) // 文字列の JSON 化は失敗しない
		return string(b)
	}
	return v.String()
}

// writeConfigKeys は、設定ファイルの最上位の項目のうち、設定の表の項目でないもの（config.TopLevelNames）を並べる。
func writeConfigKeys(w *bytes.Buffer) {
	names := config.TopLevelNames()
	for i, n := range names {
		names[i] = code(n)
	}
	fmt.Fprintln(w, strings.Join(names, "、"))
}

// leaderProbe は、既定のキーのうち <Leader> で始まるものを見分けるために、既定の leader の代わりに置くキーである。
// 本体の既定のキーで使っていないキーにする（使っていると DefaultKeymapLeader が誤りを返し、writeActions が失敗する）。
const leaderProbe = "<C-g>"

// writeActions は、操作の ID の一覧（input.Actions）を、種類、モード、既定のキー、説明の表にする。
// 既定のキーは、本体の既定のキーマップ（input.DefaultKeymapLeader）から、その ID の本体の操作を引いたものである。
// <Leader> で始まるキーは <Leader> と書く。既定の leader と leaderProbe で作った 2 つのキーマップの、同じ位置の登録のキーが
// 違えば、違う所が leader である（2 つのキーマップは同じ順に登録する）。
func writeActions(w *bytes.Buffer) error {
	def, errs := input.DefaultKeymapLeader(nil)
	if len(errs) > 0 {
		return fmt.Errorf("既定のキーマップを作れない: %v", errs)
	}
	probe, errs := input.DefaultKeymapLeader(editcmd.MustParseKeys(leaderProbe))
	if len(errs) > 0 {
		return fmt.Errorf("%s を leader にした既定のキーマップを作れない: %v", leaderProbe, errs)
	}
	a, b := def.Bindings(), probe.Bindings()
	if len(a) != len(b) {
		return fmt.Errorf("leader を変えた既定のキーマップの登録の数が違う（%d と %d）", len(a), len(b))
	}
	leader, probeKeys := editcmd.MustParseKeys(editcmd.DefaultLeader), editcmd.MustParseKeys(leaderProbe)
	keys := map[string][]string{} // ID と種類から、既定のキーの表記へ
	for i, bd := range a {
		if !bd.Ref.Builtin() && !bd.Ref.Provider() {
			continue
		}
		label := editcmd.KeysString(bd.Keys)
		if !slices.Equal(bd.Keys, b[i].Keys) {
			// leader は先頭にだけ置く前提で書く。そうでない既定のキーを足したら、ここで気づけるように誤りにする
			rest, ok := cutKeys(b[i].Keys, probeKeys)
			if r2, ok2 := cutKeys(bd.Keys, leader); !ok || !ok2 || !slices.Equal(rest, r2) {
				return fmt.Errorf("既定のキー %s の <Leader> が先頭に無い", label)
			}
			label = "<Leader>" + editcmd.KeysString(rest)
		}
		k := actionKey(bd.Ref.ID, bd.Kind)
		if !slices.Contains(keys[k], label) {
			keys[k] = append(keys[k], label)
		}
	}
	kinds := map[input.BindingKind]string{input.BindMotion: "モーション", input.BindOperator: "オペレーター", input.BindTextObject: "テキストオブジェクト", input.BindAction: "操作"}
	fmt.Fprintln(w, "| ID | 種類 | モード | 既定のキー | 説明 |")
	fmt.Fprintln(w, "|---|---|---|---|---|")
	for _, act := range input.Actions() {
		var modes []string
		for _, m := range []input.MapMode{input.MapNormal, input.MapVisual, input.MapInsert} {
			if act.Modes&m != 0 {
				modes = append(modes, input.ModeName(m))
			}
		}
		var ks []string
		for _, k := range keys[actionKey(act.ID, act.Kind)] {
			ks = append(ks, code(k))
		}
		fmt.Fprintf(w, "| %s | %s | %s | %s | %s |\n", code(act.ID), kinds[act.Kind], strings.Join(modes, "、"), cell(strings.Join(ks, " ")), cell(act.Description))
	}
	return nil
}

// cutKeys は、keys が prefix で始まれば、その後ろを返す。
func cutKeys(keys, prefix []editcmd.Key) ([]editcmd.Key, bool) {
	if len(keys) < len(prefix) || !slices.Equal(keys[:len(prefix)], prefix) {
		return nil, false
	}
	return keys[len(prefix):], true
}

// actionKey は、操作の ID と種類の組を 1 つの文字列にする（同じ ID の別の種類の操作を分けるため）。
func actionKey(id string, kind input.BindingKind) string {
	return fmt.Sprintf("%s\x00%d", id, kind)
}

// writeExCommands は、本体の Ex コマンドの表と別名の表（ex.Names）を、名前と最短の省略形の表にする。
func writeExCommands(w *bytes.Buffer) {
	fmt.Fprintln(w, "| コマンド | 最短の省略形 |")
	fmt.Fprintln(w, "|---|---|")
	for _, n := range ex.Names(nil) {
		short := code(":" + n.Short)
		if n.Short == n.Full {
			short = "省略できない"
		}
		fmt.Fprintf(w, "| %s | %s |\n", code(":"+n.Full), short)
	}
}

// writeHostCommands は、本体のコマンドのうち拡張機能のコマンドと同じ口から呼ぶもの（editor.HostCommandNames）を、
// 名前と、打てる短い名前の表にする。他の名前の先頭の部分になっている名前（:mes は :messages の先頭）は、その名前の短い名前にまとめる。
// この口のコマンドは、表に並べた名前でだけ呼べる（:he のような途中までの省略はできない）。
func writeHostCommands(w *bytes.Buffer) {
	names := editor.HostCommandNames()
	fmt.Fprintln(w, "| コマンド | ほかに打てる名前 |")
	fmt.Fprintln(w, "|---|---|")
	for _, n := range names {
		if slices.ContainsFunc(names, func(o string) bool { return o != n && strings.HasPrefix(o, n) }) {
			continue
		}
		var alt []string
		for _, o := range names {
			if o != n && strings.HasPrefix(n, o) {
				alt = append(alt, code(":"+o))
			}
		}
		fmt.Fprintf(w, "| %s | %s |\n", code(":"+n), strings.Join(alt, "、"))
	}
}
