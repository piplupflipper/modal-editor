package command

import (
	"strings"
	"unicode/utf8"

	"github.com/piplupflipper/modal-editor/internal/buffer"
	"github.com/piplupflipper/modal-editor/internal/command/ex"
	"github.com/piplupflipper/modal-editor/internal/editcmd"
)

// actionFunc は、本体の対象を取らない操作 1 つの実装である。
type actionFunc func(s *State, cmd editcmd.Command) error

// actions は、本体の対象を取らない操作の表である。キーは input の表の ID である。
//
// req: FR-02
var actions map[string]actionFunc

// repeatableActions は、ドットリピートで繰り返す操作である。
// インサートモードへ移る操作（i、a、o など）は、インサートモードを抜けた時点で記録するので、ここには入れない。
var repeatableActions = map[string]bool{
	"delete-char":        true,
	"delete-char-before": true,
	"replace-char":       true,
	"toggle-case":        true,
	"join-lines":         true,
	"put-after":          true,
	"put-before":         true,
	"delete-to-line-end": true,
}

func init() {
	// actions の関数は State のメソッドを通して actions を参照しうるので、初期化の循環を避けて init で作る。
	actions = map[string]actionFunc{
		"delete-char":        actDeleteChar,
		"delete-char-before": actDeleteCharBefore,
		"replace-char":       actReplaceChar,
		"delete-to-line-end": actShorthand("delete", editcmd.Target{Kind: editcmd.TargetMotion, Ref: editcmd.Ref{ID: "line-end"}}),
		"change-to-line-end": actShorthand("change", editcmd.Target{Kind: editcmd.TargetMotion, Ref: editcmd.Ref{ID: "line-end"}}),
		"substitute-line":    actShorthand("change", editcmd.Target{Kind: editcmd.TargetLine}),
		"substitute-char":    actShorthand("change", editcmd.Target{Kind: editcmd.TargetMotion, Ref: editcmd.Ref{ID: "right"}}),
		"yank-line":          actShorthand("yank", editcmd.Target{Kind: editcmd.TargetLine}),
		"toggle-case":        actToggleCase,
		"join-lines":         actJoin,
		"put-after":          actPut(true),
		"put-before":         actPut(false),
		"open-below":         actOpen(true),
		"open-above":         actOpen(false),
		"insert":             actInsert,
		"append":             actInsert,
		"insert-line-start":  actInsert,
		"append-line-end":    actInsert,
		"undo":               actUndo(false),
		"redo":               actUndo(true),
		"command-line":       actCommandLine,
		"visual":             actVisual(ModeVisual),
		"visual-line":        actVisual(ModeVisualLine),
		"visual-swap":        actVisualSwap,
		"page-down":          actPage(true),
		"page-up":            actPage(false),
		"output-next":        actOutput(ex.OutputNext),
		"output-close":       actOutput(ex.OutputClose),
		"output-scroll-up":   actOutput(ex.OutputScrollUp),
		"output-scroll-down": actOutput(ex.OutputScrollDown),
		"jump-older":         actJump(-1),
		"jump-newer":         actJump(1),
		"alternate-buffer":   actAlternate,
	}
}

// action は、対象を取らない操作を実行する。拡張機能の操作なら問い合わせる（FR-12）。
func (s *State) action(cmd editcmd.Command) error {
	if !cmd.Action.Builtin() {
		return s.extAction(cmd)
	}
	f, ok := actions[cmd.Action.ID]
	if !ok {
		return failf("unknown action %q", cmd.Action.ID)
	}
	return f(s, cmd)
}

