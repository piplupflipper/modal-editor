package command

import (
	"github.com/piplupflipper/modal-editor/internal/buffer"
	"github.com/piplupflipper/modal-editor/internal/command/target"
	"github.com/piplupflipper/modal-editor/internal/editcmd"
)

// visualState は、ビジュアルモードの状態である。
type visualState struct {
	// anchor は、選択の動かない側の端である。もう一方の端はカーソルである。
	anchor buffer.Position
	// start と end は、最後に抜けたビジュアルモードの選択の始まりと終わりの位置（マーク '< と '>）である。
	start, end buffer.Position
	marked     bool
}

// visualExtent は、ドットリピートでビジュアルモードのオペレーターを繰り返すための、選択の大きさである（Vim と同じ）。
// 行単位なら行の数、1 行の文字単位なら文字の数、複数の行の文字単位なら行の数と終わりの位置のバイト数を記録する。
type visualExtent struct {
	linewise bool
	lines    int
	chars    int
	endByte  int
	eol      bool // 終わりの行の改行まで選んでいた
}

// Selection は、ビジュアルモードの選択の始まりと終わり（どちらも含む）と、行単位かどうかを返す。
// ビジュアルモードでなければ ok が false である。
// 端は行末の後（Byte が行の長さ）にあることがあり、そのときはその行の改行を選んでいることを表す（visualCursor）。
func (s *State) Selection() (start, end buffer.Position, linewise, ok bool) {
	if !s.mode.visual() {
		return buffer.Position{}, buffer.Position{}, false, false
	}
	start, end = s.visual.anchor, s.cursor
	if end.Less(start) {
		start, end = end, start
	}
	return start, end, s.mode == ModeVisualLine, true
}

// actVisual は、v と V である。ノーマルモードではビジュアルモードへ入り、
// ビジュアルモードでは、同じ種類なら抜け、違う種類なら切り替える（選択の端はそのまま）。
func actVisual(m Mode) actionFunc {
	return func(s *State, _ editcmd.Command) error {
		switch {
		case s.mode == m:
			s.exitVisual()
		case s.mode.visual():
			s.mode = m
		default:
			s.mode = m
			s.visual.anchor = s.cursor
			s.parser.SetVisual(true)
		}
		return nil
	}
}

// actVisualSwap は、ビジュアルモードの o である。カーソルを選択の反対の端へ移す。
func actVisualSwap(s *State, _ editcmd.Command) error {
	if !s.mode.visual() {
		return failf("not in visual mode")
	}
	s.visual.anchor, s.cursor = s.cursor, s.visual.anchor
	s.updateWant()
	return nil
}

// exitVisual は、ビジュアルモードを抜けてノーマルモードへ戻る。選択の端をマーク '< と '> に記録する。
func (s *State) exitVisual() {
	if start, end, _, ok := s.Selection(); ok {
		s.visual.start, s.visual.end, s.visual.marked = start, end, true
	}
	s.mode = ModeNormal
	s.parser.SetVisual(false)
	s.setCursorNormal(s.cursor)
}

// mark は、Ex のアドレスのマーク（'< と '>）を行に解決する。
func (s *State) mark(name rune) (int, bool) {
	if !s.visual.marked {
		return 0, false
	}
	switch name {
	case '<':
		return min(s.visual.start.Line, s.lastLine()), true
	case '>':
		return min(s.visual.end.Line, s.lastLine()), true
	}
	return 0, false
}

// visualMotion は、ビジュアルモードのモーションとテキストオブジェクトで、選択を動かす。
func (s *State) visualMotion(cmd editcmd.Command) error {
	if cmd.Target.Kind == editcmd.TargetTextObject {
		return s.visualTextObject(cmd)
	}
	if cmd.Target.Kind == editcmd.TargetMotion && cmd.Target.Ref.Builtin() && cmd.Target.Ref.ID == "right" {
		return s.visualRight(cmd)
	}
	return s.moveMotion(cmd)
}

