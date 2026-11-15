package command

import (
	"strings"
	"unicode"

	"github.com/piplupflipper/modal-editor/internal/buffer"
	"github.com/piplupflipper/modal-editor/internal/command/target"
	"github.com/piplupflipper/modal-editor/internal/editcmd"
)

// region は、オペレーターが働く範囲である。
type region struct {
	// start は、範囲の始まりとして扱うカーソルの位置である。y や g~ の後のカーソルはここへ移る。
	// 行単位の範囲でも列を持つ（yk の後は、k で移った列に置く）。
	// Vim がオペレーターを働かせる直前のカーソル（do_pending_operator の oap->start）に当たり、
	// アンドゥの単位に、変更の前のカーソルとして記録する（markUndoCursor）。
	start buffer.Position
	// r は、半開区間の範囲である。行単位の範囲では、始まりの行の先頭から終わりの行の行末までである。
	r        buffer.Range
	linewise bool
	// empty は、排他的（exclusive）な範囲で、始まりと終わりが同じであることを表す（Vim の oap->empty）。
	// 包含的（inclusive）な範囲は、空の行で r が空になっても empty ではない（空の行の c$ は空のテキストをレジスターに書く）。
	empty bool
}

// regionOf は、Span を region にする。
func (s *State) regionOf(sp target.Span) region {
	return region{
		start: sp.Start, r: sp.Range(s.buf), linewise: sp.Kind == target.Linewise,
		empty: sp.Kind == target.Exclusive && sp.Start == sp.End,
	}
}

// opOptions は、オペレーターの実行の条件である。
type opOptions struct {
	// visual は、ビジュアルモードの選択に対する実行であることを表す。d の行単位の例外を適用しない。
	visual bool
	// forceOne は、削除を 1 行の中でもレジスター 1 に入れることを表す（%、{、} の削除）。
	forceOne bool
	// times は、> と < で字下げを繰り返す回数である（ビジュアルモードの 3>）。
	times int
}

// operatorFunc は、本体のオペレーター 1 つの実装である。
type operatorFunc func(s *State, cmd editcmd.Command, rg region, o opOptions) error

// operators は、本体のオペレーターの表である。キーは input の表の ID である。
// 一覧は issue #9 の案で、裁定で増減しうるので、行の足し引きで済むようにしておく。
//
// req: FR-02
var operators = map[string]operatorFunc{
	"delete":      opDelete,
	"change":      opChange,
	"yank":        opYank,
	"indent":      opShift(1),
	"dedent":      opShift(-1),
	"toggle-case": opCase(toggleCase),
	"lowercase":   opCase(unicode.ToLower),
	"uppercase":   opCase(unicode.ToUpper),
}

// forceOneMotions は、1 行の中の削除でもレジスター 1 に入れるモーションである（Vim の `:help quote1`）。
var forceOneMotions = map[string]bool{"match-pair": true, "paragraph-prev": true, "paragraph-next": true}

// moveMotion は、モーションだけの移動を実行する。
func (s *State) moveMotion(cmd editcmd.Command) error {
	if cmd.Target.Kind != editcmd.TargetMotion {
		return failf("text object without operator")
	}
	res, err := s.resolveMotion(cmd.Target, cmd, target.NoOperator)
	if err != nil {
		return err
	}
	s.cursor = s.clampNormal(s.clampPos(res.Pos))
	s.want = res.Want
	return nil
}

// operator は、ノーマルモードのオペレーターと対象の組を実行する。
//
// req: FR-02
func (s *State) operator(cmd editcmd.Command) error {
	sp, err := s.operatorSpan(cmd)
	if err != nil {
		return err
	}
	o := opOptions{times: 1}
	if cmd.Target.Kind == editcmd.TargetMotion && cmd.Target.Ref.Builtin() {
		o.forceOne = forceOneMotions[cmd.Target.Ref.ID]
	}
	return s.runOperator(cmd, sp, s.regionOf(sp), o)
}

// isYank は、本体の y であるかを返す。
func isYank(ref editcmd.Ref) bool { return ref.Builtin() && ref.ID == "yank" }