// extAction は、拡張機能の操作を問い合わせ、返った編集を適用する。
func (s *State) extAction(cmd editcmd.Command) error {
	if s.Extensions == nil {
		return ErrNoExtensions
	}
	tx, err := s.Extensions.Action(cmd.Action, s.buf, ActionQuery{
		Cursor: s.cursor, Count: cmd.Count, CountGiven: cmd.CountGiven, Char: cmd.Char, Register: cmd.Register,
	}, s.queryInfo())
	if err != nil {
		return err
	}
	if len(tx.Edits) > 0 {
		if err := s.applyTx(tx); err != nil {
			return err
		}
	}
	if cmd.Mode == editcmd.ModeInsert {
		s.cursor = s.clampPos(s.cursor)
		s.startInsert(cmd, 1)
		return nil
	}
	s.setCursorNormal(s.cursor)
	return nil
}

// actShorthand は、省略形のコマンドである。D は d$、C は c$、S は cc、s は cl、Y は yy に当たる（Vim の nv_optrans()）。
// 回数とレジスターをそのまま渡し、本体のオペレーター op と対象 tg の組として実行する。
// 3D は d3$（カーソルから行末までと、続く 2 行）、3s は 3cl、3S は 3cc である。
//
// ドットリピートには、D は省略形の値のまま記録する（回数を付けた . は、記録した値の回数を置き換える）。
// C、S、s は、インサートモードを抜けた時点で、置き換えた後の c の値を記録する。どちらも繰り返す内容は Vim と同じである。
// Y はヤンクなので記録しない。
//
// req: FR-02
func actShorthand(op string, tg editcmd.Target) actionFunc {
	return func(s *State, cmd editcmd.Command) error {
		oc := cmd
		oc.Kind = editcmd.KindOperator
		oc.Action = editcmd.Ref{}
		oc.Operator = editcmd.Ref{ID: op}
		oc.Target = tg
		return s.operator(oc)
	}
}

// actDeleteChar は、x である。カーソルから回数の文字を消す。行末を越えては消さない。空の行では何もしない。
func actDeleteChar(s *State, cmd editcmd.Command) error {
	if err := checkRegister(cmd.Register); err != nil {
		return err
	}
	line := s.line(s.cursor.Line)
	if line == "" {
		return nil
	}
	end, _ := advanceChars(line, s.cursor.Byte, max(cmd.Count, 1))
	return s.deleteInLine(cmd, s.cursor.Byte, end)
}

// actDeleteCharBefore は、X である。カーソルの前の回数の文字を消す。行の先頭では失敗する。
func actDeleteCharBefore(s *State, cmd editcmd.Command) error {
	if err := checkRegister(cmd.Register); err != nil {
		return err
	}
	if s.cursor.Byte == 0 {
		return failf("at the start of the line")
	}
	line := s.line(s.cursor.Line)
	start := s.cursor.Byte
	for range max(cmd.Count, 1) {
		if start == 0 {
			break
		}
		start = prevChar(line, start)
	}
	// X は dh に当たり、Vim はカーソルを範囲の始まりへ移してから消す。アンドゥの後のカーソルもそこへ戻る。
	s.markUndoCursor(pos(s.cursor.Line, start))
	defer s.clearUndoCursor()
	return s.deleteInLine(cmd, start, s.cursor.Byte)
}

// deleteInLine は、カーソルの行の [from, to) を消してレジスターに入れる。
func (s *State) deleteInLine(cmd editcmd.Command, from, to int) error {
	l := s.cursor.Line
	rg := region{r: buffer.Range{Start: pos(l, from), End: pos(l, to)}}
	reg, edit, err := s.cut(rg)
	if err != nil {
		return err
	}
	if err := s.apply(edit); err != nil {
		return err
	}
	s.writeRegister(cmd.Register, reg, true, false)
	s.setCursorNormal(pos(l, from))
	return nil
}

