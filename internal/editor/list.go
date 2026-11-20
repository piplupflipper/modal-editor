package editor

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/piplupflipper/vi-imitation/ext"
	"github.com/piplupflipper/vi-imitation/internal/command/ex"
	"github.com/piplupflipper/vi-imitation/internal/editcmd"
	"github.com/piplupflipper/vi-imitation/internal/exthost"
	"github.com/piplupflipper/vi-imitation/internal/input"
)

// 本体が持つ一覧の問い合わせ（editor/list。基本設計 6.9。FR-107）。
//
// 拡張機能（ファジーファインダー）は、タブ、コマンド、キーの一覧、レジスターの履歴、ジャンプリストを問い合わせて候補にする。
// 応答は問い合わせた時点の写しで、イベントループが、その拡張機能へ最後に問い合わせを始めた client（extView）を読み込んで作る。
// 「その client の」ものになるのはジャンプリスト（client ごと。基本設計 4.4）と、タブの Own である。

// editorList は、一覧 kind の写しを返す。読み込んでいる client を、問い合わせを始めた client として扱う。
//
// req: FR-107
func (e *Editor) editorList(kind ext.EditorListKind) ext.EditorListResult {
	var r ext.EditorListResult
	switch kind {
	case ext.ListTabs:
		r.Tabs = e.listTabs()
	case ext.ListCommands:
		r.Commands = e.listCommands()
	case ext.ListKeys:
		r.Keys = listKeys(e.keyRows("", nil))
	case ext.ListRegisters:
		r.Registers = e.listRegisters()
	case ext.ListJumps:
		r.Jumps = e.listJumps()
	}
	return r
}

// execGrant は、拡張機能のポップアップで利用者が項目を選んだ（view/select）ことで、その拡張機能に許した 1 回の editor/execute である。
type execGrant struct {
	// selection は、view/select に付けた選択の番号（ext.ViewSelectParams.Selection）である。
	selection uint64
	// client は、選んだ client である。実行はこの client で行う。
	client *clientView
}

// grantExecute は、拡張機能 extID のポップアップで利用者が項目を選んだことを覚え、view/select に付ける選択の番号を返す。
// 拡張機能ごとに最後の 1 回だけを覚える（前の選択への editor/execute は断る）。
func (e *Editor) grantExecute(extID string) uint64 {
	e.selectionSeq++
	if e.execGrants == nil {
		e.execGrants = map[string]execGrant{}
	}
	e.execGrants[extID] = execGrant{selection: e.selectionSeq, client: e.view}
	return e.selectionSeq
}

// revokeExecute は、client c で選んだ選択への、まだ使っていない editor/execute の許しを無効にする（PR #249 の 3 回目の確認の推奨）。
// その client が次のキーを打ったとき（handleKeys）と、ポップアップを利用者か本体が閉じたとき（closeFloat。拡張機能の view/close を除く）に呼ぶ。
// 次の選択が起きたときは grantExecute が置き換える。拡張機能が送らなかった選択の番号で、後から関係の無い時点に実行させないためである。
func (e *Editor) revokeExecute(c *clientView) {
	for id, g := range e.execGrants {
		if g.client == c {
			delete(e.execGrants, id)
		}
	}
}

// errExecuteNotSelected は、利用者の選択への応答でない editor/execute を断る誤りである。
var errExecuteNotSelected = errors.New("editor/execute は、拡張機能が開いた一覧で利用者が選んだ項目への応答としてだけ使えます")