// operatorSpan は、オペレーターの対象を範囲に解決する。
func (s *State) operatorSpan(cmd editcmd.Command) (target.Span, error) {
	switch cmd.Target.Kind {
	case editcmd.TargetLine:
		// dd は d と (count-1)j の組に当たる。最後の行で回数が 2 以上なら j が失敗するので、全体が失敗する。
		n := max(cmd.Count, 1)
		if n > 1 && s.cursor.Line == s.lastLine() {
			return target.Span{}, failf("count exceeds the last line")
		}
		end := min(s.cursor.Line+n-1, s.lastLine())
		start := s.cursor
		if fnb := s.firstNonBlankPos(start.Line); n == 1 && !isYank(cmd.Operator) && fnb.Less(start) {
			// Vim の nv_lineop は、回数が 1 なら y 以外でカーソルを最初の空白でない文字へ移してから働く（g~~ の後のカーソル）。
			// 範囲の始まりは、動かす前と後のカーソルの前の方である（do_pending_operator）。0g~~ のカーソルは行頭に残る。
			start = fnb
		}
		return target.Span{Start: start, End: pos(end, 0), Kind: target.Linewise}, nil
	case editcmd.TargetMotion:
		op := target.AnyOperator
		if cmd.Operator.Builtin() && cmd.Operator.ID == "change" {
			op = target.ChangeOperator
		}
		res, err := s.resolveMotion(cmd.Target, cmd, op)
		if err != nil {
			return target.Span{}, err
		}
		return target.OperatorSpan(s.buf, s.cursor, res), nil
	case editcmd.TargetTextObject:
		sp, err := s.resolveTextObject(cmd.Target, cmd, s.cursor)
		if err != nil {
			return target.Span{}, err
		}
		return target.AdjustExclusive(s.buf, sp), nil
	case editcmd.TargetNone:
	}
	return target.Span{}, failf("operator without target")
}

// runOperator は、範囲に対してオペレーターを実行する。拡張機能のオペレーターなら問い合わせる（FR-12）。
func (s *State) runOperator(cmd editcmd.Command, sp target.Span, rg region, o opOptions) error {
	// Vim は、カーソルを範囲の始まりへ移してからオペレーターを働かせる（do_pending_operator）。
	// その位置を、アンドゥの単位に変更の前のカーソルとして記録する。
	s.markUndoCursor(rg.start)
	defer s.clearUndoCursor()
	if !cmd.Operator.Builtin() {
		return s.extOperator(cmd, sp)
	}
	f, ok := operators[cmd.Operator.ID]
	if !ok {
		return failf("unknown operator %q", cmd.Operator.ID)
	}
	return f(s, cmd, rg, o)
}

// extOperator は、拡張機能のオペレーターを問い合わせ、返った編集を適用する。
func (s *State) extOperator(cmd editcmd.Command, sp target.Span) error {
	if s.Extensions == nil {
		return ErrNoExtensions
	}
	tx, err := s.Extensions.Operator(cmd.Operator, s.buf, OperatorQuery{
		Span: sp, Cursor: s.cursor, Count: cmd.Count, Char: cmd.Char, Register: cmd.Register,
	}, s.queryInfo())
	if err != nil {
		return err
	}
	if err := s.applyTx(tx); err != nil {
		return err
	}
	if cmd.Mode == editcmd.ModeInsert {
		s.cursor = s.clampPos(sp.Start)
		s.startInsert(cmd, 1)
		return nil
	}
	s.setCursorNormal(sp.Start)
	return nil
}

// blank は、文字列が空白（スペースとタブ）だけからなるかを返す。
func blank(str string) bool { return strings.Trim(str, " \t") == "" }

