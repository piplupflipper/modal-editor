package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/piplupflipper/vi-imitation/internal/command/ex"
	"github.com/piplupflipper/vi-imitation/internal/config"
	"github.com/piplupflipper/vi-imitation/internal/editcmd"
	"github.com/piplupflipper/vi-imitation/internal/editor"
	"github.com/piplupflipper/vi-imitation/internal/exthost"
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
	type defaultKey struct {
		label string
		modes input.MapMode // 登録が有効なモード（normal、visual、insert の 3 つに限る）
	}
	keys := map[string][]defaultKey{} // ID と種類から、既定のキーへ
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
		modes := bd.Modes & (keymapModes | input.MapOperatorPending)
		if j := slices.IndexFunc(keys[k], func(d defaultKey) bool { return d.label == label }); j >= 0 {
			keys[k][j].modes |= modes
		} else {
			keys[k] = append(keys[k], defaultKey{label, modes})
		}
	}
	kinds := map[input.BindingKind]string{input.BindMotion: "モーション", input.BindOperator: "オペレーター", input.BindTextObject: "テキストオブジェクト", input.BindAction: "操作"}
	fmt.Fprintln(w, "| ID | 種類 | モード | 既定のキー | 説明 |")
	fmt.Fprintln(w, "|---|---|---|---|---|")
	for _, act := range input.Actions() {
		rowModes := act.Modes & keymapModes
		var ks []string
		for _, k := range keys[actionKey(act.ID, act.Kind)] {
			s := code(k.label)
			if k.modes&keymapModes != rowModes {
				// 行のモードの一部だけで効くキー（visual の u は lowercase、normal の u は undo）。
				// オペレーターの後だけで効くテキストオブジェクトのキー（dgc の gc）は「オペレーターの後」と書く
				label := modeNames(k.modes)
				if k.modes&input.MapOperatorPending != 0 {
					label = strings.TrimPrefix(label+"、オペレーターの後", "、")
				}
				s += "（" + label + "）"
			}
			ks = append(ks, s)
		}
		fmt.Fprintf(w, "| %s | %s | %s | %s | %s |\n", code(act.ID), kinds[act.Kind], modeNames(rowModes), cell(strings.Join(ks, " ")), cell(act.Description))
	}
	return nil
}

// keymapModes は、設定の keymap に書けるモードのうち、操作の ID を割り当てられるもの（normal、visual、insert）である。
const keymapModes = input.MapNormal | input.MapVisual | input.MapInsert

// modeNames は、モードの集合を、設定の keymap のモードの名前を「、」でつないだ文字列にする。
func modeNames(modes input.MapMode) string {
	var names []string
	for _, m := range []input.MapMode{input.MapNormal, input.MapVisual, input.MapInsert} {
		if modes&m != 0 {
			names = append(names, input.ModeName(m))
		}
	}
	return strings.Join(names, "、")
}

// writeKeyNames は、キーの表記で < と > で囲む名前と、修飾キーを書く。
// 特殊キーの名前は editcmd.Key.String から取る。文字のキーの名前（<Space>、<lt>、<Bar>、<Bslash>）と修飾キーは、
// 表記の文字列を editcmd.ParseKeys で読めることを確かめてから書く（読めなければ生成を誤りにする）。
func writeKeyNames(w *bytes.Buffer) error {
	var names []string
	for c := editcmd.CodeEsc; c <= editcmd.CodeInsert; c++ {
		names = append(names, code(editcmd.Special(c).String()))
	}
	f1, _ := editcmd.FunctionKey(1)
	fn, _ := editcmd.FunctionKey(editcmd.MaxFunctionKey)
	names = append(names, code(editcmd.Special(f1).String())+"〜"+code(editcmd.Special(fn).String()))
	runes := []string{"<Space>", "<lt>", "<Bar>", "<Bslash>"}
	mods := []string{"<C-a>", "<A-a>", "<M-a>", "<S-Tab>"}
	for _, s := range append(slices.Clone(runes), mods...) {
		if _, err := editcmd.ParseKeys(s); err != nil {
			return fmt.Errorf("キーの表記 %s を読めない: %w", s, err)
		}
	}
	for _, s := range runes {
		names = append(names, code(s))
	}
	fmt.Fprintf(w, "`<` と `>` で囲む名前: %s（`<lt>` は `<` そのもの）。\n", strings.Join(names, "、"))
	fmt.Fprintln(w, "修飾キー: `C-`（Ctrl）、`A-` か `M-`（Alt）、`S-`（Shift）を名前の前に付ける（`<C-w>`、`<A-x>`、`<S-Tab>`）。名前と修飾キーの大文字と小文字は区別しない。")
	return nil
}

