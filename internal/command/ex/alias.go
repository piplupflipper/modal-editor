package ex

import (
	"errors"
	"fmt"
	"slices"
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
	// 設定ファイルの別名（FR-150）では、拡張機能のコマンドの ID でもよい。そのときの扱いは拡張機能のコマンドと同じである（runExtCommand）。
	target string
	// expand は、変換を持つ項目の変換である。引数を読み、正規のコマンドの呼び出しを aliasCtx.call で 1 つずつ行う。
	// 範囲と "!" は取らない（付ければ E481 と E477）。"|" の扱いは bar で決める。
	expand func(x *aliasCtx, inv *invocation) error
	bar    barKind
	// complete は、変換を持つ項目の、コマンドラインの補完で引数の位置に何を補うかである（Complete）。
	// 名前を付け替えるだけの項目は、付け替えた先のコマンドのものを使う。
	complete argKind
}

// builtinAliases は、本体に書いた Ex コマンドの別名の行である。どの別名の表（Aliases）も、この行から始まる。
// 変換が Execute を経由して表を参照するので、初期化の循環を避けるために init で作る（commands と同じ）。
var builtinAliases []alias

func init() {
	builtinAliases = []alias{
		// :se[t] は、Vim の :set の書き方の一部を :option.set と :option.get の呼び出しに変換する（expandSet）。
		// 省略形は Vim と同じく :se までである（:s は :substitute）。
		{name: "set", min: 2, expand: expandSet, bar: barSplit, complete: argSet},
		// :noh[lsearch] は :search.clearHighlight に名前を付け替えるだけの別名である（FR-138）。省略形は Vim と同じく :noh までである。
		{name: "nohlsearch", min: 3, target: clearHighlightName},
	}
}

// Aliases は、Ex コマンドの別名の表である（FR-133、FR-150。基本設計 7.3）。
// 本体に書いた行（:set、:nohlsearch）の後に、設定ファイルの別名（名前を付け替えるだけの行）を足した順に並べる。
// 名前は表の順に引き、最初に当たった行を使う。本体の行と設定ファイルの行は、同じ表で同じ規則で引く。
//
// nil の *Aliases は、本体の行だけの表として使える（State.Aliases が nil のとき）。
// 表は呼び出し側（internal/editor）が起動のときに 1 つ作り、その後は変えない。
type Aliases struct {
	rows []alias
}

// NewAliases は、本体の行だけを持つ別名の表を作る。設定ファイルの別名は Add で足す。
func NewAliases() *Aliases {
	return &Aliases{rows: slices.Clone(builtinAliases)}
}

// list は、表の行を返す。nil なら本体の行である。
func (t *Aliases) list() []alias {
	if t == nil {
		return builtinAliases
	}
	return t.rows
}

// RenameAlias は、名前を付け替えるだけの別名の 1 行である（設定ファイルの commandAliases の 1 件。FR-150）。
type RenameAlias struct {
	// Name は、省略しない名前（"tabedit"）である。英字だけで書く。
	Name string
	// Min は、省略できる最短の長さ（"tabe" まで省略できるなら 4）である。1 から Name の長さまでで、Name の長さなら省略できない。
	Min int
	// Target は、付け替える先の、本体の Ex コマンドの省略しない名前（"edit"、"tab.new"）か、拡張機能のコマンドの ID（"finder.files"）である。
	Target string
}