// actReplaceChar は、r である。カーソルから回数の文字を、指定した文字に置き換える。
// 行に残りの文字が回数より少なければ失敗する。r<CR> は、回数の文字をまとめて 1 つの改行に置き換える。
func actReplaceChar(s *State, cmd editcmd.Command) error {
	n := max(cmd.Count, 1)
	l := s.cursor.Line
	line := s.line(l)
	end, moved := advanceChars(line, s.cursor.Byte, n)
	if moved < n {
		return failf("not enough characters")
	}
	rng := buffer.Range{Start: s.cursor, End: pos(l, end)}
	if cmd.Char == '\n' {
		if err := s.apply(buffer.Edit{Range: rng, Text: "\n"}); err != nil {
			return err
		}
		s.setCursorNormal(pos(l+1, 0))
		return nil
	}
	ch := string(cmd.Char)
	if err := s.apply(buffer.Edit{Range: rng, Text: strings.Repeat(ch, n)}); err != nil {
		return err
	}
	s.setCursorNormal(pos(l, s.cursor.Byte+(n-1)*len(ch)))
	return nil
}

// actToggleCase は、~ である（'tildeop' がオフ）。カーソルから回数の文字の大文字と小文字を入れ替え、その後ろへ進む。
func actToggleCase(s *State, cmd editcmd.Command) error {
	l := s.cursor.Line
	line := s.line(l)
	if line == "" {
		return nil
	}
	end, _ := advanceChars(line, s.cursor.Byte, max(cmd.Count, 1))
	text := line[s.cursor.Byte:end]
	if t := mapCase(text, toggleCase); t != text {
		if err := s.apply(buffer.Edit{Range: buffer.Range{Start: s.cursor, End: pos(l, end)}, Text: t}); err != nil {
			return err
		}
	}
	s.setCursorNormal(pos(l, end))
	return nil
}

// actJoin は、J である。回数の行（2 未満は 2）を 1 行につなぐ。行が足りなければ、ある分だけつなぐ。
// 最後の行では失敗する。
//
// つなぐ規則は Vim の do_join() に合わせる。次の行の先頭の空白を消し、間にスペースを 1 つ入れる。
// ただし、次の行が空か ')' で始まる場合、それまでにつないだテキストが空の場合、前の行がタブで終わる場合は入れない。
// 前の行がスペースで終わる場合はスペースを足さない。'joinspaces' がオンなので、'.'、'?'、'!' の後には 2 つ入れる。
// カーソルは、最後につないだ箇所（入れたスペースの始まり）へ移る。
func actJoin(s *State, cmd editcmd.Command) error {
	l := s.cursor.Line
	if l == s.lastLine() {
		return failf("cannot join the last line")
	}
	joins := min(max(cmd.Count, 2)-1, s.lastLine()-l)
	var sb strings.Builder
	first := s.line(l)
	sb.WriteString(first)
	end1, end2 := lastTwo(first)
	col := 0
	for i := 1; i <= joins; i++ {
		next := strings.TrimLeft(s.line(l+i), " \t")
		spaces := 0
		if next != "" && next[0] != ')' && sb.Len() != 0 && end1 != '\t' {
			if end1 == ' ' {
				end1 = end2
			} else {
				spaces++
			}
			if end1 == '.' || end1 == '?' || end1 == '!' {
				spaces++
			}
		}
		col = sb.Len()
		sb.WriteString(strings.Repeat(" ", spaces))
		sb.WriteString(next)
		end1, end2 = 0, 0
		if next != "" {
			end1, end2 = lastTwo(next)
		}
	}
	rng := buffer.Range{Start: pos(l, 0), End: s.lineEnd(l + joins)}
	if err := s.apply(buffer.Edit{Range: rng, Text: sb.String()}); err != nil {
		return err
	}
	s.setCursorNormal(pos(l, col))
	return nil
}

// lastTwo は、文字列の最後の文字と、その前の文字を返す。無ければ 0 を返す。
func lastTwo(str string) (last, before rune) {
	r1, n1 := utf8.DecodeLastRuneInString(str)
	if n1 == 0 {
		return 0, 0
	}
	r2, n2 := utf8.DecodeLastRuneInString(str[:len(str)-n1])
	if n2 == 0 {
		return r1, 0
	}
	return r1, r2
}