// editorExecuteRequest は、拡張機能の editor/execute を処理する（PR #249 のレビューの必須 1 と再レビューの必須）。
//
// 次をすべて満たすときだけ、選んだ client を操作中の client にして実行する。満たさなければ実行せずに誤りで応答する。
//
//   - params の selection が、その拡張機能のポップアップで利用者が最後に選んだ（view/select）ときの番号と一致する。1 回の選択に 1 回だけ
//   - 選んだ client が、ノーマルモードで入力の途中でない（viewIdle。FR-63 の focusOwner と同じ判定）。外部のコマンドか再生中の拡張機能の結果を待っていない
//   - command なら、コマンドの一覧（listCommands）の名前か最短の形と完全に一致する（範囲、引数、| を含む行は断る）
//
// 実行は editorExecute が行う。
func (e *Editor) editorExecuteRequest(ev *exthost.EditorExecuteRequest) {
	p := ev.Params
	g, ok := e.execGrants[ev.Extension]
	if !ok || p.Selection == 0 || g.selection != p.Selection || g.client == nil || g.client.detached {
		// 利用者が選んでいないのに実行を頼まれた。拡張機能の不具合か、選択と関係の無い実行なので、利用者にも知らせる。
		e.withView(e.extView(ev.Extension), func() { e.showError(fmt.Errorf("拡張機能 %s: %w", ev.Extension, errExecuteNotSelected)) })
		_ = ev.ReplyError(errExecuteNotSelected)
		return
	}
	delete(e.execGrants, ev.Extension)
	var refuse error
	e.withView(g.client, func() {
		switch {
		case p.Command != "" && !e.knownCommand(p.Command):
			refuse = fmt.Errorf("コマンド %q はコマンドの一覧にありません（範囲と引数は付けられません）", p.Command)
		case e.suspension() != nil || !e.viewIdle():
			refuse = errors.New("入力の途中か待っている間なので、ファインダーで選んだものを実行しませんでした")
		}
		if refuse != nil {
			e.showError(refuse)
		}
	})
	if refuse != nil {
		_ = ev.ReplyError(refuse)
		return
	}
	_ = ev.Reply()
	// 実行は操作中の client として行う（キーを打ったのと同じ）。終わった後も、その client を操作中のままにする。
	e.useClient(g.client)
	e.editorExecute(p)
	e.afterKey()
}

// knownCommand は、name がコマンドの一覧（listCommands）の名前か最短の形と完全に一致するかを返す。
func (e *Editor) knownCommand(name string) bool {
	return slices.ContainsFunc(e.listCommands(), func(c ext.ListCommand) bool { return c.Name == name || (c.Short != "" && c.Short == name) })
}

// editorExecute は、editor/execute を、読み込んでいる client（操作中の client にしてある）で行う。editorExecuteRequest が確かめた後に呼ぶ。
//
//   - Keys: キーの一覧（keyRows）からその表記の行を引き直し、キーの一覧で選んだのと同じく selectKeyRow で実行する。
//     同じ表記の行が複数あれば（< の n と v など）、ノーマルモードの行を選ぶ
//   - Command: :Command<CR> を、キーマップを通さずに打ったのと同じく渡す
//
// どちらもマクロに記録しない（選んだ <CR> が記録されている）。ドットリピートの値は、打ったときと同じく替わる。
//
// req: FR-107
func (e *Editor) editorExecute(p ext.EditorExecuteParams) {
	if p.Keys != "" {
		rows := e.keyRows("", nil)
		pick := -1
		for i, r := range rows {
			if r.Label != p.Keys {
				continue
			}
			if pick < 0 {
				pick = i
			}
			if r.Modes&input.MapNormal != 0 && !r.CommandLine {
				pick = i
				break
			}
		}
		if pick < 0 {
			e.showError(fmt.Errorf("キー %s はキーの一覧にありません", p.Keys))
			return
		}
		e.selectKeyRow(rows[pick])
		return
	}
	keys := []editcmd.Key{editcmd.Rune(':')}
	for _, r := range p.Command {
		keys = append(keys, editcmd.Rune(r))
	}
	keys = append(keys, editcmd.Special(editcmd.CodeEnter))
	e.noRecord = true
	defer func() { e.noRecord = false }()
	e.feedMapped(keys)
}

