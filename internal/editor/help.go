package editor

import (
	"fmt"
	"slices"
	"strings"

	"github.com/piplupflipper/modal-editor/docs"
	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/internal/buffer"
	"github.com/piplupflipper/modal-editor/internal/command"
	"github.com/piplupflipper/modal-editor/internal/editcmd"
	"github.com/piplupflipper/modal-editor/internal/input"
	"github.com/piplupflipper/modal-editor/internal/ui"
)

// ヘルプ（:help。FR-89）とキーの一覧（FR-90）。基本設計 7.2。
//
// どちらも、ファイルでないバッファ（bufferState.title を持つ、path の無いバッファ）を、読み込んでいる client のタブに出す。
// 基本設計 7.2 はヘルプをテキストのバッファの部品のポップアップに、キーの一覧を一覧の部品の中央のポップアップに出すとしているが、
// ポップアップの部品はまだ無いので、今はタブに出す（仮。#227）。
// ファイルでないバッファは、編集を禁止した状態で始める（FR-88。基本設計 4.6）。
// 同じ client に同じ名前のタブがあれば、新しいタブを作らずにそのタブの中身を替えて移る。
//
// :help と :keys は本体のコマンドだが、:ext（extsync.go）と同じく ex パッケージの表には置かず、
// 拡張機能のコマンド（ex.State.ExtCommand）と同じ口から呼ぶ。コマンドラインの表（exCommands）に helpExtension を入れ、
// extQuery.Action が helpAction へ渡す。引数を行末まで取ること、範囲と ! を取らないことは、拡張機能のコマンドと同じである。
//
// 接頭辞で絞ったキーの一覧は、接頭辞の後に ? を打って出す（<leader>l? で <leader>l に続くキー）。
// <leader>l? と <leader>d? は本体の既定のキー（input の keyListKeys）で、他の接頭辞の ? は addKeyListBindings が作る。
// どちらも Ref.Extension を input.ProviderExtension、ID を input.KeyListPrefix で始まる値にした操作で、helpAction が受け取る。

// helpExtension は、コマンドラインの表（exCommands）で :help と :keys に入れる拡張機能の ID の代わりの値である。
// 空白を含むので、宣言ファイルの拡張機能の ID（空白を含めない。ext.Manifest.Validate）と重ならない。
const helpExtension = " help"

// helpCommandNames は、:help のコマンドの名前である。Vim と同じく :h でも呼べる。
// 拡張機能のコマンドの口から呼ぶので、Vim のような途中までの省略（:he、:hel）はできない。
var helpCommandNames = []string{"help", "h"}

// keysCommandName は、キーの一覧を出すコマンドの名前（:keys {接頭辞}。名前は仮）である。
const keysCommandName = "keys"

// helpTitle と keyListTitle は、ヘルプとキーの一覧のタブの名前である（仮）。
const (
	helpTitle    = "[ヘルプ]"
	keyListTitle = "[キーの一覧]"
)

// addHelpCommands は、コマンドラインの表に :help、:h、:keys を足す。
// 拡張機能が同じ ID のコマンドを宣言していれば、本体のコマンドを優先し、誤りを返す（addExtManagerCommand と同じ）。
func addHelpCommands(cmds map[string]string) []error {
	var errs []error
	for _, name := range append(slices.Clone(helpCommandNames), keysCommandName) {
		if prev, ok := cmds[name]; ok {
			errs = append(errs, fmt.Errorf("拡張機能 %s のコマンド %s は本体の :%s と重なるので、コマンドラインからは呼べない", prev, name, name))
		}
		cmds[name] = helpExtension
	}
	return errs
}

// helpAction は、ref が :help、:keys、キーの一覧のキー（<leader>l? など）なら、それを行い、true と誤りを返す。
// それ以外の操作なら false を返す。text は、コマンドラインから呼んだときの引数である。
// bs は、操作を呼んだバッファである。出したタブへ移ったら command.ErrBufferSwitched を返し、
// マクロの再生の残りを移った先のバッファで続けさせる（navigator.Navigate の <Tab> と同じ）。
func (e *Editor) helpAction(bs *bufferState, ref editcmd.Ref, text string) (bool, error) {
	var err error
	switch {
	case ref.Extension == helpExtension && slices.Contains(helpCommandNames, ref.ID):
		err = e.openHelp(text)
	case ref.Extension == helpExtension && ref.ID == keysCommandName:
		prefix, perr := editcmd.ParseKeysLeader(strings.TrimSpace(text), e.leader)
		if perr != nil {
			return true, fmt.Errorf("E475: 無効な引数です: %s", text)
		}
		err = e.openKeyList(prefix)
	case ref.Provider() && strings.HasPrefix(ref.ID, input.KeyListPrefix):
		prefix, perr := editcmd.ParseKeys(strings.TrimPrefix(ref.ID, input.KeyListPrefix))
		if perr != nil {
			return true, fmt.Errorf("内部の誤り: キーの一覧の接頭辞 %q を読めない: %w", ref.ID, perr)
		}
		err = e.openKeyList(prefix)
	default:
		return false, nil
	}
	if err == nil && e.cur != bs {
		err = command.ErrBufferSwitched
	}
	return true, err
}

