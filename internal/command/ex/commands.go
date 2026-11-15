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
)

// command は、Ex コマンドの表の 1 行である。
type command struct {
	name     string // 省略しない名前
	min      int    // 省略できる最短の長さ（"substitute" は "s" まで省略できるので 1）
	bang     bool   // "!" を付けられるか
	rng      rangeKind
	inGlobal bool // :g の中で実行できるか（:g 自身は、入れ子を E147 で断るために true にする）
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
	arg          string // コマンド名と "!" の後の、先頭の空白を除いた引数
	argPos       int
}

// commands は、対応する Ex コマンドの表である。
// 対象の一覧が裁定で増減したときは、この表の行を足し引きする。
// 表の関数が Execute を経由して表を参照するので、初期化の循環を避けるために init で作る。
var commands []command

func init() {
	commands = []command{
		{name: "write", min: 1, bang: true, rng: rangeNone, run: runWrite},
		{name: "wq", min: 2, bang: true, rng: rangeNone, run: runWriteQuit},
		{name: "xit", min: 1, bang: true, rng: rangeNone, run: runXit},
		{name: "quit", min: 1, bang: true, rng: rangeNone, run: runQuit},
		{name: "edit", min: 1, bang: true, rng: rangeNone, run: runEdit},
		{name: "substitute", min: 1, rng: rangeCurrent, inGlobal: true, run: runSubstitute},
		{name: "global", min: 1, bang: true, rng: rangeWhole, inGlobal: true, run: runGlobal},
		{name: "vglobal", min: 1, rng: rangeWhole, inGlobal: true, run: runGlobal},
		{name: "delete", min: 1, rng: rangeCurrent, inGlobal: true, run: runDelete},
	}
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
func (c *execCtx) checkModified(inv *invocation) error {
	if !inv.bang && c.b.Modified() {
		return errAt(inv.namePos, codeNoWrite, "最後の変更が保存されていません（! を付けると変更を捨てます）")
	}
	return nil
}

// req: FR-08
func runWrite(c *execCtx, inv *invocation) error {
	file, err := fileArg(inv)
	if err != nil {
		return err
	}
	c.actions = append(c.actions, Action{Kind: ActionWrite, File: file, Force: inv.bang})
	return nil
}

// req: FR-08
func runWriteQuit(c *execCtx, inv *invocation) error {
	file, err := fileArg(inv)
	if err != nil {
		return err
	}
	c.actions = append(c.actions,
		Action{Kind: ActionWrite, File: file, Force: inv.bang},
		Action{Kind: ActionQuit, Force: inv.bang})
	return nil
}

// runXit は :x を実行する。変更があるか、ファイル名を指定したときだけ書き出す。
//
// req: FR-08
func runXit(c *execCtx, inv *invocation) error {
	file, err := fileArg(inv)
	if err != nil {
		return err
	}
	if c.b.Modified() || file != "" {
		c.actions = append(c.actions, Action{Kind: ActionWrite, File: file, Force: inv.bang})
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
//
// req: FR-08
func runDelete(c *execCtx, inv *invocation) error {
	s := &scanner{src: inv.arg, base: inv.argPos}
	var reg rune
	if ch := s.peek(); !s.eof() && !isDigit(ch) {
		if ch >= 0x80 || ch == ' ' || ch == '\t' {
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

	l1, l2 := inv.line1, inv.line2
	lines := make([]string, 0, l2-l1+1)
	for l := l1; l <= l2; l++ {
		lines = append(lines, c.b.Line(l))
	}
	if err := c.apply(deleteLinesEdit(c.b, l1, l2)); err != nil {
		return err
	}
	c.replaced = append(c.replaced, lineReplace{start: l1, old: l2 - l1 + 1, new: 0})
	c.deletions = append(c.deletions, Deletion{Register: reg, Lines: lines})
	c.cursor = min(l1, c.b.LineCount()-1)
	return nil
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