// listTabs は、すべての編集の client のタブを、client の古い順に、タブの並びの順で返す。
// カーソルのあるタブは、フォーカスのあるポップアップを開いている client では、ポップアップを開いたときのタブである（Editor.tab）。
func (e *Editor) listTabs() []ext.ListTab {
	var out []ext.ListTab
	for _, c := range e.clients {
		if c.detached {
			continue
		}
		v := e.viewOf(c)
		cur := v.cur
		if f := v.float; f != nil && f.focused {
			cur = f.base
		}
		for _, bs := range c.tabs {
			out = append(out, ext.ListTab{
				// client の番号は、本体のほかの表示（ownerName の「client N のタブです」など）と同じ ClientInfo.ID である（PR #249 のレビューの必須 3）。
				Buffer: bs.id, Path: bs.path, Name: e.bufferName(bs), Client: uint64(c.info.ID),
				Own: c == e.view, Current: bs == cur,
				Modified: bs.buf.Modified(), ReadOnly: bs.buf.ReadOnly(),
			})
		}
	}
	return out
}

// listCommands は、コマンドラインから呼べるコマンドを返す。本体の Ex コマンドと別名（設定ファイルの別名を含む。ex.Names の表の順）の後に、
// 拡張機能のコマンドと、本体が拡張機能のコマンドの口から呼ぶコマンド（:help、:keys、:ext、provides の操作の名前）を名前の順に並べる。
//
// req: FR-150
func (e *Editor) listCommands() []ext.ListCommand {
	var out []ext.ListCommand
	for _, n := range ex.Names(e.aliases) {
		out = append(out, ext.ListCommand{Name: n.Full, Short: n.Short})
	}
	desc := map[string]string{}
	for _, s := range e.cfg.Extensions {
		for _, c := range s.Manifest.Contributes.Commands {
			if _, ok := desc[c.ID]; !ok {
				desc[c.ID] = c.Description
			}
		}
	}
	names := make([]string, 0, len(e.exCommands))
	for name := range e.exCommands {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		c := ext.ListCommand{Name: name}
		switch id := e.exCommands[name]; id {
		case helpExtension, input.ProviderExtension, extManagerExtension:
			// 本体のコマンド。provides の操作は、どの拡張機能が引き受けるかが呼ぶまで決まらないので、拡張機能の ID を書かない。
		default:
			c.Extension, c.Description = id, desc[name]
		}
		out = append(out, c)
	}
	return out
}

// listKeys は、キーの一覧の行（keyRows）を editor/list の形にする。
func listKeys(rows []keyRow) []ext.ListKey {
	out := make([]ext.ListKey, len(rows))
	for i, r := range rows {
		modes := modeLetters(r.Modes)
		if r.CommandLine {
			modes += "c"
		}
		k := ext.ListKey{Keys: r.Label, Modes: modes, Description: r.Description, Overridden: modeLetters(r.Overridden)}
		switch r.Source {
		case sourceBuiltin:
			k.Source = "builtin"
		case sourceUser:
			k.Source = "user"
		case sourceExtension:
			k.Source, k.Extension = "extension", r.Extension
		}
		out[i] = k
	}
	return out
}

// listRegisters は、カーソルのあるバッファのレジスターと履歴（:registers の一覧と同じ並び）を返す。
// レジスターは client の間で共有する（FR-58）。
func (e *Editor) listRegisters() []ext.ListRegister {
	entries := e.cur.state.Registers().List()
	out := make([]ext.ListRegister, len(entries))
	for i, en := range entries {
		r := ext.ListRegister{Index: en.Index, Text: en.Register.Text, Linewise: en.Register.Linewise}
		if en.Name != 0 {
			r.Name, r.Index = string(en.Name), 0
		}
		out[i] = r
	}
	return out
}

// listJumps は、読み込んでいる client のジャンプリストを、古い順に返す。:jumps と同じく、先に同じ行の古い項目を除く（cleanupJumps）。
func (e *Editor) listJumps() []ext.ListJump {
	e.cleanupJumps()
	out := make([]ext.ListJump, 0, len(e.jumps))
	for i, j := range e.jumps {
		en := ext.ListJump{Buffer: j.buf, Position: toExtPos(j.pos), Current: i == e.jumpIdx}
		if bs := e.findBuffer(j.buf); bs != nil {
			en.Path, en.Name = bs.path, e.bufferName(bs)
			if j.pos.Line < bs.buf.LineCount() {
				en.Text = strings.TrimLeft(bs.buf.Line(j.pos.Line), " \t")
			}
		}
		out = append(out, en)
	}
	return out
}