// openScratch は、ファイルでないバッファ title を、読み込んでいる client のタブに出し、カーソルを line 行目（0 始まり）の先頭に置く。
// 同じ名前のタブがこの client にあれば、中身を text に替えてそのタブへ移る。
// バッファは編集を禁止した状態で始める（FR-88）。中身を替えるのは、禁止の判定を通らない読み直し（reload）の入り口で行う（基本設計 4.6）。
//
// req: FR-88
func (e *Editor) openScratch(title, text string, line int) {
	var bs *bufferState
	for _, t := range e.view.tabs {
		if t.path == "" && t.title == title {
			bs = t
			break
		}
	}
	if bs == nil {
		b := buffer.New(text)
		b.SetFixEndOfLine(e.fixEOL)
		b.SetReadOnly(true)
		bs = e.newBuffer(b, "", "")
		bs.title = title
	} else {
		// 中身が同じなら、reload は何も変えない（reloadEdits が空）。
		e.reload(bs, buffer.New(text))
	}
	bs.state.SetCursor(buffer.Position{Line: min(max(line, 0), bs.buf.LineCount()-1)})
	e.enter(bs)
}

// bufferTitle は、ステータス行に出すバッファの名前（パス。無ければファイルでないバッファの名前。どちらも無ければ空）を返す。
func bufferTitle(bs *bufferState) string {
	if bs.path != "" {
		return bs.path
	}
	return bs.title
}

// openHelp は、:help の引数 word の説明を出す。word が空なら先頭を出す。
// 説明は docs/usage.md（docs.Usage）に、読み込んだ拡張機能の操作の説明文（宣言ファイルの description）の節を足したものである。
// 引き方は helpLine にある。見つからなければ E149 を返す（Vim と同じ番号）。
//
// req: FR-89
func (e *Editor) openHelp(word string) error {
	text := e.helpText()
	line := 0
	if w := strings.TrimSpace(word); w != "" {
		l, ok := helpLine(strings.Split(text, "\n"), w)
		if !ok {
			return fmt.Errorf("E149: %s のヘルプがありません", w)
		}
		line = l
	}
	e.openScratch(helpTitle, text, line)
	return nil
}

// helpText は、:help に出す文書である。docs/usage.md の後に、読み込んだ拡張機能の操作の一覧の節を足す。
func (e *Editor) helpText() string {
	var sb strings.Builder
	sb.WriteString(strings.TrimRight(docs.Usage, "\n"))
	sb.WriteString("\n")
	var lines []string
	for _, s := range e.cfg.Extensions {
		m := s.Manifest
		groups := []struct {
			kind string
			cs   []ext.Contribution
		}{
			{"オペレーター", m.Contributes.Operators},
			{"モーション", m.Contributes.Motions},
			{"テキストオブジェクト", m.Contributes.TextObjects},
			{"コマンド", m.Contributes.Commands},
		}
		for _, g := range groups {
			for _, c := range g.cs {
				line := fmt.Sprintf("- `%s`（拡張機能 %s の%s", c.ID, m.ID, g.kind)
				if c.Keys != "" {
					line += fmt.Sprintf("。キーは `%s`", c.Keys)
				}
				line += "）"
				if c.Description != "" {
					line += ": " + c.Description
				}
				lines = append(lines, line)
			}
		}
	}
	if len(lines) > 0 {
		sb.WriteString("\n## 読み込んだ拡張機能の操作\n\n")
		sb.WriteString("本体が起動したときに読んだ宣言ファイルの contributes の一覧である。説明は宣言ファイルの description による。\n\n")
		sb.WriteString(strings.Join(lines, "\n"))
		sb.WriteString("\n")
	}
	return sb.String()
}

