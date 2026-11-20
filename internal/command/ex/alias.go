package ex

import (
	"errors"
	"strings"

	"github.com/piplupflipper/vi-imitation/internal/option"
)

// alias は、Ex コマンドの別名の表の 1 行である（FR-133。基本設計 7.3）。
// 別名は、受け取ったコマンドラインを正規のコマンド（commands の表の行）の呼び出しに変換する。
// 別名の表は Ex コマンドの表（commands）と別に置き、名前を引くときは commands の後に引く（resolve）。
//
// 行は、名前を付け替えるだけの項目（target）と、変換を持つ項目（expand）のどちらかである。
type alias struct {
	name string // 省略しない名前
	min  int    // 省略できる最短の長さ
	// target は、名前を付け替えるだけの項目の、正規のコマンドの名前である。
	// 範囲、"!"、引数、"|" の扱いは、正規のコマンドの表の行のものをそのまま使う。
	target string
	// expand は、変換を持つ項目の変換である。引数を読み、正規のコマンドの呼び出しを aliasCtx.call で 1 つずつ行う。
	// 範囲と "!" は取らない（付ければ E481 と E477）。"|" の扱いは bar で決める。
	expand func(x *aliasCtx, inv *invocation) error
	bar    barKind
}

// aliases は、Ex コマンドの別名の表である。
// 変換が Execute を経由して表を参照するので、初期化の循環を避けるために init で作る（commands と同じ）。
var aliases []alias

func init() {
	aliases = []alias{
		// :se[t] は、Vim の :set の書き方の一部を :option.set と :option.get の呼び出しに変換する（expandSet）。
		// 省略形は Vim と同じく :se までである（:s は :substitute）。
		{name: "set", min: 2, expand: expandSet, bar: barSplit},
	}
}

// lookupAlias は、名前（省略形を含む）に当たる別名を返す。無ければ nil を返す。
func lookupAlias(name string) *alias {
	for i := range aliases {
		a := &aliases[i]
		if len(name) >= a.min && strings.HasPrefix(a.name, name) {
			return a
		}
	}
	return nil
}

// resolve は、名前（省略形を含む）に当たるコマンドを返す。Ex コマンドの表を先に引き、無ければ別名の表を引く。
// 名前を付け替える別名は、付け替えた先の正規のコマンドを返す。
// 変換を持つ別名は、範囲と "!" を取らず、変換を実行するコマンドの行を作って返す。どちらにも無ければ nil を返す。
//
// req: FR-133
func resolve(name string) *command {
	if cmd := lookup(name); cmd != nil {
		return cmd
	}
	a := lookupAlias(name)
	switch {
	case a == nil:
		return nil
	case a.target != "":
		return lookup(a.target)
	}
	return &command{name: a.name, min: a.min, rng: rangeNone, bar: a.bar, run: func(c *execCtx, inv *invocation) error {
		return a.expand(&aliasCtx{c: c}, inv)
	}}
}

// aliasCtx は、変換を持つ別名が、正規のコマンドを呼ぶための口である。
type aliasCtx struct {
	c *execCtx
}

// call は、正規のコマンドの呼び出し line（"option.set filetype go"）を、同じ Execute の中で実行する。
// 誤りの位置は、変換した後の文字列の中ではなく、元のコマンドラインの pos（変換の元にした引数の位置）にする。
func (x *aliasCtx) call(line string, pos int) error {
	err := x.c.runLine(line, pos)
	if e := (*Error)(nil); errors.As(err, &e) {
		e.Pos = pos
	}
	return err
}

// get は、:option.get に当たる読み出しで、正規の名前 name の項目の今の値を返す（反転の変換に使う）。
func (x *aliasCtx) get(name string, pos int) (option.Value, error) {
	v, _, err := x.c.getOption(name, pos, pos)
	return v, err
}