// maxPutBytes は、p と P と、回数つきの挿入で一度に入れるテキストの上限である。
// 999999999p のような入力でメモリを使い切らないための仮の値である。
const maxPutBytes = 64 << 20

// actPut は、p（after が true）と P である。
//
// 文字単位のテキストは、カーソルの文字の後（P は前）に回数だけ続けて入れる。
// カーソルは、入れたテキストが 1 行なら最後の文字へ、複数の行にわたれば始まりへ移る。
// 行単位のテキストは、カーソルの行の下（P は上）に入れ、入れた最初の行の最初の空白でない文字へ移る。
// 名前を省略すれば履歴の先頭（上限を超えた無名のレジスターがあればそれ）を、名前を指定すればそのレジスターを入れる。
// 貼り付けは履歴の順を変えない（基本設計 4.5）。
// レジスターが空なら失敗する。_ は何もしない。
//
// req: FR-51
func actPut(after bool) actionFunc {
	return func(s *State, cmd editcmd.Command) error {
		if cmd.Register == '_' {
			return nil
		}
		reg, ok := s.registers.Get(cmd.Register)
		if !ok {
			return checkRegister(cmd.Register)
		}
		if reg.empty() {
			return failf("register %q is empty", cmd.Register)
		}
		if reg.Text == "" && !reg.Linewise {
			// 空のテキストが入ったレジスター（空の行の C の後など）は、Vim と同じく何も入れず、カーソルも動かさない。
			return nil
		}
		n := max(cmd.Count, 1)
		unit := reg.Text
		if reg.Linewise {
			unit += "\n"
		}
		if len(unit)*n > maxPutBytes {
			return failf("text too large")
		}
		text := strings.Repeat(unit, n)
		l := s.cursor.Line
		if reg.Linewise {
			text = strings.TrimSuffix(text, "\n")
			if after {
				if err := s.apply(buffer.Edit{Range: emptyRange(s.lineEnd(l)), Text: "\n" + text}); err != nil {
					return err
				}
				s.setCursorNormal(s.firstNonBlankPos(l + 1))
				return nil
			}
			if err := s.apply(buffer.Edit{Range: emptyRange(pos(l, 0)), Text: text + "\n"}); err != nil {
				return err
			}
			s.setCursorNormal(s.firstNonBlankPos(l))
			return nil
		}
		at := s.cursor
		if after {
			at.Byte = nextChar(s.line(l), at.Byte)
		}
		if err := s.apply(buffer.Edit{Range: emptyRange(at), Text: text}); err != nil {
			return err
		}
		if strings.Contains(text, "\n") {
			s.setCursorNormal(at)
			return nil
		}
		s.setCursorNormal(pos(l, at.Byte+lastCharStart(text)))
		return nil
	}
}

// emptyRange は、p の位置の空の範囲（挿入の位置）を返す。
func emptyRange(p buffer.Position) buffer.Range { return buffer.Range{Start: p, End: p} }

// actOpen は、o（below が true）と O である。下か上に空の行を開いてインサートモードへ移る。
func actOpen(below bool) actionFunc {
	return func(s *State, cmd editcmd.Command) error {
		l := s.cursor.Line
		if below {
			if err := s.apply(buffer.Edit{Range: emptyRange(s.lineEnd(l)), Text: "\n"}); err != nil {
				return err
			}
			s.cursor = pos(l+1, 0)
		} else {
			if err := s.apply(buffer.Edit{Range: emptyRange(pos(l, 0)), Text: "\n"}); err != nil {
				return err
			}
			s.cursor = pos(l, 0)
		}
		s.startInsert(cmd, max(cmd.Count, 1))
		return nil
	}
}