// keymapModeNames は、設定の keymap のモードの名前である。writeKeymapModes が、設定ファイルとして読めることを確かめる。
var keymapModeNames = []string{"normal", "visual", "insert", "commandLine"}

// writeKeymapModes は、設定の keymap のモードの名前を書く。どの名前も、設定ファイルの keymap のキーとして誤りなく読めることを
// config.Parse で確かめ、読めなければ生成を誤りにする。normal、visual、insert は、操作の表のモードの名前（input.ModeName）と同じことも確かめる。
func writeKeymapModes(w *bytes.Buffer) error {
	for i, m := range keymapModeNames {
		if _, errs := config.Parse("config.jsonc", []byte(`{"keymap":{"`+m+`":{}}}`)); len(errs) > 0 {
			return fmt.Errorf("keymap のモードの名前 %s を設定ファイルで読めない: %v", m, errs)
		}
		if i < 3 && input.ModeName([]input.MapMode{input.MapNormal, input.MapVisual, input.MapInsert}[i]) != m {
			return fmt.Errorf("keymap のモードの名前 %s が操作の表のモードの名前と違う", m)
		}
	}
	names := make([]string, len(keymapModeNames))
	for i, m := range keymapModeNames {
		names[i] = code(m)
	}
	fmt.Fprintf(w, "モードの名前は %s である。\n", strings.Join(names, "、"))
	return nil
}

// duration は、時間の長さを文書の書き方（5 秒、500 ms、1 分）にする。
func duration(d time.Duration) string {
	switch {
	case d%time.Minute == 0:
		return fmt.Sprintf("%d 分", d/time.Minute)
	case d%time.Second == 0:
		return fmt.Sprintf("%d 秒", d/time.Second)
	default:
		return fmt.Sprintf("%d ms", d/time.Millisecond)
	}
}

// writeLifecycle は、拡張機能の起動から終了までの流れと期限を、internal/exthost と internal/editor の既定値の定数から書く。
func writeLifecycle(w *bytes.Buffer) {
	fmt.Fprintln(w, "1. 本体は、起動の契機が成り立ったら `command` を `args` と一緒に起動する。作業ディレクトリは本体のものを引き継ぐ")
	fmt.Fprintf(w, "2. 本体は `initialize` を送る。%s 以内に応答が無ければ異常とみなす\n", duration(exthost.DefaultInitTimeout))
	fmt.Fprintln(w, "3. 本体は、開いているバッファの全文を `buffer/open` で送り、その後の変更を `buffer/change` で送る")
	fmt.Fprintf(w, "4. 本体は %s ごとに `ping` を送る。%s 以内に応答が無ければ異常とみなす。時間のかかる処理は、メッセージを読む処理と別に動かす\n",
		duration(exthost.DefaultPingInterval), duration(exthost.DefaultPingTimeout))
	fmt.Fprintf(w, "5. 本体は、エディタを終えるときに `shutdown` を送る。%s で終わらなければ SIGTERM を、さらに %s で SIGKILL を送る\n",
		duration(exthost.DefaultShutdownTimeout), duration(exthost.DefaultTerminateTimeout))
	fmt.Fprintln(w)
	fmt.Fprintf(w, "異常（プロセスの終了、期限切れ、解釈できないメッセージ）のときは、プロセスグループに SIGKILL を送り、%s から %s まで倍に延ばしながら起動し直す。\n",
		duration(exthost.DefaultRestartDelay), duration(exthost.DefaultMaxRestartDelay))
	fmt.Fprintf(w, "%d 回続けて失敗すると無効にする（%s 続けて動けば、失敗を数え直す）。\n", exthost.DefaultMaxFailures, duration(exthost.DefaultResetAfter))
	fmt.Fprintln(w, "起動し直したときは、開いているバッファの全文を送り直す。")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "キー入力ごとに結果が要る問い合わせ（`motion/resolve`、`textObject/resolve`、`operator/apply`）は、期限（設定の `extensions.syncTimeoutMs`。既定は %s）までに応答が無ければ、その操作を失敗にする。\n",
		duration(editor.DefaultSyncTimeout))
	fmt.Fprintln(w, "期限を過ぎて届いた応答は適用しない。")
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
