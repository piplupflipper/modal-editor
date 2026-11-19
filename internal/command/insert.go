package command

import (
	"github.com/piplupflipper/vi-imitation/internal/buffer"
	"github.com/piplupflipper/vi-imitation/internal/command/target"
	"github.com/piplupflipper/vi-imitation/internal/editcmd"
)

// insertState は、インサートモードの 1 回分の入力の状態である。
type insertState struct {
	// cmd は、インサートモードへ移った編集コマンドの値である。抜けるときに Text を埋めてドットリピートに記録する。
	cmd editcmd.Command
	// text は、入力した文字の列である。<CR> は '\n'、<BS>（insert.delete-prev-char）は '\b'、
	// insert.delete-char は runeDeleteChar、insert.delete-to-line-end は runeDeleteToLineEnd として記録する。
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

// insertKey は、インサートモードのキーを 1 個処理する。キーは次の 2 つの経路に分ける（#257 の決定）。
//
//   - 1 文字の入力の経路: 修飾キーの付かない文字、<CR>、<Tab>、<Esc>。下の switch の各 case で直接処理し、操作の表を引かない。
//     1 文字の入力の速さ（要件定義 8.1 の計測の対象）に、表を引く処理を足さないためである
//   - 操作の経路: それ以外のキー（<BS>、<Del>、カーソルキー、修飾キーの付いたキーなど）。switch を抜けた後で、操作の表の
//     インサートモードの登録（KeyParser.InsertAction）を引き、割り当てた操作を insertAction で実行する（FR-127）。
//     割り当ての無いキーは何もしない
//
// 2 つの経路の分け方は、internal/input の InsertTableKey（インサートモードの登録の左辺の検査）と同じにする。
//
// req: FR-01, FR-127
func (s *State) insertKey(k editcmd.Key) {
	if k.Mod == 0 {
		switch k.Code {
		case editcmd.CodeEsc:
			s.finishInsert()
			return
		case editcmd.CodeEnter:
			s.insertRune('\n')
			return
		case editcmd.CodeTab:
			s.insertRune('\t')
			return
		case editcmd.CodeRune:
			s.insertRune(k.Rune)
			return
		case editcmd.CodeBackspace, editcmd.CodeDelete, editcmd.CodeUp, editcmd.CodeDown, editcmd.CodeLeft, editcmd.CodeRight,
			editcmd.CodeHome, editcmd.CodeEnd, editcmd.CodePageUp, editcmd.CodePageDown, editcmd.CodeInsert, editcmd.CodeF1:
			// 操作の経路へ進む。
		}
	}
	ref, ok := s.parser.InsertAction(k)
	if !ok || !ref.Builtin() {
		// 拡張機能の操作は、インサートモードには登録されない（internal/editor の buildKeymap は既定のモードで登録する）。
		return
	}
	if f, ok := insertActions[ref.ID]; ok {
		f(s)
	}
}

// insertActions は、インサートモードの操作の ID（internal/input の builtinInsert の表）から実装への対応である（FR-127）。
// 移動は insertMove、削除は insertRune に、削除を表す文字（'\b'、runeDeleteChar、runeDeleteToLineEnd）を渡す。
// 削除を文字として入力の記録（insertState.text）に入れるので、アンドゥの単位、ドットリピート、回数による繰り返し（3i）、
// カーソルキーで区切った後の入力の始め直し（resumeInsert）は、<BS> と同じ扱いになる。
//
// req: FR-127
var insertActions = map[string]func(*State){
	"insert.left":               func(s *State) { s.insertMove(editcmd.CodeLeft) },
	"insert.right":              func(s *State) { s.insertMove(editcmd.CodeRight) },
	"insert.up":                 func(s *State) { s.insertMove(editcmd.CodeUp) },
	"insert.down":               func(s *State) { s.insertMove(editcmd.CodeDown) },
	"insert.line-start":         func(s *State) { s.insertMove(editcmd.CodeHome) },
	"insert.line-end":           func(s *State) { s.insertMove(editcmd.CodeEnd) },
	"insert.page-up":            func(s *State) { s.insertMove(editcmd.CodePageUp) },
	"insert.page-down":          func(s *State) { s.insertMove(editcmd.CodePageDown) },
	"insert.delete-prev-char":   func(s *State) { s.insertRune('\b') },
	"insert.delete-char":        func(s *State) { s.insertRune(runeDeleteChar) },
	"insert.delete-to-line-end": func(s *State) { s.insertRune(runeDeleteToLineEnd) },
}

// InsertActionIDs は、実装のあるインサートモードの操作の ID を返す（順は決まらない）。
// internal/editor のテストが、internal/input の操作の表と照合する（同じ層のパッケージどうしはテストからも import しないため）。
func InsertActionIDs() []string {
	ids := make([]string, 0, len(insertActions))
	for id := range insertActions {
		ids = append(ids, id)
	}
	return ids
}

// 入力の記録（insertState.text）の中で、削除の操作を表す文字。'\b'（<BS>、insert.delete-prev-char）と同じく、
// 入力した文字としては現れない制御文字を使う（端末からの貼り付けは制御文字を捨てる。pasteable）。
const (
	// runeDeleteChar は、カーソルの文字を消す操作（<Del>、insert.delete-char）である。DEL の文字にした。
	runeDeleteChar = '\x7f'
	// runeDeleteToLineEnd は、カーソルから行末まで消す操作（insert.delete-to-line-end）である。Emacs の C-k に合わせ、^K の文字にした。
	runeDeleteToLineEnd = '\x0b'
)

// insertMove は、インサートモードのカーソルキーでカーソルを動かす（Vim 9.1 の ins_left() などに当たる）。
// c は既定のキー（<Left> など）で、操作の ID（insert.left など）から insertActions が渡す。設定で別のキーに割り当てても同じく動く。
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
//
// runeDeleteChar はカーソルの文字を消し、runeDeleteToLineEnd はカーソルから行末までを消す。どちらもカーソルは動かない。
// 行末の位置では、どちらも次の行をつなげる（次の行の先頭の空白は残す）。最後の行の行末と空のバッファでは何もしない。
// runeDeleteChar は Vim 9.1 のインサートモードの <Del>（'backspace' が "indent,eol,start"）と同じ動きである。
// runeDeleteToLineEnd は Vim に対応するキーが無く、行末の位置で次の行をつなげるのは Emacs の C-k に合わせた（#257 の決定）。
// 消したテキストは、<BS> と同じくレジスターにも履歴にも入れない。
//
// req: FR-127
func (s *State) typeRune(r rune) error {
	c := s.cursor
	switch r {
	case runeDeleteChar, runeDeleteToLineEnd:
		line := s.line(c.Line)
		var to buffer.Position
		switch {
		case c.Byte < len(line) && r == runeDeleteChar:
			to = pos(c.Line, nextChar(line, c.Byte))
		case c.Byte < len(line):
			to = pos(c.Line, len(line))
		case c.Line < s.lastLine():
			to = pos(c.Line+1, 0)
		default:
			return nil
		}
		if err := s.apply(buffer.Edit{Range: buffer.Range{Start: c, End: to}}); err != nil {
			return err
		}
		s.cursor = c
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
