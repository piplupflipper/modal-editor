package command

import (
	"github.com/piplupflipper/modal-editor/internal/buffer"
	"github.com/piplupflipper/modal-editor/internal/command/target"
	"github.com/piplupflipper/modal-editor/internal/input"
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
	return func(s *State, _ input.Command) error {
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
func actVisualSwap(s *State, _ input.Command) error {
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
func (s *State) visualMotion(cmd input.Command) error {
	if cmd.Target.Kind == input.TargetTextObject {
		return s.visualTextObject(cmd)
	}
	return s.moveMotion(cmd)
}

// visualTextObject は、ビジュアルモードのテキストオブジェクトである。
// 選択が 1 文字（端とカーソルが同じ）なら、選択をテキストオブジェクトの範囲にする。
// そうでなければ、カーソルの次の文字から始まるテキストオブジェクトの終わりまで選択を広げる。
// 行単位のテキストオブジェクト（ip）は、文字単位のビジュアルモードを行単位に切り替える。
//
// Vim は、選択を広げる方向（カーソルが端より前か）や、i( を重ねたときの外側の括弧への広げ方を細かく扱うが、
// ここでは前へ広げることだけを扱う。
func (s *State) visualTextObject(cmd input.Command) error {
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
		return region{
			start:    pos(start.Line, 0),
			r:        buffer.Range{Start: pos(start.Line, 0), End: s.lineEnd(end.Line)},
			linewise: true,
		}
	}
	eol := s.line(end.Line) == "" || (end == s.cursor && s.want == target.WantEOL)
	return s.charRegion(start, end, eol)
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

// visualOperator は、ビジュアルモードの選択に対してオペレーターを実行し、ノーマルモードへ戻る。
// 回数は、> と < では字下げを繰り返す回数で、それ以外のオペレーターでは使わない。
//
// req: FR-01
func (s *State) visualOperator(cmd input.Command) error {
	rg := s.visualRegion()
	ext := s.extentOf(rg)
	s.exitVisual()
	s.dotVisual = ext
	return s.change(cmd, func() error { return s.regionOperator(cmd, rg) })
}

// regionOperator は、ビジュアルモードの選択（またはその繰り返し）の範囲に対してオペレーターを実行する。
func (s *State) regionOperator(cmd input.Command, rg region) error {
	sp := target.Span{Start: rg.r.Start, End: rg.r.End, Kind: target.Exclusive}
	if rg.linewise {
		sp.Kind = target.Linewise
	}
	s.cursor = rg.start
	return s.runOperator(cmd, sp, rg, opOptions{visual: true, times: max(cmd.Count, 1)})
}