// vimOptionNames は、:set で受け付ける Vim の名前から、設定の正規の名前への対応である（FR-133）。
// 短い名前（Vim の ft など）は受け付けない。
var vimOptionNames = map[string]string{
	"filetype":    option.FileType,
	"number":      option.LineNumberShow,
	"numberwidth": option.LineNumberMinWidth,
}

// vimOption は、:set の Vim の名前 name の項目を返す。無ければ ok は false である。
func vimOption(name string) (option.Def, bool) {
	canon, ok := vimOptionNames[name]
	if !ok {
		return option.Def{}, false
	}
	return optionLookup(canon)
}

// expandSet は、:se[t] {引数} … を変換する（FR-133。基本設計 7.3）。空白で区切った引数を左から 1 つずつ変換して実行する。
//
//	name        真偽の項目なら option.set <正規の名前> true。それ以外の項目なら option.get <正規の名前>（Vim と同じく値を出す）
//	noname      真偽の項目なら option.set <正規の名前> false。それ以外の項目なら E474
//	name!       真偽の項目なら、今の値を読み、反対の値で option.set。それ以外の項目なら E488
//	name=value  option.set <正規の名前> value
//	name?       option.get <正規の名前>
//
// 知らない名前（inv の接頭辞、+=、-=、^=、&、短い名前を含む）は E518 にし、引数が無ければ E471 にする。
// 途中の引数が誤りなら、そこで止め、前の引数の設定は残す（Vim 9.1 の :set ts=4 foo ts=8 と同じ）。
//
// req: FR-133
func expandSet(x *aliasCtx, inv *invocation) error {
	s := &scanner{src: inv.arg, base: inv.argPos}
	s.skipBlanks()
	if s.eof() {
		return errAt(inv.argPos, codeArgRequired, "引数が必要です")
	}
	for !s.eof() {
		pos, start := s.pos(), s.i
		for !s.eof() && s.peek() != ' ' && s.peek() != '\t' {
			s.next()
		}
		line, err := setCall(x, inv.arg[start:s.i], pos)
		if err != nil {
			return err
		}
		if err := x.call(line, pos); err != nil {
			return err
		}
		s.skipBlanks()
	}
	return nil
}

// setCall は、:set の引数 1 つ arg を、正規のコマンドの呼び出しの文字列に変換する。pos は arg の位置である。
func setCall(x *aliasCtx, arg string, pos int) (string, error) {
	unknown := func() (string, error) {
		return "", errAt(pos, codeUnknownOption, "知らない設定です: %s", arg)
	}
	set := func(d option.Def, value string) string { return optionSetName + " " + d.Name + " " + value }
	switch {
	case strings.HasSuffix(arg, "?"):
		d, ok := vimOption(strings.TrimSuffix(arg, "?"))
		if !ok {
			return unknown()
		}
		return optionGetName + " " + d.Name, nil
	case strings.HasSuffix(arg, "!"):
		d, ok := vimOption(strings.TrimSuffix(arg, "!"))
		if !ok {
			return unknown()
		}
		if d.Type != option.Bool {
			return "", errAt(pos, codeTrailing, "真偽の設定ではないので反転できません: %s", arg)
		}
		v, err := x.get(d.Name, pos)
		if err != nil {
			return "", err
		}
		return set(d, option.BoolValue(!v.Bool()).String()), nil
	case strings.Contains(arg, "="):
		name, value, _ := strings.Cut(arg, "=")
		d, ok := vimOption(name)
		if !ok {
			return unknown()
		}
		return set(d, value), nil
	}
	if d, ok := vimOption(arg); ok {
		if d.Type != option.Bool {
			return optionGetName + " " + d.Name, nil
		}
		return set(d, "true"), nil
	}
	if name, ok := strings.CutPrefix(arg, "no"); ok {
		if d, ok := vimOption(name); ok {
			if d.Type != option.Bool {
				return "", errAt(pos, codeInvalidArg, "真偽の設定ではないので no を付けられません: %s", arg)
			}
			return set(d, "false"), nil
		}
	}
	return unknown()
}