// helpLine は、:help の文書の行 lines から、語 word の説明の行を探し、その行番号（0 始まり）を返す（基本設計 7.2）。
//
// 語は、Ex コマンドの名前（:readonly。: は省いてもよい）、設定の項目の名前（registers.historySize）、キーの列（<C-o>）である。
// 次の順に探し、各段で最初に当たった行を返す（仮。#227）。
//
//  1. 見出しの行（コードの区画の外の # で始まる行）のうち、`語` か語そのものを含む行
//  2. `語` を含む行。語の後に空白が続く形（`:acp.prompt <プロンプト>`）も当てる
//  3. 設定の例の JSON のキー（"語"）を含む行。点で区切った名前は、最後の部分（"historySize"）でも探す
//  4. 語そのものを含む行
//
// 語の形は、: を足したもの（Ex コマンドの形）、そのままのものの順に探す（: で始まる語は、そのまま、: を除いたものの順）。
// :help readonly が、記号の設定の `readonly` より先に :readonly の説明に当たるようにするためである。
func helpLine(lines []string, word string) (int, bool) {
	forms := []string{":" + word, word}
	if rest, ok := strings.CutPrefix(word, ":"); ok {
		forms = []string{word}
		if rest != "" {
			forms = append(forms, rest)
		}
	}
	quoted := func(l, f string) bool {
		return strings.Contains(l, "`"+f+"`") || strings.Contains(l, "`"+f+" ")
	}
	// 1. 見出し。
	inCode := false
	for i, l := range lines {
		if strings.HasPrefix(l, "```") {
			inCode = !inCode
			continue
		}
		if !inCode && strings.HasPrefix(l, "#") && (quoted(l, forms[0]) || quoted(l, forms[len(forms)-1]) || strings.Contains(l, word)) {
			return i, true
		}
	}
	// 2. `語`。
	for _, f := range forms {
		for i, l := range lines {
			if quoted(l, f) {
				return i, true
			}
		}
	}
	// 3. JSON のキー。
	keys := []string{`"` + word + `"`}
	if j := strings.LastIndexByte(word, '.'); j >= 0 && j+1 < len(word) {
		keys = append(keys, `"`+word[j+1:]+`"`)
	}
	for _, k := range keys {
		for i, l := range lines {
			if strings.Contains(l, k) {
				return i, true
			}
		}
	}
	// 4. 語そのもの。
	for i, l := range lines {
		if strings.Contains(l, word) {
			return i, true
		}
	}
	return 0, false
}

// keyRow は、キーの一覧の 1 行である。
type keyRow struct {
	keys, modes, origin, desc string
}

// openKeyList は、接頭辞 prefix で始まるキーの一覧を出す。prefix が空ならすべてのキーを出す。
// 該当するキーが無ければ誤りを返す。
//
// req: FR-90
func (e *Editor) openKeyList(prefix []editcmd.Key) error {
	rows := e.keyRows(prefix)
	if len(rows) == 0 {
		return fmt.Errorf("%s で始まるキーはありません", editcmd.KeysString(prefix))
	}
	e.openScratch(keyListTitle, keyListText(prefix, rows), 0)
	return nil
}

// keyRows は、接頭辞 prefix で始まるキーの一覧の行を、キーの表記の順に返す（基本設計 7.2）。
//
// 行は次の 3 つから作る。
//
//   - キーマップ（本体の既定のキー、LSP と DAP の既定のキー、拡張機能の contributes のキー）。説明は input.Binding.Description で、
//     拡張機能の項目に説明文が無ければ操作の ID を出す
//   - 利用者のキーマップ（設定の keymap）。説明は展開先のキーの列である。説明文を書く項目は設定に足していない（仮）
//   - Parser が表を引く前に読むキー（回数、レジスター、マクロ、ドットリピート。input.ParserKeys）。prefix が空のときだけ出す
//
// 利用者のキーマップが本体のキーと同じキーの列なら、両方を出す（効くのは利用者のキーマップ）。
func (e *Editor) keyRows(prefix []editcmd.Key) []keyRow {
	var rows []keyRow
	for _, b := range e.keymap.Bindings() {
		if !hasKeyPrefix(b.Keys, prefix) {
			continue
		}
		origin, desc := "本体", b.Description
		if !b.Ref.Builtin() && !b.Ref.Provider() {
			origin = "拡張機能 " + b.Ref.Extension
			if desc == "" {
				desc = b.Ref.ID
			}
		}
		rows = append(rows, keyRow{keys: editcmd.KeysString(b.Keys), modes: modeLetters(b.Modes), origin: origin, desc: desc})
	}
	if r := e.remap; r != nil {
		for _, mode := range []mapMode{mapNormal, mapVisual, mapInsert, mapCommandLine} {
			for _, m := range r.maps[mode] {
				if !hasKeyPrefix(m.lhs, prefix) {
					continue
				}
				rows = append(rows, keyRow{keys: editcmd.KeysString(m.lhs), modes: remapModeLetter[mode], origin: "設定", desc: "→ " + editcmd.KeysString(m.rhs)})
			}
		}
	}
	slices.SortStableFunc(rows, func(a, b keyRow) int { return strings.Compare(a.keys, b.keys) })
	if len(prefix) == 0 {
		for _, k := range input.ParserKeys() {
			rows = append(rows, keyRow{keys: k.Keys, modes: modeLetters(k.Modes), origin: "本体", desc: k.Description})
		}
	}
	return rows
}

