package ex

import (
	"strings"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
)

// rangeKind は、コマンドが範囲を取るかと、省略したときの範囲を表す。
type rangeKind int

const (
	rangeNone    rangeKind = iota // 範囲を取らない
	rangeCurrent                  // 範囲を取り、省略すると現在の行
	rangeWhole                    // 範囲を取り、省略するとバッファ全体
	rangeWindow                   // 範囲にウィンドウの番号を取る（:q）。ウィンドウは 1 つなので、値は使わない
)

// barKind は、コマンドが "|" をどう扱うかを表す。
type barKind int

const (
	// barSplit は、"\" の付かない最初の "|" で引数を終え、続きを次のコマンドにする（:w、:q、:e、:d）。
	barSplit barKind = iota
	// barSelf は、コマンド自身が引数を読み、"|" を見つけたら invocation.next に入れる（:s。パターンの中の "|" を区切りにしない）。
	barSelf
	// barNone は、"|" を含めて行末までを引数にする（:g、:v）。
	barNone
)

// command は、Ex コマンドの表の 1 行である。
type command struct {
	name     string // 省略しない名前
	min      int    // 省略できる最短の長さ（"substitute" は "s" まで省略できるので 1）
	bang     bool   // "!" を付けられるか
	rng      rangeKind
	inGlobal bool // :g の中で実行できるか（:g 自身は、入れ子を E147 で断るために true にする）
	// modify は、バッファのテキストを変えるコマンドであることを表す（Vim の EX_MODIFY）。
	// 編集を禁止したバッファでは、範囲と引数を確かめる前に E21 で断る（FR-85。Vim 9.1 と同じく :5d も :2,1d も E21）。
	modify bool
	bar    barKind
	// complete は、コマンドラインの補完（Complete。FR-195）で、引数の位置に何を補うかである。
	complete argKind
	run      func(c *execCtx, inv *invocation) error
}

// invocation は、解析したコマンドの呼び出しである。
type invocation struct {
	name    string
	namePos int
	bang    bool
	// line1 と line2 は、範囲の始まりと終わりの行（0 始まり、両端を含む）である。rng が rangeNone のコマンドでは意味を持たない。
	line1, line2 int
	rangeCount   int    // 書かれていたアドレスの数。0 なら範囲は省略されている
	rangePos     int    // 範囲の始まりの位置
	zero         bool   // 範囲の最後のアドレスが 0 だったか（:0r）。line2 は 0 に丸めてある
	arg          string // コマンド名と "!" の後の、先頭の空白を除いた引数。barSplit のコマンドでは "|" の手前まで
	argPos       int
	// next と nextPos は、"|" の後に続くコマンドとその位置である。hasNext が false なら続くコマンドは無い。
	next    string
	nextPos int
	hasNext bool
}

// commands は、対応する Ex コマンドの表である。
// 対象の一覧が裁定で増減したときは、この表の行を足し引きする。
// 表の関数が Execute を経由して表を参照するので、初期化の循環を避けるために init で作る。
var commands []command