// visualRight は、ビジュアルモードの l である。Vim と同じく、最後の文字から行末の後（NUL の位置）まで進める
// （Vim の nv_right() は、'selection' が old でないビジュアルモードで行末の後へ移る）。
// 行末の後（空の行の先頭を含む）にいれば失敗する。ab|c と d の vld は、c と改行を消して abd にする。
func (s *State) visualRight(cmd editcmd.Command) error {
	if s.cursor.Byte >= len(s.line(s.cursor.Line)) {
		return failf("%v", target.ErrNoTarget)
	}
	// オペレーターの後の l と同じく、行末の位置まで進める。
	res, err := s.resolveMotion(cmd.Target, cmd, target.AnyOperator)
	if err != nil {
		return err
	}
	s.cursor = s.clampPos(res.Pos)
	s.want = res.Want
	return nil
}

// visualTextObject は、ビジュアルモードのテキストオブジェクトである。
// 選択が 1 文字（端とカーソルが同じ）なら、選択をテキストオブジェクトの範囲にする。
// そうでなければ、カーソルの次の文字から始まるテキストオブジェクトの終わりまで選択を広げる。
// 行単位のテキストオブジェクト（ip）は、文字単位のビジュアルモードを行単位に切り替える。
//
// Vim は、選択を広げる方向（カーソルが端より前か）や、i( を重ねたときの外側の括弧への広げ方を細かく扱うが、
// ここでは前へ広げることだけを扱う。
func (s *State) visualTextObject(cmd editcmd.Command) error {
	from := s.cursor
	extend := s.cursor != s.visual.anchor
	if extend {
		line := s.line(from.Line)
		switch {
		case nextChar(line, from.Byte) < len(line):
			from.Byte = nextChar(line, from.Byte)
		case from.Line < s.lastLine():
			from = pos(from.Line+1, 0)
		default:
			return failf("no more text")
		}
	}
	sp, err := s.resolveTextObject(cmd.Target, cmd, from)
	if err != nil {
		return err
	}
	end := sp.End
	if sp.Kind == target.Exclusive && end != sp.Start {
		if end.Byte > 0 {
			end.Byte = prevChar(s.line(end.Line), end.Byte)
		} else if end.Line > 0 {
			end = s.lineEnd(end.Line - 1)
		}
	}
	if !extend {
		s.visual.anchor = sp.Start
	}
	if sp.Kind == target.Linewise && s.mode == ModeVisual {
		s.mode = ModeVisualLine
	}
	s.setCursorNormal(end)
	return nil
}

// visualRegion は、ビジュアルモードの選択を、オペレーターが働く範囲にする。
// 文字単位の選択の終わりが行末（空の行か、$ で動いた行）なら、その後の改行も含める（Vim の 'selection' が inclusive）。
func (s *State) visualRegion() region {
	start, end, linewise, _ := s.Selection()
	if linewise {
		// Vim は、行単位の選択の端（VIsual）が始まりなら列を 0 にし、カーソルが始まりならカーソルの列を残す（do_pending_operator）。
		// y の後のカーソルと、アンドゥの後のカーソルがこの位置になる。
		// カーソルが行末の後（NUL の位置）にあれば、その列を残す（vkC のアンドゥの後のカーソル。#86）。
		at := pos(start.Line, 0)
		if s.cursor.Less(s.visual.anchor) {
			at = s.cursor
		}
		return region{
			start:    at,
			r:        buffer.Range{Start: pos(start.Line, 0), End: s.lineEnd(end.Line)},
			linewise: true,
		}
	}
	// 終わりが行末の後（空の行の先頭を含む）なら、その行の改行まで選ぶ。始まりが行末の後なら、その行の改行から選ぶ。
	eol := end.Byte >= len(s.line(end.Line))
	return s.charRegion(start, end, eol)
}

// visualCursor は、モーションで移った後のビジュアルモードのカーソルを、Vim が置く位置で返す。
// Vim は、'selection' が inclusive のビジュアルモードで、望みの列が行の幅に届かない行（j や k で移った短い行、$ の後）では、
// カーソルを最後の文字ではなく行末の後（NUL の位置）へ置く（coladvance()）。ここでも同じ位置へ置き、
// 選択の端（Selection）とオペレーターの範囲を Vim に合わせる（#89。a と bcd|e の vkd は a を残す）。
// 行末の後から h で戻る列（vkh）も、この位置から数えるので Vim と同じになる。
// l で行末の後へ移るのは visualRight が扱う。ビジュアルモードを抜けると、カーソルは最後の文字へ戻る（exitVisual）。
func (s *State) visualCursor() buffer.Position {
	line := s.line(s.cursor.Line)
	if line != "" && s.cursor.Byte == lastCharStart(line) &&
		(s.want == target.WantEOL || s.want >= s.columns().Column(line, len(line))) {
		return pos(s.cursor.Line, len(line))
	}
	return s.cursor
}

