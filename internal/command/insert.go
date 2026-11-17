package command

import (
	"github.com/piplupflipper/modal-editor/internal/buffer"
	"github.com/piplupflipper/modal-editor/internal/command/target"
	"github.com/piplupflipper/modal-editor/internal/editcmd"
)

// insertState は、インサートモードの 1 回分の入力の状態である。
type insertState struct {
	// cmd は、インサートモードへ移った編集コマンドの値である。抜けるときに Text を埋めてドットリピートに記録する。
	cmd editcmd.Command
	// text は、入力した文字の列である。<CR> は '\n'、<BS> は '\b' として記録する。
	text []rune
	// repeat は、入力したテキストを入れる回数である（3ix<Esc> なら 3）。c では 1 である。
	repeat int
	// extraGroups は、抜けるときに追加で閉じるアンドゥのグループの数である（インサートモードで終わったマクロの再生の分）。
	extraGroups int
	// moved は、カーソルキーで入力を区切った後、まだ文字を入力していないかを表す（Vim の arrow_used）。
	// このときアンドゥのグループは閉じていて、ドットリピートの記録も済んでいる（breakInsert）。
	moved bool
}

// startInsert は、インサートモードへ移る。アンドゥのグループは呼び出し側（change）が開いたままにしておく。
// ドットリピートの再実行では、記録したテキストを入力してすぐに抜ける。
func (s *State) startInsert(cmd editcmd.Command, repeat int) {
	s.mode = ModeInsert
	s.insert = insertState{cmd: cmd, repeat: repeat}
	if s.replaying {
		for _, r := range cmd.Text {
			s.insertRune(r)
		}
		s.finishInsert()
	}
}

// insertKey は、インサートモードのキーを 1 個処理する。
// 文字、<CR>、<Tab>、<BS>、<Esc> と、カーソルキー（insertMove）を扱い、それ以外のキー（<Del>、Ctrl つきの文字など）は無視する。
//
// req: FR-01
func (s *State) insertKey(k editcmd.Key) {
	if k.Mod != 0 {
		return
	}
	switch k.Code {
	case editcmd.CodeEsc:
		s.finishInsert()
	case editcmd.CodeEnter:
		s.insertRune('\n')
	case editcmd.CodeTab:
		s.insertRune('\t')
	case editcmd.CodeBackspace:
		s.insertRune('\b')
	case editcmd.CodeRune:
		s.insertRune(k.Rune)
	case editcmd.CodeUp, editcmd.CodeDown, editcmd.CodeLeft, editcmd.CodeRight,
		editcmd.CodeHome, editcmd.CodeEnd, editcmd.CodePageUp, editcmd.CodePageDown:
		s.insertMove(k.Code)
	case editcmd.CodeDelete, editcmd.CodeInsert, editcmd.CodeF1:
	}
}

// insertMove は、インサートモードのカーソルキーでカーソルを動かす（Vim 9.1 の ins_left() などに当たる）。
//
//   - <Left> と <Right> は 1 文字動く。行の先頭と行末の位置を越えない（'whichwrap' の既定）
//   - <Up> と <Down> は、望みの列を保って 1 行動く。行の幅が望みの列に届かなければ行末の位置へ移る
//   - <Home> は行の先頭へ、<End> は行末の位置へ移る。<End> の後の <Up> と <Down> も行末の位置へ移る
//   - <PageUp> と <PageDown> は、ノーマルモードと同じく 1 ページスクロールする（scrollPage）
//
// 動けたときは、入力をそこで区切る（breakInsert）。動けなかったときは何もしない。
// Vim はビープを鳴らすが、ノーマルモードのモーションの失敗と違って先行入力を捨てないので、失敗として扱わない（マクロの再生は止まらない）。
//
// req: FR-01, FR-07
func (s *State) insertMove(c editcmd.Code) {
	p := s.cursor
	line := s.line(p.Line)
	want := -1 // -1 なら、動いた後の位置から望みの列を数え直す
	switch c {
	case editcmd.CodeLeft:
		if p.Byte == 0 {
			return
		}
		p.Byte = prevChar(line, p.Byte)
	case editcmd.CodeRight:
		if p.Byte >= len(line) {
			return
		}
		p.Byte = nextChar(line, p.Byte)
	case editcmd.CodeUp, editcmd.CodeDown:
		l := p.Line - 1
		if c == editcmd.CodeDown {
			l = p.Line + 1
		}
		if l < 0 || l > s.lastLine() {
			return
		}
		want = s.want
		p = pos(l, s.insertByteAt(l, want))
	case editcmd.CodeHome:
		p.Byte = 0
	case editcmd.CodeEnd:
		p.Byte = len(line)
		want = target.WantEOL
	case editcmd.CodePageUp, editcmd.CodePageDown:
		if err := s.scrollPage(c == editcmd.CodePageDown, 1); err != nil {
			return
		}
		s.breakInsert()
		return
	case editcmd.CodeRune, editcmd.CodeEsc, editcmd.CodeEnter, editcmd.CodeTab, editcmd.CodeBackspace, editcmd.CodeDelete,
		editcmd.CodeInsert, editcmd.CodeF1:
		return
	}
	s.cursor = p
	if want >= 0 {
		s.want = want
	} else {
		s.updateWant()
	}
	s.breakInsert()
}