func init() {
	commands = []command{
		{name: "write", min: 1, bang: true, rng: rangeWhole, complete: argFile, run: runWrite},
		{name: "wq", min: 2, bang: true, rng: rangeWhole, complete: argFile, run: runWriteQuit},
		{name: "xit", min: 1, bang: true, rng: rangeWhole, complete: argFile, run: runXit},
		{name: "quit", min: 1, bang: true, rng: rangeWindow, run: runQuit},
		{name: "edit", min: 1, bang: true, rng: rangeNone, complete: argFile, run: runEdit},
		{name: "substitute", min: 1, rng: rangeCurrent, inGlobal: true, modify: true, bar: barSelf, run: runSubstitute},
		{name: "global", min: 1, bang: true, rng: rangeWhole, inGlobal: true, bar: barNone, run: runGlobal(false)},
		{name: "vglobal", min: 1, rng: rangeWhole, inGlobal: true, bar: barNone, run: runGlobal(true)},
		{name: "delete", min: 1, rng: rangeCurrent, inGlobal: true, modify: true, run: runDelete},
		{name: "output", min: 3, rng: rangeNone, run: runOutput},
		{name: "outclose", min: 4, rng: rangeNone, run: runOutClose},
		{name: "checktime", min: 6, rng: rangeNone, run: runCheckTime},
		// バッファの一覧と切り替え。"buffer" を "buffers" より前に置き、:bu などの省略形を :buffer にする（Vim と同じ）。
		{name: "buffer", min: 1, bang: true, rng: rangeNone, complete: argBuffer, run: runBuffer},
		{name: "bnext", min: 2, bang: true, rng: rangeNone, run: runBufferStep(BufferNext)},
		{name: "bNext", min: 2, bang: true, rng: rangeNone, run: runBufferStep(BufferPrev)},
		{name: "bprevious", min: 2, bang: true, rng: rangeNone, run: runBufferStep(BufferPrev)},
		{name: "buffers", min: 7, bang: true, rng: rangeNone, run: runBufferList},
		{name: "ls", min: 2, bang: true, rng: rangeNone, run: runBufferList},
		{name: "jumps", min: 2, rng: rangeNone, run: runJumps},
		// "read" を "registers" より前に置き、:r と :re を :read にする（Vim と同じ）。
		{name: "read", min: 1, bang: true, rng: rangeCurrent, modify: true, bar: barNone, run: runRead},
		{name: "registers", min: 3, rng: rangeNone, run: runRegisters},
		{name: "readonly", min: 5, rng: rangeNone, run: runReadOnly},
		{name: "wall", min: 2, bang: true, rng: rangeNone, run: runAll(true, false)},
		{name: "qall", min: 2, bang: true, rng: rangeNone, run: runAll(false, true)},
		{name: "wqall", min: 3, bang: true, rng: rangeNone, run: runAll(true, true)},
		{name: "xall", min: 2, bang: true, rng: rangeNone, run: runAll(true, true)},
		{name: "bdelete", min: 2, bang: true, rng: rangeNone, complete: argBuffer, run: runBufferDelete},
		{name: "tabmove", min: 4, rng: rangeNone, run: runTabMove},
		// 空のタブを開く（FR-151）。点を含む正規の名前で、省略できない。Vim の :tabnew に当たる名前は、設定ファイルの別名で付ける（FR-150）。
		{name: tabNewName, min: len(tabNewName), bang: true, rng: rangeNone, complete: argFile, run: runTabNew},
		// ポップアップとタブの間でバッファを移す（基本設計 1.4。FR-70。名前は仮）。
		{name: "popup", min: 4, rng: rangeNone, run: runPopup(PopupShrink)},
		{name: "tabopen", min: 5, rng: rangeNone, run: runPopup(PopupExpand)},
		// 見比べ（基本設計 6.8）。省略形は Vim 9.1 と同じ。
		{name: "diffsplit", min: 5, rng: rangeNone, complete: argFile, run: runDiffSplit},
		{name: "diffthis", min: 5, rng: rangeNone, run: runDiff(DiffThis)},
		{name: "diffoff", min: 5, bang: true, rng: rangeNone, run: runDiff(DiffOff)},
		{name: "diffupdate", min: 5, bang: true, rng: rangeNone, run: runDiff(DiffUpdate)},
		// 設定の読み書き（基本設計 7.3。FR-132）。点を含む正規の名前で、省略できない。
		// :option.set の値は "|" を含めて行末までなので barNone にする。
		{name: optionSetName, min: len(optionSetName), rng: rangeNone, bar: barNone, complete: argOptionSet, run: runOptionSet},
		{name: optionGetName, min: len(optionGetName), rng: rangeNone, complete: argOptionName, run: runOptionGet},
		// 真偽の項目を反転する（FR-155）。:set 名前! の別名もこれを呼ぶ。
		{name: optionToggleName, min: len(optionToggleName), rng: rangeNone, complete: argOptionBool, run: runOptionToggle},
		// 検索の一致の色を消す（FR-138）。点を含む正規の名前で、省略できない。Vim の :noh[lsearch] は別名の表にある。
		{name: clearHighlightName, min: len(clearHighlightName), rng: rangeNone, run: runClearHighlight},
		// 任意のコマンドを端末多重化ソフトのポップアップで動かす（FR-168）。点を含む正規の名前で、省略できない。
		// コマンドは :! と同じく "|" を含めて行末までなので barNone にする。:! と同じく :g の中では使えない。
		{name: terminalPopupName, min: len(terminalPopupName), rng: rangeNone, bar: barNone, run: runTerminalPopup},
	}
}