// Add は、名前を付け替えるだけの別名 a を、表の最後に足す（FR-150）。足せなければ、表を変えずに誤りを返す。
// extCommands は、コマンドラインから呼べる拡張機能のコマンドの ID（State.ExtCommand が引ける ID）である。
//
// 次のときは足さない。
//
//   - 名前が英字だけでない（コマンドラインは英字の列をコマンドの名前として読むため。readName）
//   - 省略できる最短の長さが 1 より小さいか、名前より長い
//   - 名前か省略形が、本体の Ex コマンド、表の別名（本体の行と、先に足した行）と重なる。
//     2 つの行が重なるのは、どちらの行にも当たる名前がある（共通の接頭辞の長さが、2 つの行の最短の長さの大きい方以上の）ときである
//   - 名前か省略形が、拡張機能のコマンドの ID と同じ（別名が先に当たり、そのコマンドを呼べなくなるため）
//   - 付け替える先が、本体の Ex コマンドの省略しない名前でも、拡張機能のコマンドの ID でもない（省略形、別名、知らない名前）
//
// req: FR-150
func (t *Aliases) Add(a RenameAlias, extCommands []string) error {
	// 名前の全体が、コマンドラインと同じ読み方（scanner.letters）で 1 つの名前として読めることを確かめる。
	if a.Name == "" || len(a.Name) != len((&scanner{src: a.Name}).letters()) {
		return fmt.Errorf("別名の名前 %q は英字だけで書く", a.Name)
	}
	if a.Min < 1 || a.Min > len(a.Name) {
		return fmt.Errorf("省略できる最短の長さ %d は 1 から名前の長さ %d までにする", a.Min, len(a.Name))
	}
	overlap := func(name string, minLen int) (string, bool) {
		n := max(a.Min, minLen)
		if commonPrefixLen(a.Name, name) < n {
			return "", false
		}
		return a.Name[:n], true
	}
	for _, c := range commands {
		if s, ok := overlap(c.name, c.min); ok {
			return fmt.Errorf("名前か省略形 %s が本体の Ex コマンド :%s と重なる", s, c.name)
		}
	}
	for _, r := range t.rows {
		if s, ok := overlap(r.name, r.min); ok {
			return fmt.Errorf("名前か省略形 %s が別名 :%s と重なる", s, r.name)
		}
	}
	for _, id := range extCommands {
		if len(id) >= a.Min && strings.HasPrefix(a.Name, id) {
			return fmt.Errorf("名前か省略形 %s が拡張機能のコマンド :%s と同じなので、そのコマンドを呼べなくなる", id, id)
		}
	}
	switch cmd := lookup(a.Target); {
	case cmd != nil && cmd.name == a.Target:
	case slices.Contains(extCommands, a.Target):
	case cmd != nil:
		return fmt.Errorf("付け替える先 %s は省略しない名前（%s）で書く", a.Target, cmd.name)
	case t.lookup(a.Target) != nil:
		return fmt.Errorf("付け替える先 %s は別名なので、付け替える先にできない（正規のコマンドの名前を書く）", a.Target)
	default:
		return fmt.Errorf("付け替える先 %s は、本体の Ex コマンドの名前でも拡張機能のコマンドの ID でもない", a.Target)
	}
	t.rows = append(t.rows, alias{name: a.Name, min: a.Min, target: a.Target})
	return nil
}

// Added は、Add で足した行を、足した順に返す（:help の節。internal/editor）。
func (t *Aliases) Added() []RenameAlias {
	rows := t.list()[len(builtinAliases):]
	out := make([]RenameAlias, len(rows))
	for i, r := range rows {
		out[i] = RenameAlias{Name: r.name, Min: r.min, Target: r.target}
	}
	return out
}