// keyListText は、キーの一覧の行 rows を、列をそろえた文書にする。
func keyListText(prefix []editcmd.Key, rows []keyRow) string {
	head := fmt.Sprintf("キーの一覧（すべて。%d 件）", len(rows))
	if len(prefix) > 0 {
		head = fmt.Sprintf("キーの一覧（%s で始まるもの。%d 件）", editcmd.KeysString(prefix), len(rows))
	}
	cols := [3]string{"キー", "モード", "出どころ"}
	width := [3]int{}
	for i, c := range cols {
		width[i] = ui.StringWidth(c, 8)
	}
	for _, r := range rows {
		width[0] = max(width[0], ui.StringWidth(r.keys, 8))
		width[1] = max(width[1], ui.StringWidth(r.modes, 8))
		width[2] = max(width[2], ui.StringWidth(r.origin, 8))
	}
	pad := func(s string, w int) string { return s + strings.Repeat(" ", w-ui.StringWidth(s, 8)+2) }
	var sb strings.Builder
	sb.WriteString(head + "\n")
	sb.WriteString("モードは n（ノーマル）、v（ビジュアル）、o（オペレーターの後）、i（インサート）、c（コマンドライン）。接頭辞の後に ? を打つか :keys {接頭辞} で絞る\n\n")
	sb.WriteString(pad(cols[0], width[0]) + pad(cols[1], width[1]) + pad(cols[2], width[2]) + "説明\n")
	for _, r := range rows {
		sb.WriteString(pad(r.keys, width[0]) + pad(r.modes, width[1]) + pad(r.origin, width[2]) + r.desc + "\n")
	}
	return sb.String()
}

// hasKeyPrefix は、keys が prefix で始まるかを返す。
func hasKeyPrefix(keys, prefix []editcmd.Key) bool {
	return len(keys) >= len(prefix) && slices.Equal(keys[:len(prefix)], prefix)
}

// modeLetters は、モードの集合を n、v、o、i の文字の列にする。
func modeLetters(m input.MapMode) string {
	var sb strings.Builder
	for _, x := range []struct {
		mode input.MapMode
		c    byte
	}{{input.MapNormal, 'n'}, {input.MapVisual, 'v'}, {input.MapOperatorPending, 'o'}, {input.MapInsert, 'i'}} {
		if m&x.mode != 0 {
			sb.WriteByte(x.c)
		}
	}
	return sb.String()
}

// remapModeLetter は、利用者のキーマップのモードの文字である。
var remapModeLetter = map[mapMode]string{mapNormal: "n", mapVisual: "v", mapInsert: "i", mapCommandLine: "c"}

// addKeyListBindings は、ノーマルモードのキーの登録の接頭辞のうち、それ自体は操作でないもの（<Space>、<Space>l、g、<C-w> など）に、
// ? を続けたキーの一覧の操作（input.KeyListBinding）を足す（基本設計 7.2。仮）。
// 接頭辞は、km の登録（本体、LSP と DAP、拡張機能）と、利用者のキーマップ remap の左辺から集める。
// 既に登録のあるキーの列（既定の <leader>l? など）と、Parser が先に読むキーで始まるものは足さない。
//
// req: FR-90
func addKeyListBindings(km *input.Keymap, remap *remapper) {
	var seqs [][]editcmd.Key
	for _, b := range km.Bindings() {
		if b.Modes&input.MapNormal != 0 {
			seqs = append(seqs, b.Keys)
		}
	}
	userLHS := map[string]bool{}
	if remap != nil {
		for _, m := range remap.maps[mapNormal] {
			seqs = append(seqs, m.lhs)
			userLHS[editcmd.KeysString(m.lhs)] = true
		}
	}
	prefixes := map[string][]editcmd.Key{}
	for _, s := range seqs {
		for n := 1; n < len(s); n++ {
			prefixes[editcmd.KeysString(s[:n])] = s[:n]
		}
	}
	names := make([]string, 0, len(prefixes))
	for name := range prefixes {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		p := prefixes[name]
		if _, ok := km.Lookup(input.MapNormal, p); ok || userLHS[name] {
			continue
		}
		// 既にあるキーの列（既定の <leader>l?）と先に読むキーは、Bind が断る。断られたら足さない。
		_ = km.Bind(input.KeyListBinding(p, name+" で始まるキーの一覧"))
	}
}