// insertByteAt は、インサートモードで、行 l の望みの列 want に当たる位置を返す。
// 行の幅が want に届かなければ、行末の位置を返す（インサートモードのカーソルは行末の位置に置ける）。
func (s *State) insertByteAt(l, want int) int {
	line := s.line(l)
	cols := s.columns()
	if line == "" || want > cols.Column(line, lastCharStart(line)) {
		return len(line)
	}
	return cols.ByteAt(line, want)
}

// breakInsert は、カーソルキーで動いたときに、インサートモードの入力をそこで区切る（Vim の start_arrow()）。
// それまでの入力をアンドゥの 1 単位として閉じ、ドットリピートとレジスター . に記録する。回数による繰り返し（3ix）はしない。
// 続けて文字を入力すると、resumeInsert が i として新しい入力を始める。このため、ifoo<Left>bar<Esc> の後の . は bar を i で入れ、
// u は bar だけを戻す（Vim 9.1 と同じ）。
// 既に区切った後（moved）なら何もしない。
//
// req: FR-06, FR-07
func (s *State) breakInsert() {
	if s.insert.moved {
		return
	}
	s.insert.moved = true
	s.buf.EndUndoGroup()
	text := string(s.insert.text)
	cmd := s.insert.cmd
	cmd.Text = text
	s.dot = &cmd
}

// resumeInsert は、カーソルキーで区切った後（moved）の最初の入力の前に、新しい入力を i として始める。
// アンドゥのグループを開き直し、回数は 1 にする（Vim は区切った後の入力を "1i" として記録し直す）。
func (s *State) resumeInsert() {
	if !s.insert.moved {
		return
	}
	s.buf.BeginUndoGroup()
	s.insert.moved = false
	s.insert.text = nil
	s.insert.repeat = 1
	s.insert.cmd = editcmd.Command{Kind: editcmd.KindAction, Action: editcmd.Ref{ID: "insert"}, Count: 1, Mode: editcmd.ModeInsert}
}

// insertRune は、入力した文字を記録して、バッファに入れる。
func (s *State) insertRune(r rune) {
	s.resumeInsert()
	s.insert.text = append(s.insert.text, r)
	if err := s.typeRune(r); err != nil {
		s.fail(err)
	}
}

// typeRune は、文字 1 つをカーソルの位置に入れる。'\n' は行を分け、'\b' はカーソルの前の文字を消す。
// '\b' は行の先頭では前の行とつなぎ、バッファの先頭では何もしない（'backspace' が "indent,eol,start" の動き）。
func (s *State) typeRune(r rune) error {
	c := s.cursor
	switch r {
	case '\b':
		var from buffer.Position
		switch {
		case c.Byte > 0:
			from = pos(c.Line, prevChar(s.line(c.Line), c.Byte))
		case c.Line > 0:
			from = s.lineEnd(c.Line - 1)
		default:
			return nil
		}
		if err := s.apply(buffer.Edit{Range: buffer.Range{Start: from, End: c}}); err != nil {
			return err
		}
		s.cursor = from
	case '\n':
		if err := s.apply(buffer.Edit{Range: emptyRange(c), Text: "\n"}); err != nil {
			return err
		}
		s.cursor = pos(c.Line+1, 0)
	default:
		str := string(r)
		if err := s.apply(buffer.Edit{Range: emptyRange(c), Text: str}); err != nil {
			return err
		}
		s.cursor.Byte += len(str)
	}
	s.updateWant()
	return nil
}

// finishInsert は、インサートモードを抜ける（<Esc>）。
//
// 回数つきで入った場合（3ix、3o）は、入力したテキストを残りの回数だけ続けて入れる。o と O は、そのたびに行を開く。
// カーソルを 1 文字左へ戻し、アンドゥのグループを閉じ、入力したテキストを値の Text に埋めてドットリピートに記録する（FR-06）。
// カーソルキーで区切った後、まだ文字を入力していなければ（moved）、区切ったときに閉じて記録してあるので、カーソルを戻すだけにする。
//
// req: FR-06, FR-07
func (s *State) finishInsert() {
	in := s.insert
	text := string(in.text)
	if n := in.repeat - 1; n > 0 && !in.moved {
		if len(text)*n > maxPutBytes {
			s.fail(failf("text too large"))
			n = 0
		}
		open := in.cmd.Action.Builtin() && (in.cmd.Action.ID == "open-below" || in.cmd.Action.ID == "open-above")
		for range n {
			if open {
				s.cursor = s.lineEnd(s.cursor.Line)
				if err := s.typeRune('\n'); err != nil {
					s.fail(err)
					break
				}
			}
			for _, r := range in.text {
				if err := s.typeRune(r); err != nil {
					s.fail(err)
					break
				}
			}
		}
	}
	s.mode = ModeNormal
	if s.cursor.Byte > 0 {
		s.cursor.Byte = prevChar(s.line(s.cursor.Line), s.cursor.Byte)
	}
	s.setCursorNormal(s.cursor)
	for range in.extraGroups {
		s.buf.EndUndoGroup()
	}
	if !in.moved {
		s.buf.EndUndoGroup()
		cmd := in.cmd
		cmd.Text = text
		s.dot = &cmd
	}
	s.insert = insertState{}
}