// Name は、Ex コマンドの表の 1 行の名前である（Names）。
type Name struct {
	// Full は、省略しない名前（"write"）、Short は、省略できる最短の形（"w"）である。
	Full, Short string
}

// Names は、コマンドラインから呼べる本体の Ex コマンドの名前を返す（拡張機能の editor/list のコマンドの一覧。FR-107）。
// Ex コマンドの表の行を表の順に並べ、その後に別名の表 t の行（:set など。FR-133。設定ファイルの別名。FR-150）を表の順に並べる。
// t が nil なら、別名は本体の行だけである。
//
// req: FR-150
func Names(t *Aliases) []Name {
	rows := t.list()
	out := make([]Name, 0, len(commands)+len(rows))
	for _, c := range commands {
		out = append(out, Name{Full: c.name, Short: c.name[:c.min]})
	}
	for _, a := range rows {
		out = append(out, Name{Full: a.name, Short: a.name[:a.min]})
	}
	return out
}

// tabNewName は、空のタブを開く正規の Ex コマンドの名前である（FR-151）。
const tabNewName = "tab.new"

// runTabNew は :tab.new[!] [file] を実行する（FR-151）。
// file が無ければ、名前の無い空のバッファを新しいタブで開く ActionTabNew を返す。開くのは、Action を受け取る呼び出し側である。
// file があれば、:e file と同じ ActionEdit を返す（Vim の :tabnew file と同じく、ファイルを新しいタブで開く）。
// "!" は受け付ける（Vim 9.1 の :tabnew! と同じ）。file を開くのは新しいタブなので、未保存の変更を確かめない（:e file と同じ）。
//
// req: FR-151
func runTabNew(c *execCtx, inv *invocation) error {
	file, err := fileArg(inv)
	if err != nil {
		return err
	}
	if file != "" {
		c.actions = append(c.actions, Action{Kind: ActionEdit, File: file, Force: inv.bang})
		return nil
	}
	c.actions = append(c.actions, Action{Kind: ActionTabNew})
	return nil
}

// runPopup は、引数を取らないポップアップのコマンド（:popup、:tabopen）を実行する関数を返す。
// 縮めるタブと広げるポップアップを持つのは、Action を受け取る呼び出し側（internal/editor）である。
//
// req: FR-70
func runPopup(op PopupOp) func(c *execCtx, inv *invocation) error {
	return func(c *execCtx, inv *invocation) error {
		if err := noArg(inv); err != nil {
			return err
		}
		c.actions = append(c.actions, Action{Kind: ActionPopup, Popup: op})
		return nil
	}
}

// runDiffSplit は :diffs[plit] {file} を実行する。file が無ければ、Vim 9.1 と同じく E471 にする。
// 見比べを始めるのは、Action を受け取る呼び出し側（internal/editor）である。
func runDiffSplit(c *execCtx, inv *invocation) error {
	file, err := fileArg(inv)
	if err != nil {
		return err
	}
	if file == "" {
		return errAt(inv.argPos, codeArgRequired, "引数が必要です")
	}
	c.actions = append(c.actions, Action{Kind: ActionDiff, Diff: DiffSplit, File: file})
	return nil
}

