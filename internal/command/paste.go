package command

import (
	"github.com/piplupflipper/modal-editor/internal/buffer"
	"github.com/piplupflipper/modal-editor/internal/editcmd"
)

// pasteAppend は、ノーマルモードとビジュアルモードの貼り付けを、ドットリピートに記録するときの値（a）である。
var pasteAppend = editcmd.Command{
	Kind: editcmd.KindAction, Action: editcmd.Ref{ID: "append"}, Count: 1, Mode: editcmd.ModeInsert,
}

// pasteDelete は、ビジュアルモードの貼り付けで選択を消す値（d）である。
var pasteDelete = editcmd.Command{Kind: editcmd.KindOperator, Operator: editcmd.Ref{ID: "delete"}, Count: 1}

// Paste は、端末から貼り付けた（bracketed paste）テキスト text を、現在のモードに応じて入れる（#114）。
// text の改行は "\n" とする。"\n" と "\t" の他の制御文字は入れずに捨てる。
// キーの列と違い、ノーマルモードとビジュアルモードでも、テキストをコマンドとして解釈しない。
//
// 振る舞いは、Vim 9.1（vim -Nu NONE）を tmux 3.4 の中で動かし、paste-buffer -p で貼って確かめたものに合わせる（2026-11-16）。
//
//   - インサートモード: カーソルの位置に入れ、インサートモードのまま残る
//   - コマンドラインモード: コマンドラインに入れる。改行で確定せず、文字 '\r' として入れる（Vim は ^M と表示し、実行しない）
//   - ノーマルモード: カーソルが行の先頭（空の行を含む）なら i、それ以外なら a と同じ位置から入れ、
//     <Esc> と同じくノーマルモードへ戻る（カーソルは入れた最後の文字）。
//     回数、オペレーター、レジスターの指定などを打ちかけていれば、それを取り消し、貼り付けも捨てて失敗にする。
//     Vim は回数とオペレーターでは同じく捨てるが、"a の後の貼り付けは入れる（レジスターの指定は無視する）。ここでは区別しない
//   - ビジュアルモード: 選択を消し、消した位置から入れて、ノーマルモードへ戻る。消したテキストは、1 行の中の文字単位の選択なら "-d、
//     それ以外なら "1d と同じくレジスターに入れる（無名のレジスターも指す。1 行の中でも改行を含めば 1 にも入る）。
//     Vim は 1 に入れるとき 2 にも同じテキストを入れる（1〜9 を 2 回ずらす）が、ここでは 1 回だけずらす。
//     行単位の選択でバッファの最後の行まで消し、カーソルが選択より上の行へ移ったときは、下に空の行を開いてそこへ入れる。
//     文字単位の選択で行末まで消し、カーソルが選択より左へ移ったときは、カーソルの文字の後ろへ入れる（Vim の nv_edit の K_PS）
//
// ノーマルモードとビジュアルモードの貼り付けは、選択を消すことも含めてアンドゥの 1 単位にする。
// ドットリピートには、どちらも a でテキストを入れる操作として記録する（Vim と同じ。ビジュアルモードでも . は消さずに a で入れる）。
// マクロの記録中は、貼り付けと同じ結果になるキーの列を記録する。ノーマルモードは i か a、テキスト、<Esc>、
// ビジュアルモードは c、テキスト、<Esc> である（Vim は貼り付けの印ごと記録する。行単位の選択の c は空の行を残すので、再生の結果が違う）。
func (s *State) Paste(text string) Result {
	s.res = Result{}
	text = pasteable(text)
	if text == "" {
		return s.take()
	}
	switch s.mode {
	case ModeInsert:
		s.record(pasteKeys(text, false)...)
		for _, r := range text {
			s.insertRune(r)
		}
	case ModeCommandLine:
		s.record(pasteKeys(text, true)...)
		for _, r := range text {
			if r == '\n' {
				r = '\r'
			}
			s.cmdline = append(s.cmdline, r)
		}
	case ModeNormal, ModeVisual, ModeVisualLine:
		if s.parser.Pending() {
			s.runResults(s.parser.Feed(editcmd.Esc)) // 打ちかけた列を取り消す
			s.fail(failf("paste while a command is pending"))
			return s.take()
		}
		if s.mode == ModeNormal {
			s.pasteNormal(text)
		} else {
			s.pasteVisual(text)
		}
	}
	return s.take()
}