// cut は、範囲のテキストをレジスターの内容にし、範囲を消す編集を返す。
//
// 行単位の範囲は、行の後の改行ごと消す。最後の行を含む場合は、前の行の後の改行を消す。
// バッファの全ての行を消す場合は、空の行 1 つを残す。
func (s *State) cut(rg region) (Register, buffer.Edit, error) {
	text, err := s.buf.Slice(rg.r)
	if err != nil {
		return Register{}, buffer.Edit{}, err
	}
	reg := Register{Text: text, Linewise: rg.linewise}
	if !rg.linewise {
		return reg, buffer.Edit{Range: rg.r}, nil
	}
	l1, l2 := rg.r.Start.Line, rg.r.End.Line
	switch {
	case l2 < s.lastLine():
		return reg, buffer.Edit{Range: buffer.Range{Start: pos(l1, 0), End: pos(l2+1, 0)}}, nil
	case l1 > 0:
		return reg, buffer.Edit{Range: buffer.Range{Start: s.lineEnd(l1 - 1), End: s.lineEnd(l2)}}, nil
	}
	return reg, buffer.Edit{Range: rg.r}, nil
}

// smallDelete は、範囲が 1 行の中の文字単位の範囲であるかを返す（レジスター - に入る削除）。
func smallDelete(rg region) bool {
	return !rg.linewise && rg.r.Start.Line == rg.r.End.Line
}

// opDelete は、d である。
//
// `:help d` の例外に合わせ、ノーマルモードで、文字単位の範囲が 2 行以上にわたり、
// 始まりの前が空白だけで、終わりの後に空白でない文字が無い場合は、行単位の削除にする。
func opDelete(s *State, cmd editcmd.Command, rg region, o opOptions) error {
	if !writable(cmd.Register) {
		return failf("invalid register %q", cmd.Register)
	}
	if !o.visual && !rg.linewise && rg.r.Start.Line != rg.r.End.Line &&
		blank(s.line(rg.r.Start.Line)[:rg.r.Start.Byte]) && blank(s.line(rg.r.End.Line)[rg.r.End.Byte:]) {
		rg.linewise = true
		rg.r = buffer.Range{Start: pos(rg.r.Start.Line, 0), End: s.lineEnd(rg.r.End.Line)}
	}
	if rg.r.Start == rg.r.End && !rg.linewise {
		return nil
	}
	if rg.linewise && s.noLines() {
		// Vim は、全ての行を消したバッファ（--No lines in buffer--）の行単位の削除を、レジスターを変えずに何もせず終える。
		return nil
	}
	all := rg.linewise && rg.r.Start.Line == 0 && rg.r.End.Line == s.lastLine()
	reg, edit, err := s.cut(rg)
	if err != nil {
		return err
	}
	if err := s.apply(edit); err != nil {
		return err
	}
	if all {
		// 行の無いバッファは、書き出すと空のファイルになる（基本設計 4.1）。
		s.buf.MarkNoLines()
	}
	s.registers.delete(cmd.Register, reg, smallDelete(rg), o.forceOne)
	if rg.linewise {
		s.setCursorNormal(s.firstNonBlankPos(min(rg.r.Start.Line, s.lastLine())))
		return nil
	}
	s.setCursorNormal(rg.r.Start)
	return nil
}

// opChange は、c である。範囲を消してインサートモードへ移る。
// 行単位の範囲は、行の内容を消して空の行を 1 つ残す（'autoindent' がオフなので字下げも残さない）。
// cw が ce のように動く例外（`:help cw`）は、モーションの解決（target.ChangeOperator）が扱う。
func opChange(s *State, cmd editcmd.Command, rg region, _ opOptions) error {
	if !writable(cmd.Register) {
		return failf("invalid register %q", cmd.Register)
	}
	text, err := s.buf.Slice(rg.r)
	if err != nil {
		return err
	}
	// 行の無いバッファの cc（S）と c$（C）は、Vim と同じくレジスターを変えずにインサートモードへ移る（opDelete の dd と同じ扱い）。
	noLines := s.noLines()
	if rg.linewise && rg.r.End.Line > rg.r.Start.Line && s.undoAtSet {
		// Vim の op_change は、2 行目以降を消すときに、カーソルを一時的に次の行へ移してアンドゥの単位を作る（op_delete）。
		// アンドゥの後のカーソルは、その位置（cj の取り消しなら 2 行目）へ戻る。
		s.markUndoCursor(pos(s.undoAt.Line+1, s.undoAt.Byte))
	}
	if err := s.apply(buffer.Edit{Range: rg.r}); err != nil {
		return err
	}
	if !rg.empty && !noLines {
		// Vim の op_change は、範囲が空でなければ（空の行の c$、ciw のように包含的な範囲なら、テキストが空でも）、
		// 消したテキストをレジスターに書く。cl のような排他的で空の範囲では書かない。
		s.registers.delete(cmd.Register, Register{Text: text, Linewise: rg.linewise}, smallDelete(rg), false)
	}
	s.cursor = rg.r.Start
	s.updateWant()
	s.startInsert(cmd, 1)
	return nil
}