// runDiff は、引数を取らない見比べのコマンド（:diffthis、:diffoff、:diffupdate）を実行する関数を返す。
// :diffoff! と :diffupdate! の ! は受け付けて無視する（見比べは 2 つのタブの組で、:diffoff はその組を終えるため）。
func runDiff(op DiffOp) func(c *execCtx, inv *invocation) error {
	return func(c *execCtx, inv *invocation) error {
		if err := noArg(inv); err != nil {
			return err
		}
		c.actions = append(c.actions, Action{Kind: ActionDiff, Diff: op})
		return nil
	}
}

// runAll は、:wa、:qa、:wqa、:xa を実行する関数を返す。write なら ActionWriteAll を、quit なら ActionQuitAll を返す。
// 書き出すのは未保存の変更のあるバッファだけで、:wqa と :xa は同じ動作になる（Vim 9.1 と同じ）。
func runAll(write, quit bool) func(c *execCtx, inv *invocation) error {
	// req: FR-08
	return func(c *execCtx, inv *invocation) error {
		if err := noArg(inv); err != nil {
			return err
		}
		if write {
			// :wqa と :xa は、閉じる client のタブだけを書き出す（基本設計 4.4。:wa は server のすべてのバッファ）。
			c.actions = append(c.actions, Action{Kind: ActionWriteAll, Force: inv.bang, ClientOnly: quit})
		}
		if quit {
			c.actions = append(c.actions, Action{Kind: ActionQuitAll, Force: inv.bang})
		}
		return nil
	}
}

// runBufferDelete は :bd[elete][!] [N|名前] を実行する。引数の読み方は :b と同じで、省くとカーソルのあるバッファを閉じる。
//
// req: FR-08
func runBufferDelete(c *execCtx, inv *invocation) error {
	if err := runBuffer(c, inv); err != nil {
		return err
	}
	a := &c.actions[len(c.actions)-1]
	a.Kind, a.Buffer = ActionBufferDelete, 0
	return nil
}

// runJumps は :jumps を実行する。一覧は、ジャンプリストを持つ呼び出し側が作る。
//
// req: FR-08
func runJumps(c *execCtx, inv *invocation) error {
	if err := noArg(inv); err != nil {
		return err
	}
	c.actions = append(c.actions, Action{Kind: ActionJumpList})
	return nil
}

// runRegisters は :registers を実行する。一覧の窓は、レジスターと画面を持つ呼び出し側が出す（FR-54）。
// Vim の :registers は引数に出すレジスターの名前を取るが、ここでは取らず、引数があれば E488 にする。
//
// req: FR-54
func runRegisters(c *execCtx, inv *invocation) error {
	if err := noArg(inv); err != nil {
		return err
	}
	c.actions = append(c.actions, Action{Kind: ActionRegisterList})
	return nil
}

// runBufferList は :ls と :buffers を実行する。一覧は、バッファを持つ呼び出し側が作る。
// "!" は Vim では一覧に載せないバッファも出す指定だが、一覧に載せないバッファは無いので、付けても同じ一覧を出す。
//
// req: FR-08
func runBufferList(c *execCtx, inv *invocation) error {
	if err := noArg(inv); err != nil {
		return err
	}
	c.actions = append(c.actions, Action{Kind: ActionBuffer, Buffer: BufferList})
	return nil
}