// actInsert は、i、a、I、A である。カーソルを挿入の位置へ移してインサートモードへ移る。
// I は行の最初の空白でない文字の前、A は行末である。
func actInsert(s *State, cmd editcmd.Command) error {
	l := s.cursor.Line
	line := s.line(l)
	switch cmd.Action.ID {
	case "append":
		s.cursor.Byte = nextChar(line, s.cursor.Byte)
	case "insert-line-start":
		s.cursor.Byte = len(line) - len(strings.TrimLeft(line, " \t"))
	case "append-line-end":
		s.cursor.Byte = len(line)
	}
	s.startInsert(cmd, max(cmd.Count, 1))
	return nil
}

// actUndo は、u（redo が false）と <C-r> である。回数の単位を取り消すかやり直す（FR-07）。
// 1 つも取り消せなければ失敗する。
//
// カーソルは、Vim の u_undoredo() と同じ規則で、最後に取り消した（やり直した）単位から決める（undoCursor）。
//
// req: FR-07
func actUndo(redo bool) actionFunc {
	return func(s *State, cmd editcmd.Command) error {
		done := 0
		for range max(cmd.Count, 1) {
			var (
				ch buffer.Change
				ok bool
			)
			if redo {
				ch, ok = s.buf.Redo()
			} else {
				ch, ok = s.buf.Undo()
			}
			if !ok {
				break
			}
			s.res.Changes = append(s.res.Changes, ch)
			s.cursor = s.undoCursor(s.buf.LastUndo())
			done++
		}
		if done == 0 {
			if redo {
				return failf("already at newest change")
			}
			return failf("already at oldest change")
		}
		s.setCursorNormal(s.cursor)
		return nil
	}
}

// undoCursor は、アンドゥとリドゥの後のカーソルの位置を、Vim 9.1 の u_undoredo() と同じ規則で決める。
//
// 単位に記録した変更の前のカーソル（u.Cursor。Vim の uh_cursor）の行が、変わった行の範囲か、その前後の 1 行にあれば、その位置へ戻す。
// そうでなければ、変わった行の範囲の最初の行へ移る。ただし、その行が記録したカーソルの次の行なら、記録したカーソルの行へ移る
// （o の取り消しで、o を打った行へ戻る）。
// 移った行が記録したカーソルの行なら記録した列へ、そうでなければ最初の空白でない文字へ移る。
// 移った行がバッファの外なら、最後の行の最初の空白でない文字へ移る（最後の行を消す変更のリドゥ。記録した列は使わない。
// Vim 9.1 で `:3d` と `dd` のリドゥを確かめた。#86）。
//
// 記録したカーソルは、変更を始めたときのカーソルである。Vim と同じく、オペレーターでは範囲の始まり、
// dd や cc では最初の空白でない文字へ動かした後の位置を記録する（markUndoCursor）。
func (s *State) undoCursor(u buffer.UndoInfo) buffer.Position {
	cur := s.cursor
	if u.HasCursor && u.Cursor.Line >= u.Top-1 && u.Cursor.Line <= u.Top+u.Lines {
		cur = u.Cursor
	} else {
		cur.Line = u.Top
	}
	if u.HasCursor && u.Cursor.Line+1 == cur.Line && cur.Line > 0 {
		cur.Line--
	}
	if cur.Line > s.lastLine() {
		return s.firstNonBlankPos(s.lastLine())
	}
	if u.HasCursor && u.Cursor.Line == cur.Line {
		return s.clampPos(pos(cur.Line, u.Cursor.Byte))
	}
	return s.firstNonBlankPos(cur.Line)
}

// actCommandLine は、: である。コマンドラインモードへ移る。
// ビジュアルモードからは、選択を抜けて、選択した行の範囲 '<,'> を入力した状態で始める（Vim と同じ）。
func actCommandLine(s *State, _ editcmd.Command) error {
	s.cmdline = s.cmdline[:0]
	if s.mode.visual() {
		s.exitVisual()
		s.cmdline = append(s.cmdline, []rune("'<,'>")...)
	}
	s.mode = ModeCommandLine
	return nil
}