// opYank は、y である。カーソルは範囲の始まりへ移る（yy では動かない）。
func opYank(s *State, cmd editcmd.Command, rg region, _ opOptions) error {
	if !writable(cmd.Register) {
		return failf("invalid register %q", cmd.Register)
	}
	text, err := s.buf.Slice(rg.r)
	if err != nil {
		return err
	}
	s.registers.yank(cmd.Register, Register{Text: text, Linewise: rg.linewise})
	s.setCursorNormal(rg.start)
	return nil
}

// 字下げの幅。Vim の既定の 'shiftwidth' と 'tabstop' は 8 で、'expandtab' はオフである。
const (
	shiftWidth = 8
	tabStop    = 8
)

// opShift は、> と < である。範囲の行の字下げを 'shiftwidth' の幅だけ増やすか減らす。
// > は空の行を字下げしない。字下げはタブで作り、端数をスペースで埋める（'expandtab' がオフ）。
// カーソルは最初の行の、最初の空白でない文字へ移る。
func opShift(dir int) operatorFunc {
	return func(s *State, _ editcmd.Command, rg region, o opOptions) error {
		times := max(o.times, 1)
		var edits []buffer.Edit
		for l := rg.r.Start.Line; l <= rg.r.End.Line; l++ {
			line := s.line(l)
			if dir > 0 && line == "" {
				continue
			}
			n := len(line) - len(strings.TrimLeft(line, " \t"))
			w := indentWidth(line[:n])
			nw := max(w+dir*shiftWidth*times, 0)
			indent := strings.Repeat("\t", nw/tabStop) + strings.Repeat(" ", nw%tabStop)
			if indent != line[:n] {
				edits = append(edits, buffer.Edit{Range: buffer.Range{Start: pos(l, 0), End: pos(l, n)}, Text: indent})
			}
		}
		if err := s.apply(edits...); err != nil {
			return err
		}
		s.setCursorNormal(s.firstNonBlankPos(rg.r.Start.Line))
		return nil
	}
}

// indentWidth は、字下げの表示の幅を返す。タブは次の 'tabstop' の倍数まで進める。
func indentWidth(indent string) int {
	w := 0
	for _, c := range indent {
		if c == '\t' {
			w += tabStop - w%tabStop
		} else {
			w++
		}
	}
	return w
}

// toggleCase は、大文字を小文字に、小文字を大文字にする。
func toggleCase(r rune) rune {
	switch {
	case unicode.IsUpper(r):
		return unicode.ToLower(r)
	case unicode.IsLower(r):
		return unicode.ToUpper(r)
	}
	return r
}

// mapCase は、文字列の文字を 1 つずつ f で変える。
func mapCase(str string, f func(rune) rune) string { return strings.Map(f, str) }

// opCase は、g~、gu、gU（ビジュアルモードの ~、u、U）である。カーソルは範囲の始まりへ移る。
func opCase(f func(rune) rune) operatorFunc {
	return func(s *State, _ editcmd.Command, rg region, _ opOptions) error {
		text, err := s.buf.Slice(rg.r)
		if err != nil {
			return err
		}
		if t := mapCase(text, f); t != text {
			if err := s.apply(buffer.Edit{Range: rg.r, Text: t}); err != nil {
				return err
			}
		}
		s.setCursorNormal(rg.start)
		return nil
	}
}

// noLines は、バッファが Vim の「行が無い」バッファ（buffer.Buffer の NoLines）かどうかを返す。
// 行単位の削除で全ての行を消したとき（opDelete、:d）と、空のファイルを開いたときに当たる。
func (s *State) noLines() bool {
	return s.buf.NoLines()
}