// runBuffer は :b[uffer][!] [N|名前] を実行する。引数が数字なら番号、そうでなければ名前として、ActionBuffer の BufferGo を返す。
// 番号の後に文字が続けば E488 にする（:b 1 2。Vim 9.1 と同じ）。
// 番号と名前からバッファを探すこと、未保存の変更の確認（"!" が無ければ E37）は、呼び出し側が行う。
// Vim の範囲で番号を書く形（:2b）は使えず、E481 にする。
//
// req: FR-08
func runBuffer(c *execCtx, inv *invocation) error {
	arg := strings.TrimSpace(inv.arg)
	a := Action{Kind: ActionBuffer, Buffer: BufferGo, Force: inv.bang}
	if arg != "" && isDigit(arg[0]) {
		s := &scanner{src: inv.arg, base: inv.argPos}
		s.skipBlanks()
		n, _ := s.number()
		s.skipBlanks()
		if !s.eof() {
			return errAt(s.pos(), codeTrailing, "余分な文字があります: %s", s.rest())
		}
		a.Count = n
	} else {
		a.Text = strings.ReplaceAll(arg, `\ `, " ")
	}
	c.actions = append(c.actions, a)
	return nil
}

// runBufferStep は、:bn[ext][!] [N] と :bp[revious][!] [N]（:bN[ext]）を実行する関数を返す。
// 回数は引数で書き、省くと 1 である。Vim の範囲で回数を書く形（:2bn）は使えず、E481 にする。
func runBufferStep(op BufferOp) func(c *execCtx, inv *invocation) error {
	// req: FR-08
	return func(c *execCtx, inv *invocation) error {
		n := 1
		if strings.TrimSpace(inv.arg) != "" {
			s := &scanner{src: inv.arg, base: inv.argPos}
			s.skipBlanks()
			v, ok := s.number()
			s.skipBlanks()
			if !ok || !s.eof() {
				return errAt(s.pos(), codeTrailing, "余分な文字があります: %s", s.rest())
			}
			if v <= 0 {
				return errAt(inv.argPos, codePositiveCount, "回数は 1 以上にします")
			}
			n = v
		}
		c.actions = append(c.actions, Action{Kind: ActionBuffer, Buffer: op, Count: n, Force: inv.bang})
		return nil
	}
}

// runCheckTime は :checktime を実行する。外での変更の確認は、ファイルの状態を持つ呼び出し側が行う（Vim の省略形 :checkt と同じく 6 文字から）。
//
// req: FR-08
func runCheckTime(c *execCtx, inv *invocation) error {
	if err := noArg(inv); err != nil {
		return err
	}
	c.actions = append(c.actions, Action{Kind: ActionCheckTime})
	return nil
}

// runReadOnly は :reado[nly] を実行する。バッファの編集の禁止を切り替え、切り替えた後の状態を通知で知らせる（FR-86）。
// Vim に無い本体のコマンドである（名前は仮。基本設計 4.6）。
// 禁止の状態はバッファが持つ（buffer.Buffer.SetReadOnly）ので、テキスト、アンドゥの履歴、保存済みの記録は変えない。
//
// req: FR-86
func runReadOnly(c *execCtx, inv *invocation) error {
	if err := noArg(inv); err != nil {
		return err
	}
	on := !c.b.ReadOnly()
	c.b.SetReadOnly(on)
	if on {
		c.notice = Notice{Text: "編集を禁止しました"}
	} else {
		c.notice = Notice{Text: "編集の禁止を解きました"}
	}
	return nil
}

// runOutput は :output [名前] を実行する。出力の欄を開き、名前があればその欄に切り替える（基本設計 6.7）。
// 名前の解決（欄があるか）は、欄を持つ呼び出し側が行う。
func runOutput(c *execCtx, inv *invocation) error {
	c.actions = append(c.actions, Action{Kind: ActionOutput, Output: OutputOpen, Text: strings.TrimSpace(inv.arg)})
	return nil
}

// runOutClose は :outclose を実行する。出力の欄を閉じる。
func runOutClose(c *execCtx, inv *invocation) error {
	if err := noArg(inv); err != nil {
		return err
	}
	c.actions = append(c.actions, Action{Kind: ActionOutput, Output: OutputClose})
	return nil
}