// pasteable は、text から "\n" と "\t" の他の制御文字を除いたものを返す。
func pasteable(text string) string {
	clean := true
	for _, r := range text {
		if (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f {
			clean = false
			break
		}
	}
	if clean {
		return text
	}
	out := make([]rune, 0, len(text))
	for _, r := range text {
		if (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f {
			continue
		}
		out = append(out, r)
	}
	return string(out)
}

// record は、マクロの記録中なら keys を記録する。
func (s *State) record(keys ...editcmd.Key) {
	if s.recording != 0 {
		s.recorded = append(s.recorded, keys...)
	}
}

// pasteKeys は、text を入れるキーの列を返す。cmdline が true なら、改行を確定（<CR>）ではなく文字 '\r' にする。
func pasteKeys(text string, cmdline bool) []editcmd.Key {
	keys := make([]editcmd.Key, 0, len(text))
	for _, r := range text {
		switch {
		case r == '\n' && cmdline:
			keys = append(keys, editcmd.Rune('\r'))
		case r == '\n':
			keys = append(keys, editcmd.Special(editcmd.CodeEnter))
		case r == '\t':
			keys = append(keys, editcmd.Special(editcmd.CodeTab))
		default:
			keys = append(keys, editcmd.Rune(r))
		}
	}
	return keys
}

// pasteNormal は、ノーマルモードの貼り付けである（Paste の doc）。
func (s *State) pasteNormal(text string) {
	start := editcmd.Rune('i')
	if s.cursor.Byte > 0 {
		start = editcmd.Rune('a')
		s.cursor.Byte = nextChar(s.line(s.cursor.Line), s.cursor.Byte)
	}
	s.record(start)
	s.record(pasteKeys(text, false)...)
	s.record(editcmd.Esc)
	s.buf.BeginUndoGroup()
	s.pasteInsert(text)
}

// pasteVisual は、ビジュアルモードの貼り付けである（Paste の doc）。
func (s *State) pasteVisual(text string) {
	s.record(editcmd.Rune('c'))
	s.record(pasteKeys(text, false)...)
	s.record(editcmd.Esc)
	oldCursor, oldAnchor := s.cursor, s.visual.anchor
	linewise := s.mode == ModeVisualLine
	// Vim と同じく、1 行の中の文字単位の選択は - に、それ以外は 1 に入れる（どちらも無名のレジスターが指す）。
	del := pasteDelete
	del.Register = '1'
	if !linewise && oldCursor.Line == oldAnchor.Line {
		del.Register = '-'
	}
	rg := s.visualRegion()
	s.exitVisual()
	s.buf.BeginUndoGroup()
	if err := s.regionOperator(del, rg); err != nil {
		s.buf.EndUndoGroup()
		s.fail(err)
		return
	}
	c := s.cursor
	line := s.line(c.Line)
	switch {
	case c.Byte >= len(line):
		// カーソルの位置に文字が無い（空の行）なら、そのまま入れる。
	case linewise:
		if c.Line < oldCursor.Line && c.Line < oldAnchor.Line {
			// バッファの最後の行まで消して、カーソルが上の行へ移った。消した行の位置に空の行を開いて入れる。
			if err := s.apply(buffer.Edit{Range: emptyRange(s.lineEnd(c.Line)), Text: "\n"}); err != nil {
				s.buf.EndUndoGroup()
				s.fail(err)
				return
			}
			s.cursor = pos(c.Line+1, 0)
		}
	case c.Byte < oldCursor.Byte && c.Byte < oldAnchor.Byte:
		// 行末まで消して、カーソルが選択より左の文字へ戻った。その文字の後ろへ入れる。
		s.cursor.Byte = nextChar(line, c.Byte)
	}
	s.pasteInsert(text)
}

// pasteInsert は、開いてあるアンドゥのグループの中で、カーソルの位置から text を入れ、<Esc> と同じくノーマルモードへ戻る。
// ドットリピートには a として記録する（finishInsert）。
func (s *State) pasteInsert(text string) {
	s.startInsert(pasteAppend, 1)
	for _, r := range text {
		s.insertRune(r)
	}
	s.finishInsert()
}
