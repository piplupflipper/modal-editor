package ex

import (
	"errors"
	"strings"

	"github.com/piplupflipper/vi-imitation/internal/option"
)

// Options は、設定の項目（option の表）の値を読み書きする口である（:option.set、:option.get と、その別名の :set。FR-132）。
// 呼び出し側（internal/editor）が、Execute を呼ぶバッファ（カーソルのあるバッファ）について作り、State.Options に入れる。
// 範囲がバッファごとの項目はそのバッファの値を、範囲がエディタ全体の項目はエディタに 1 つの値を読み書きする。
// どちらの値を読み書きするかは d.Scope で決め、項目の名前では分けない。
type Options interface {
	// Get は、項目 d の今の値を返す。
	Get(d option.Def) option.Value
	// Set は、項目 d の値を v にする。v の型は d.Type と同じである。
	Set(d option.Def, v option.Value) error
}

// 設定の Ex コマンドの名前。点を含む正規の名前で、省略できない（拡張機能のコマンドの ID と同じ）。
const (
	optionSetName = "option.set"
	optionGetName = "option.get"
)

// optionLookup は、設定の表を引く関数である。テストが、表に無い型の項目を足すために差し替える。
var optionLookup = option.Lookup

// lookupOption は、正規の名前 name の項目を返す。無ければ E518 の誤りを返す。pos は誤りの位置である。
func lookupOption(name string, pos int) (option.Def, error) {
	d, ok := optionLookup(name)
	if !ok {
		return option.Def{}, errAt(pos, codeUnknownOption, "知らない設定です: %s", name)
	}
	return d, nil
}

// optionsOf は、設定の値を読み書きする口を返す。口が無ければ（State.Options が nil）E319 の誤りを返す。
func (c *execCtx) optionsOf(pos int) (Options, error) {
	if c.st.Options == nil {
		return nil, errAt(pos, codeNotAvailable, "設定を読み書きできません")
	}
	return c.st.Options, nil
}

// splitName は、引数 arg の先頭の空白までを名前として、名前と、名前の後の空白を除いた残りと、その位置を返す。
// pos は arg の先頭の位置である。
func splitName(arg string, pos int) (name string, rest string, restPos int) {
	s := &scanner{src: arg, base: pos}
	s.skipBlanks()
	start := s.i
	for !s.eof() && s.peek() != ' ' && s.peek() != '\t' {
		s.next()
	}
	name = arg[start:s.i]
	s.skipBlanks()
	return name, s.rest(), s.pos()
}

// runOptionSet は :option.set <名前> <値> を実行する（FR-132）。表で名前を引き、値を項目の型に合わせて読んで設定する。
// 真偽は true と false、整数は 10 進、文字列は名前の後の空白の後の行末まで（"|" を含む。空も許す）である。
// 値の末尾の空白は、型によらず除いて読む（末尾に空白を打っても E474 や E521 にならず、文字列の値にも入らない。
// #264 の本文の「空白の後の行末まで」との違いで、今の項目に末尾の空白を入れたい値は無い）。
// 知らない名前は E518、真偽でない値は E474、整数でない値は E521 にする（Vim 9.1 の :set と同じ番号）。名前が無ければ E471 にする。
// 整数の項目の範囲（option.Def.Range）の外の値は、下限より小さければ E487、上限より大きければ E474 にする
// （Vim 9.1 の :set numberwidth=0 と :set numberwidth=21 と同じ番号。項目の名前では分けない）。
//
// req: FR-131, FR-132, FR-136
func runOptionSet(c *execCtx, inv *invocation) error {
	name, text, textPos := splitName(inv.arg, inv.argPos)
	if name == "" {
		return errAt(inv.argPos, codeArgRequired, "引数が必要です")
	}
	text = strings.TrimRight(text, " \t")
	d, err := lookupOption(name, inv.argPos)
	if err != nil {
		return err
	}
	opts, err := c.optionsOf(inv.namePos)
	if err != nil {
		return err
	}
	v, err := d.Parse(text)
	switch {
	case errors.Is(err, option.ErrInvalidBool):
		return errAt(textPos, codeInvalidArg, "%s は true か false にします: %s", d.Name, text)
	case errors.Is(err, option.ErrNumberRequired):
		return errAt(textPos, codeNumberRequired, "%s は整数にします: %s", d.Name, text)
	case errors.Is(err, option.ErrBelowMin) && d.Range != nil:
		return errAt(textPos, codeNotPositive, "%s は %d 以上にします: %s", d.Name, d.Range.Min, text)
	case errors.Is(err, option.ErrAboveMax) && d.Range != nil:
		return errAt(textPos, codeInvalidArg, "%s は %d 以下にします: %s", d.Name, d.Range.Max, text)
	case err != nil:
		return errAt(textPos, codeInvalidArg, "%s の値を読めません: %s", d.Name, text)
	}
	return opts.Set(d, v)
}

// runOptionGet は :option.get <名前> を実行する（FR-132）。今の値を "<名前>=<値>" の形で通知に出す。
// 同じコマンドラインで続けて読んだとき（:set filetype? filetype?）は、前の通知の後に空白で区切って足す。
// 知らない名前は E518、名前の後に文字が続けば E488、名前が無ければ E471 にする。
//
// req: FR-131, FR-132
func runOptionGet(c *execCtx, inv *invocation) error {
	name, rest, restPos := splitName(inv.arg, inv.argPos)
	if name == "" {
		return errAt(inv.argPos, codeArgRequired, "引数が必要です")
	}
	if rest != "" {
		return errAt(restPos, codeTrailing, "余分な文字があります: %s", rest)
	}
	v, d, err := c.getOption(name, inv.argPos, inv.namePos)
	if err != nil {
		return err
	}
	text := d.Name + "=" + v.String()
	if c.notice.Text != "" && c.notice.Text == c.optionText {
		text = c.notice.Text + " " + text
	}
	c.notice = Notice{Text: text}
	c.optionText = text
	return nil
}

// getOption は、正規の名前 name の項目の今の値を読む（:option.get と、別名の中の読み出し）。
// namePos と cmdPos は、知らない名前と、読み書きの口が無いときの誤りの位置である。
func (c *execCtx) getOption(name string, namePos, cmdPos int) (option.Value, option.Def, error) {
	d, err := lookupOption(name, namePos)
	if err != nil {
		return option.Value{}, d, err
	}
	opts, err := c.optionsOf(cmdPos)
	if err != nil {
		return option.Value{}, d, err
	}
	return opts.Get(d), d, nil
}