// lookup は、名前（省略形を含む）に当たるコマンドを返す。無ければ nil を返す。
//
// req: FR-08
func lookup(name string) *command {
	for i := range commands {
		cmd := &commands[i]
		if len(name) >= cmd.min && strings.HasPrefix(cmd.name, name) {
			return cmd
		}
	}
	return nil
}

// wholeRange は、:w、:wq、:x の範囲がバッファ全体でなければ E319 を返す。
// 範囲を省いたとき、:%w、:1,$w はバッファ全体である。Action は書き出す行の範囲を持たないので、一部の行（:2,3w）の書き出しは断る。
// 行数を超えた範囲（:5w）と逆順の範囲（:3,1w）は、ここに来る前に E16 と E493 にしている（Vim 9.1 と同じ）。
func (c *execCtx) wholeRange(inv *invocation) error {
	if inv.line1 != 0 || inv.line2 != c.b.LineCount()-1 {
		return errAt(inv.rangePos, codeNotAvailable, "一部の行の書き出しは使えません（範囲を省くか %% にします）")
	}
	return nil
}

// fileArg は、:w や :e の引数のファイル名を返す。"\ " は空白に戻す。
func fileArg(inv *invocation) (string, error) {
	arg := strings.TrimSpace(inv.arg)
	if strings.HasPrefix(arg, "!") || strings.HasPrefix(arg, ">>") {
		return "", errAt(inv.argPos, codeNotAvailable, "%s の !cmd と >> は使えません", inv.name)
	}
	return strings.ReplaceAll(arg, `\ `, " "), nil
}

// noArg は、引数を取らないコマンドに引数が続いていれば誤りを返す。
func noArg(inv *invocation) error {
	if strings.TrimSpace(inv.arg) != "" {
		return errAt(inv.argPos, codeTrailing, "余分な文字があります: %s", inv.arg)
	}
	return nil
}

// checkModified は、"!" が無く、未保存の変更があれば E37 を返す。
// 同じ Execute の中で、編集中のファイルへの書き出し（File が空の ActionWrite）を先に返していて、
// その後にバッファを変えていなければ、未保存の変更は無いものとする（:w|q）。
// 書き出しが失敗したら呼び出し側は残りの動作を捨てる約束（Result.Actions）なので、終了してしまうことは無い。
func (c *execCtx) checkModified(inv *invocation) error {
	if !inv.bang && c.b.Modified() && !c.writePending() {
		return errAt(inv.namePos, codeNoWrite, "最後の変更が保存されていません（! を付けると変更を捨てます）")
	}
	return nil
}

// writePending は、編集中のファイルへの書き出しを、現在の版のまま返しているかを返す。
func (c *execCtx) writePending() bool {
	return c.wroteVersion != nil && *c.wroteVersion == c.b.Version()
}

// addWrite は、ActionWrite を加える。編集中のファイルへの書き出しなら、その時点の版を覚える。
func (c *execCtx) addWrite(file string, force bool) {
	c.actions = append(c.actions, Action{Kind: ActionWrite, File: file, Force: force})
	if file == "" {
		v := c.b.Version()
		c.wroteVersion = &v
	}
}

// req: FR-08
func runWrite(c *execCtx, inv *invocation) error {
	if err := c.wholeRange(inv); err != nil {
		return err
	}
	file, err := fileArg(inv)
	if err != nil {
		return err
	}
	c.addWrite(file, inv.bang)
	return nil
}

// req: FR-08
func runWriteQuit(c *execCtx, inv *invocation) error {
	if err := c.wholeRange(inv); err != nil {
		return err
	}
	file, err := fileArg(inv)
	if err != nil {
		return err
	}
	c.addWrite(file, inv.bang)
	c.actions = append(c.actions, Action{Kind: ActionQuit, Force: inv.bang})
	return nil
}

