package ex

import (
	"strings"

	"github.com/piplupflipper/modal-editor/internal/buffer"
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
	bar      barKind
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
		{name: "write", min: 1, bang: true, rng: rangeWhole, run: runWrite},
		{name: "wq", min: 2, bang: true, rng: rangeWhole, run: runWriteQuit},
		{name: "xit", min: 1, bang: true, rng: rangeWhole, run: runXit},
		{name: "quit", min: 1, bang: true, rng: rangeWindow, run: runQuit},
		{name: "edit", min: 1, bang: true, rng: rangeNone, run: runEdit},
		{name: "substitute", min: 1, rng: rangeCurrent, inGlobal: true, bar: barSelf, run: runSubstitute},
		{name: "global", min: 1, bang: true, rng: rangeWhole, inGlobal: true, bar: barNone, run: runGlobal},
		{name: "vglobal", min: 1, rng: rangeWhole, inGlobal: true, bar: barNone, run: runGlobal},
		{name: "delete", min: 1, rng: rangeCurrent, inGlobal: true, run: runDelete},
		{name: "output", min: 3, rng: rangeNone, run: runOutput},
		{name: "outclose", min: 4, rng: rangeNone, run: runOutClose},
	}
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
	if err := c.checkModified(inv); err != nil {
		return err
	}
	c.actions = append(c.actions, Action{Kind: ActionEdit, File: file, Force: inv.bang})
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
// x は書き込めるレジスターの名前（英字、"、-、_、*、+）で、それ以外は E488 にする（:d ! など。Vim 9.1 と同じ）。
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

// isWritableRegister は、ch が :d の書き込み先にできるレジスターの名前かを返す（Vim の valid_yank_reg の書き込みの場合）。
// 数字は回数として読むので、ここには来ない。
func isWritableRegister(ch byte) bool {
	return isLetter(ch) || strings.IndexByte(`"-_*+`, ch) >= 0
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