// charRegion は、start から end の文字まで（eol なら end の行の改行まで）の文字単位の範囲を返す。
func (s *State) charRegion(start, end buffer.Position, eol bool) region {
	stop := pos(end.Line, nextChar(s.line(end.Line), end.Byte))
	if eol && end.Line < s.lastLine() {
		stop = pos(end.Line+1, 0)
	}
	return region{start: start, r: buffer.Range{Start: start, End: stop}}
}

// extentOf は、選択の大きさを記録する。
func (s *State) extentOf(rg region) visualExtent {
	ex := visualExtent{linewise: rg.linewise, lines: rg.r.End.Line - rg.r.Start.Line + 1}
	if rg.linewise {
		return ex
	}
	start, end, _, _ := s.Selection()
	ex.lines = end.Line - start.Line + 1
	ex.eol = rg.r.End.Line > end.Line
	if ex.lines == 1 {
		ex.chars = runeCount(s.line(start.Line)[start.Byte:nextChar(s.line(end.Line), end.Byte)])
	} else {
		ex.endByte = end.Byte
	}
	return ex
}

// regionFromExtent は、ドットリピートのために、カーソルから記録した大きさの範囲を作る。
// 行や文字が足りなければ、バッファの終わりや行末で止める。
func (s *State) regionFromExtent(ex visualExtent) region {
	start := s.cursor
	lastL := min(start.Line+ex.lines-1, s.lastLine())
	if ex.linewise {
		return region{start: start, r: buffer.Range{Start: pos(start.Line, 0), End: s.lineEnd(lastL)}, linewise: true}
	}
	var end buffer.Position
	if ex.lines == 1 {
		line := s.line(start.Line)
		b, _ := advanceChars(line, start.Byte, max(ex.chars-1, 0))
		end = s.clampNormal(pos(start.Line, b))
	} else {
		end = s.clampNormal(s.clampPos(pos(lastL, ex.endByte)))
	}
	return s.charRegion(start, end, ex.eol || s.line(end.Line) == "")
}

// linewiseVisualOperators は、ビジュアルモードだけのオペレーターのうち、選択を行単位にして働くもの（D、C、S、Y）と、
// その働きに当たる本体のオペレーターの対応である。キーは input の表の ID である。
var linewiseVisualOperators = map[string]string{
	"delete-lines": "delete",
	"change-lines": "change",
	"yank-lines":   "yank",
}

// visualOperator は、ビジュアルモードの選択に対してオペレーターを実行し、ノーマルモードへ戻る。
// 回数は、> と < では字下げを繰り返す回数で、それ以外のオペレーターでは使わない。
// D、C、S、Y（linewiseVisualOperators）は、選択を行単位に切り替えてから働く。
//
// req: FR-01, FR-02
func (s *State) visualOperator(cmd editcmd.Command) error {
	if base, ok := linewiseVisualOperators[cmd.Operator.ID]; ok && cmd.Operator.Builtin() {
		// D、C、S、Y は、選択を行単位に切り替えてから、もとのオペレーターとして働く（Vim の nv_visop は
		// VIsual_mode を 'V' にしてから d、c、y を実行する）。ドットリピートも、行単位の選択に対するもとのオペレーターとして繰り返す。
		s.mode = ModeVisualLine
		cmd.Operator.ID = base
	}
	rg := s.visualRegion()
	ext := s.extentOf(rg)
	s.exitVisual()
	s.dotVisual = ext
	return s.change(cmd, func() error { return s.regionOperator(cmd, rg) })
}

// regionOperator は、ビジュアルモードの選択（またはその繰り返し）の範囲に対してオペレーターを実行する。
func (s *State) regionOperator(cmd editcmd.Command, rg region) error {
	sp := target.Span{Start: rg.r.Start, End: rg.r.End, Kind: target.Exclusive}
	if rg.linewise {
		sp.Kind = target.Linewise
	}
	s.cursor = rg.start
	return s.runOperator(cmd, sp, rg, opOptions{visual: true, times: max(cmd.Count, 1)})
}