// runXit は :x を実行する。変更があるか、ファイル名を指定したときだけ書き出す。
//
// req: FR-08
func runXit(c *execCtx, inv *invocation) error {
	if err := c.wholeRange(inv); err != nil {
		return err
	}
	file, err := fileArg(inv)
	if err != nil {
		return err
	}
	if (c.b.Modified() && !c.writePending()) || file != "" {
		c.addWrite(file, inv.bang)
	}
	c.actions = append(c.actions, Action{Kind: ActionQuit, Force: inv.bang})
	return nil
}

// req: FR-08
func runQuit(c *execCtx, inv *invocation) error {
	if err := noArg(inv); err != nil {
		return err
	}
	if err := c.checkModified(inv); err != nil {
		return err
	}
	c.actions = append(c.actions, Action{Kind: ActionQuit, Force: inv.bang})
	return nil
}

// req: FR-08
func runEdit(c *execCtx, inv *invocation) error {
	file, err := fileArg(inv)
	if err != nil {
		return err
	}
	if file == "" {
		// 段階 3 では :e {file} は新しいタブで開き、元のタブを残すので（FR-113）、未保存の変更を確かめるのは読み直しだけである。
		if err := c.checkModified(inv); err != nil {
			return err
		}
	}
	c.actions = append(c.actions, Action{Kind: ActionEdit, File: file, Force: inv.bang})
	return nil
}

// runTabMove は :tabm[ove] [N|+N|-N|$] を実行する（Vim 9.1 と同じ書き方）。
// 引数が無いか $ なら末尾へ、N なら N 番目のタブの後ろへ（0 なら先頭へ）、+N と -N なら N だけ右と左へ動かす。
// + と - だけなら 1 とする。並びの外を指すかは、タブを持つ呼び出し側が確かめる（Vim 9.1 は E475）。
//
// req: FR-65
func runTabMove(c *execCtx, inv *invocation) error {
	arg := strings.TrimSpace(inv.arg)
	a := Action{Kind: ActionTabMove, Count: -1, Text: arg}
	if arg != "" && arg != "$" {
		s := &scanner{src: inv.arg, base: inv.argPos}
		s.skipBlanks()
		sign := 0
		switch s.peek() {
		case '+':
			sign = 1
			s.next()
		case '-':
			sign = -1
			s.next()
		}
		n, ok := s.number()
		s.skipBlanks()
		switch {
		case !s.eof() || (!ok && sign == 0):
			return errAt(inv.argPos, codeInvalidArgument, "無効な引数です: %s", arg)
		case sign != 0 && ok && n == 0:
			// Vim 9.1 は :tabmove -0 と :tabmove +0 を E475 にする。
			return errAt(inv.argPos, codeInvalidArgument, "無効な引数です: %s", arg)
		case !ok:
			n = 1
		}
		a.Count = n
		if sign != 0 {
			a.Count, a.Relative = sign*n, true
		}
	}
	c.actions = append(c.actions, a)
	return nil
}

// applyCount は、引数の回数 count を範囲に適用する。Vim と同じく、範囲の最後の行から count 行を対象にし、最後の行で止める。
func (c *execCtx) applyCount(inv *invocation, count int, pos int) error {
	if count <= 0 {
		return errAt(pos, codeInvalidRange, "回数は 1 以上にします")
	}
	inv.line1 = inv.line2
	inv.line2 = min(inv.line1+count-1, c.b.LineCount()-1)
	return nil
}

