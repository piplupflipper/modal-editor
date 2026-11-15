package command

import (
	"github.com/piplupflipper/modal-editor/internal/buffer"
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
// 文字、<CR>、<Tab>、<BS>、<Esc> を扱い、それ以外のキー（矢印キー、Ctrl つきの文字など）は無視する。
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
	case editcmd.CodeDelete, editcmd.CodeUp, editcmd.CodeDown, editcmd.CodeLeft, editcmd.CodeRight,
		editcmd.CodeHome, editcmd.CodeEnd, editcmd.CodePageUp, editcmd.CodePageDown:
	}
}

// insertRune は、入力した文字を記録して、バッファに入れる。
func (s *State) insertRune(r rune) {
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
//
// req: FR-06, FR-07
func (s *State) finishInsert() {
	in := s.insert
	text := string(in.text)
	if n := in.repeat - 1; n > 0 {
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
	for range 1 + in.extraGroups {
		s.buf.EndUndoGroup()
	}
	s.registers.inserted = applyBackspaces(text)
	cmd := in.cmd
	cmd.Text = text
	s.dot = &cmd
	s.insert = insertState{}
}

// applyBackspaces は、記録したテキストの '\b' を、その前の文字を消す働きとして解いた文字列を返す（レジスター . の内容）。
func applyBackspaces(text string) string {
	out := make([]rune, 0, len(text))
	for _, r := range text {
		if r == '\b' {
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
			continue
		}
		out = append(out, r)
	}
	return string(out)
}
