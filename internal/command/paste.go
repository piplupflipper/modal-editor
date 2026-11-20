package command

import (
	"github.com/piplupflipper/vi-imitation/internal/buffer"
	"github.com/piplupflipper/vi-imitation/internal/editcmd"
)

// pasteAppend は、ノーマルモードとビジュアルモードの貼り付けを、ドットリピートに記録するときの値（a）である。
var pasteAppend = editcmd.Command{
	Kind: editcmd.KindAction, Action: editcmd.Ref{ID: "append"}, Count: 1, Mode: editcmd.ModeInsert,
}

// pasteDelete は、ビジュアルモードの貼り付けで選択を消す値（"_d）である。
// 置き換えられたテキストは、名前付きのレジスターも履歴も変えない（FR-51）。
var pasteDelete = editcmd.Command{Kind: editcmd.KindOperator, Operator: editcmd.Ref{ID: "delete"}, Count: 1, Register: '_'}

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
//     Vim は回数とオペレーターでは同じく捨てるが、"a の後の貼り付けは入れ（レジスターの指定は無視する）、
//     r と f などの文字を待つコマンドの後は貼った最初の文字を引数に使う。ここでは区別しない（docs/vim-differences.md）
//   - ビジュアルモード: 選択を消し、消した位置から入れて、ノーマルモードへ戻る。消したテキストは、"_d と同じくどのレジスターにも入れず、
//     履歴にも積まない（FR-51 の「ビジュアルモードの貼り付けで置き換えられたテキスト」。Vim は "1 か "- に入れる）。
//     行単位の選択でバッファの最後の行まで消し、カーソルが選択より上の行へ移ったときは、下に空の行を開いてそこへ入れる。
//     文字単位の選択で行末まで消し、カーソルが選択より左へ移ったときは、カーソルの文字の後ろへ入れる（Vim の nv_edit の K_PS）
//
// ノーマルモードとビジュアルモードの貼り付けは、選択を消すことも含めてアンドゥの 1 単位にする。
// ドットリピートには、どちらも a でテキストを入れる操作として記録する（Vim と同じ。ビジュアルモードでも . は消さずに a で入れる）。
// マクロの記録中は、記録の時点の貼り付けに当たるキーの列を記録する。ノーマルモードは i か a（記録の時点のカーソルで選ぶ）、
// テキスト、<Esc>、ビジュアルモードは "_c、テキスト、<Esc> である（再生でも置き換えたテキストを履歴に積まない）。打ちかけを取り消したときは、取り消しの <Esc> を記録する。
// Vim は貼り付けの印ごと記録し、再生の時点で i か a を選び直すので、記録と再生でカーソルが行の先頭かどうかが違うと結果が違う。
// 行単位の選択の c は空の行を残すので、ビジュアルモードの行単位の選択でも再生の結果が違う（docs/vim-differences.md）。
func (s *State) Paste(text string) Result {
	s.res = Result{}
	// 端末からの貼り付けも「別の操作」なので、p の後の <C-p> と <C-n> の対象を無くす（FR-55）。
	s.lastPut = nil
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
			// 打ちかけた列を取り消す。打ちかけたキーは Feed が記録済みなので、取り消しの <Esc> も記録し、
			// 再生で後のキーに回数などが掛からないようにする（qa3 の後に貼って x を打つ記録は 3<Esc>x になる）。
			s.record(editcmd.Esc)
			s.runResults(s.parser.Feed(editcmd.Esc))
			s.fail(failf("paste while a command is pending"))
			return s.take()
		}
		if s.buf.ReadOnly() {
			// 編集を禁止したバッファでは、貼ったテキストを捨てて E21 で断る（FR-85）。
			// ドットリピートとマクロの記録も、モードも変えない（Vim 9.1 の nv_edit が 'modifiable' の切れたバッファで貼り付けを捨てるのと同じ）。
			s.fail(buffer.ErrReadOnly)
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
	s.record(editcmd.Rune('"'), editcmd.Rune('_'), editcmd.Rune('c'))
	s.record(pasteKeys(text, false)...)
	s.record(editcmd.Esc)
	// 消した後のカーソルが選択より上か左かを、選択の両端（anchor と head）と比べて判定する。
	oldHead, oldAnchor := s.visual.head, s.visual.anchor
	linewise := s.mode == ModeVisualLine
	rg := s.visualRegion()
	s.exitVisual()
	s.buf.BeginUndoGroup()
	if err := s.regionOperator(pasteDelete, rg); err != nil {
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
		if c.Line < oldHead.Line && c.Line < oldAnchor.Line {
			// バッファの最後の行まで消して、カーソルが上の行へ移った。消した行の位置に空の行を開いて入れる。
			if err := s.apply(buffer.Edit{Range: emptyRange(s.lineEnd(c.Line)), Text: "\n"}); err != nil {
				s.buf.EndUndoGroup()
				s.fail(err)
				return
			}
			s.cursor = pos(c.Line+1, 0)
		}
	case c.Byte < oldHead.Byte && c.Byte < oldAnchor.Byte:
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