// runDelete は :d [x] [count] を実行する。消した行は Deletion として返す。
// x は書き込めるレジスターの名前（英字と _。FR-50）で、それ以外は E488 にする（:d ! は Vim 9.1 と同じ。:d - と :d " は Vim では書ける）。
// カーソルは、消した範囲の次の行（最後の行を消したときは新しい最後の行）の最初の空白でない文字へ移る。
//
// req: FR-08
func runDelete(c *execCtx, inv *invocation) error {
	s := &scanner{src: inv.arg, base: inv.argPos}
	var reg rune
	if ch := s.peek(); !s.eof() && !isDigit(ch) {
		if !isWritableRegister(ch) {
			return errAt(s.pos(), codeTrailing, "余分な文字があります: %s", inv.arg)
		}
		reg = rune(ch)
		s.next()
		s.skipBlanks()
	}
	countPos := s.pos()
	if n, ok := s.number(); ok {
		if err := c.applyCount(inv, n, countPos); err != nil {
			return err
		}
	}
	s.skipBlanks()
	if !s.eof() {
		return errAt(s.pos(), codeTrailing, "余分な文字があります: %s", s.rest())
	}

	// Vim 9.1 の ex_operators と同じく、ジャンプとして記録する（:g の中では記録しない）。
	c.markJump()
	if c.b.NoLines() {
		// Vim 9.1 と同じく、行の無いバッファの :d は、レジスターを変えずに何もせず終える（dd と同じ。command の opDelete）。
		return nil
	}
	l1, l2 := inv.line1, inv.line2
	all := l1 == 0 && l2 == c.b.LineCount()-1
	lines := make([]string, 0, l2-l1+1)
	for l := l1; l <= l2; l++ {
		lines = append(lines, c.b.Line(l))
	}
	at := buffer.Position{Line: l1, Byte: firstNonBlankByte(c.b.Line(l1))}
	if err := c.apply(deleteLinesEdit(c.b, l1, l2)); err != nil {
		return err
	}
	// Vim の ex_operators は、カーソルを範囲の最初の行の最初の空白でない文字へ移してからアンドゥの単位を作る。
	// アンドゥとリドゥの後のカーソルはその位置へ戻る。:g の中では、最初の :d の位置が残る。
	c.b.SetUndoCursor(at)
	if all {
		// Vim と同じく、全ての行を消したバッファは行の無いバッファにし、空のファイルとして書き出す（基本設計 4.1）。
		c.b.MarkNoLines()
	}
	c.replaced = append(c.replaced, lineReplace{start: l1, old: l2 - l1 + 1, new: 0})
	c.deletions = append(c.deletions, Deletion{Register: reg, Lines: lines})
	c.setCursor(min(l1, c.b.LineCount()-1), CursorFirstNonBlank)
	return nil
}

// firstNonBlankByte は、行の最初の空白でない文字の位置を返す。
// 空白だけの行では最後の文字、空の行では 0 を返す（Vim の beginline(BL_SOL | BL_FIX) と同じ）。
func firstNonBlankByte(line string) int {
	i := len(line) - len(strings.TrimLeft(line, " \t"))
	if i == len(line) && i > 0 {
		return i - 1
	}
	return i
}

// isWritableRegister は、ch が :d の書き込み先にできるレジスターの名前（英字と _）かを返す（FR-50）。
// 数字は回数として読むので、ここには来ない。
func isWritableRegister(ch byte) bool {
	return isLetter(ch) || ch == '_'
}

// deleteLinesEdit は、l1 行目から l2 行目まで（0 始まり、両端を含む）を行ごと消す編集を返す。
// 全ての行を消すと、空の行が 1 つ残る（buffer の行の数え方では、行は 1 つ以上ある）。
func deleteLinesEdit(b *buffer.Buffer, l1, l2 int) buffer.Edit {
	last := b.LineCount() - 1
	var r buffer.Range
	switch {
	case l2 < last:
		r = buffer.Range{Start: buffer.Position{Line: l1}, End: buffer.Position{Line: l2 + 1}}
	case l1 > 0:
		r = buffer.Range{
			Start: buffer.Position{Line: l1 - 1, Byte: len(b.Line(l1 - 1))},
			End:   buffer.Position{Line: l2, Byte: len(b.Line(l2))},
		}
	default:
		r = buffer.Range{End: buffer.Position{Line: l2, Byte: len(b.Line(l2))}}
	}
	return buffer.Edit{Range: r}
}