// commonPrefixLen は、a と b の共通の接頭辞のバイト数を返す。
func commonPrefixLen(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

// lookup は、名前（省略形を含む）に当たる別名を、表の順に引いて返す。無ければ nil を返す。
//
// req: FR-133, FR-150
func (t *Aliases) lookup(name string) *alias {
	rows := t.list()
	for i := range rows {
		a := &rows[i]
		if len(name) >= a.min && strings.HasPrefix(a.name, name) {
			return a
		}
	}
	return nil
}

// resolve は、名前（省略形を含む）に当たるコマンドを返す。Ex コマンドの表を先に引き、無ければ別名の表 t を引く。
// 名前を付け替える別名は、付け替えた先の正規のコマンドを返す。付け替えた先が拡張機能のコマンドなら nil を返す（runExtCommand が引く）。
// 変換を持つ別名は、範囲と "!" を取らず、変換を実行するコマンドの行を作って返す。どちらにも無ければ nil を返す。
//
// req: FR-133, FR-150
func (t *Aliases) resolve(name string) *command {
	if cmd := lookup(name); cmd != nil {
		return cmd
	}
	a := t.lookup(name)
	switch {
	case a == nil:
		return nil
	case a.target != "":
		return lookup(a.target)
	}
	return &command{name: a.name, min: a.min, rng: rangeNone, bar: a.bar, complete: a.complete, run: func(c *execCtx, inv *invocation) error {
		return a.expand(&aliasCtx{c: c}, inv)
	}}
}

// resolve は、名前に当たるコマンドを、State の別名の表（State.Aliases）で引く（Aliases.resolve）。
func (c *execCtx) resolve(name string) *command {
	return c.st.Aliases.resolve(name)
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

// vimOptionNames は、:set で受け付ける Vim の名前から、設定の正規の名前への対応である（FR-133）。
// 短い名前（Vim の ft など）は受け付けない。ただし字下げの 4 項目は、:set et sw=4 ts=4 の形で書くことが多いので、
// #318 の決めで短い名前（et、sw、ts、ai）も受け付ける。Vim の 'softtabstop'（sts）と 'smarttab'（sta）は、別の設定にしないので受け付けない。
// アンドゥの履歴を残す設定も、#319 の決めで短い名前（udf）を受け付ける。
// コメントの形（#327）も、issue の決めで短い名前（cms）を受け付ける。
// カーソルの上下と左右の余白（#337）も、issue の決めで短い名前（so、siso）を受け付ける。
var vimOptionNames = map[string]string{
	"filetype":      option.FileType,
	"number":        option.LineNumberShow,
	"numberwidth":   option.LineNumberMinWidth,
	"hlsearch":      option.SearchHighlight,
	"wrap":          option.LineWrapEnabled,
	"list":          option.WhitespaceShow,
	"scrolloff":     option.ScrollMargin,
	"so":            option.ScrollMargin,
	"sidescrolloff": option.ScrollSideMargin,
	"siso":          option.ScrollSideMargin,
	"expandtab":     option.IndentUseSpaces,
	"et":            option.IndentUseSpaces,
	"shiftwidth":    option.IndentWidth,
	"sw":            option.IndentWidth,
	"tabstop":       option.TabWidth,
	"ts":            option.TabWidth,
	"autoindent":    option.IndentAuto,
	"ai":            option.IndentAuto,
	"undofile":      option.UndoPersist,
	"udf":           option.UndoPersist,
	"commentstring": option.CommentString,
	"cms":           option.CommentString,
	// コマンドラインの補完（#350）は、オーナーの nvim の設定が長い名前で書いているので、長い名前だけを受け付ける。
	"wildmode":       option.CommandLineCompleteMode,
	"wildignorecase": option.CommandLineCompleteIgnoreCase,
	"pumheight":      option.CompletionMenuHeight,
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
//	name!       option.toggle <正規の名前>。真偽でない項目は、option.toggle が E488 にする
//	name=value  option.set <正規の名前> value
//	name?       option.get <正規の名前>
//
// 知らない名前（inv の接頭辞、+=、-=、^=、&、短い名前を含む）は E518 にし、引数が無ければ E471 にする。
// 途中の引数が誤りなら、そこで止め、前の引数の設定は残す（Vim 9.1 の :set ts=4 foo ts=8 と同じ）。
//
// 引数の中の \ は、次の 1 文字をそのまま値に入れ、\ を除く（:set cms=//\ %s は "// %s"、\\ は \）。
// \ の後の空白は引数を区切らない。末尾の \ は値に残す。Vim 9.1 と Neovim 0.12.5 の :set の文字列の値と同じである（2026-11-21 に確かめた）。
// ただし値の末尾の空白は、:option.set が除くので残らない（:set cms=%s\  は "%s"。Vim は "%s " にする）。
//
// req: FR-133, FR-155
func expandSet(x *aliasCtx, inv *invocation) error {
	s := &scanner{src: inv.arg, base: inv.argPos}
	s.skipBlanks()
	if s.eof() {
		return errAt(inv.argPos, codeArgRequired, "引数が必要です")
	}
	for !s.eof() {
		pos, start := s.pos(), s.i
		for !s.eof() && s.peek() != ' ' && s.peek() != '\t' {
			if s.peek() == '\\' {
				s.next()
				if s.eof() {
					break
				}
			}
			s.next()
		}
		line, err := setCall(inv.arg[start:s.i], pos)
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

// unescapeSetValue は、:set の値の \ を除き、\ の次の 1 文字をそのまま残す（expandSet の doc）。
// 末尾の \ は、続く文字が無いのでそのまま残す（:set cms=%s\ は "%s\"、:set cms=\ は "\" で E537。
// Vim 9.1 と Neovim 0.12.5 と同じ。2026-11-21 に確かめた。PR #331 のレビューの推奨 3）。
func unescapeSetValue(value string) string {
	if !strings.Contains(value, `\`) {
		return value
	}
	var sb strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] == '\\' && i+1 < len(value) {
			i++
		}
		sb.WriteByte(value[i])
	}
	return sb.String()
}

// setCall は、:set の引数 1 つ arg を、正規のコマンドの呼び出しの文字列に変換する。pos は arg の位置である。
func setCall(arg string, pos int) (string, error) {
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
		// 反転は正規のコマンド :option.toggle に任せる（FR-155）。真偽でない項目の E488 も :option.toggle が出す。
		d, ok := vimOption(strings.TrimSuffix(arg, "!"))
		if !ok {
			return unknown()
		}
		return optionToggleName + " " + d.Name, nil
	case strings.Contains(arg, "="):
		name, value, _ := strings.Cut(arg, "=")
		d, ok := vimOption(name)
		if !ok {
			return unknown()
		}
		return set(d, unescapeSetValue(value)), nil
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
