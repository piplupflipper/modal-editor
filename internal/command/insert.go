package command

import (
	"slices"
	"unicode/utf8"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
	"github.com/piplupflipper/vi-imitation/internal/command/target"
	"github.com/piplupflipper/vi-imitation/internal/editcmd"
)

// insertState は、インサートモードの 1 回分の入力の状態である。
type insertState struct {
	// cmd は、インサートモードへ移った編集コマンドの値である。抜けるときに Inserted を埋めてドットリピートに記録する。
	cmd editcmd.Command
	// text は、入力した文字と削除の操作の列である。<CR> は文字 '\n' として記録する。
	// 削除（<BS> などの insert.delete-*）は文字と別の種類（editcmd.InsertInput.Op）で記録するので、
	// 入力した制御文字（キーの列の展開から届く '\b'、'\x0b'、'\x7f' など）は文字のまま入る（#257 のレビュー）。
	text []editcmd.InsertInput
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
		for _, in := range cmd.Inserted {
			s.insertInput(in)
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
// 移動は insertMove、削除は insertInput に削除の種類（editcmd.InsertDeletePrev など）を渡す。
// 削除は文字と同じ入力の記録（insertState.text）に、文字と別の種類として入れるので、アンドゥの単位、ドットリピート、
// 回数による繰り返し（3i）、カーソルキーで区切った後の入力の始め直し（resumeInsert）は、5 つの削除で同じ扱いになる。
//
// req: FR-127, FR-128
var insertActions = map[string]func(*State){
	"insert.left":               func(s *State) { s.insertMove(editcmd.CodeLeft) },
	"insert.right":              func(s *State) { s.insertMove(editcmd.CodeRight) },
	"insert.up":                 func(s *State) { s.insertMove(editcmd.CodeUp) },
	"insert.down":               func(s *State) { s.insertMove(editcmd.CodeDown) },
	"insert.line-start":         func(s *State) { s.insertMove(editcmd.CodeHome) },
	"insert.line-end":           func(s *State) { s.insertMove(editcmd.CodeEnd) },
	"insert.page-up":            func(s *State) { s.insertMove(editcmd.CodePageUp) },
	"insert.page-down":          func(s *State) { s.insertMove(editcmd.CodePageDown) },
	"insert.delete-prev-char":   func(s *State) { s.insertInput(editcmd.InsertInput{Op: editcmd.InsertDeletePrev}) },
	"insert.delete-char":        func(s *State) { s.insertInput(editcmd.InsertInput{Op: editcmd.InsertDeleteChar}) },
	"insert.delete-to-line-end": func(s *State) { s.insertInput(editcmd.InsertInput{Op: editcmd.InsertDeleteToLineEnd}) },
	"insert.delete-prev-word":   func(s *State) { s.insertInput(editcmd.InsertInput{Op: editcmd.InsertDeletePrevWord}) },
	"insert.delete-to-line-start": func(s *State) {
		s.insertInput(editcmd.InsertInput{Op: editcmd.InsertDeleteToLineStart})
	},
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
	cmd := s.insert.cmd
	cmd.Inserted = slices.Clone(s.insert.text)
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

// insertRune は、入力した文字を記録して、バッファに入れる。制御文字も文字として入れる。
func (s *State) insertRune(r rune) {
	s.insertInput(editcmd.InsertInput{Op: editcmd.InsertRune, Rune: r})
}

// insertInput は、入力（文字か削除の操作）を記録して、バッファに行う。
func (s *State) insertInput(in editcmd.InsertInput) {
	s.resumeInsert()
	s.insert.text = append(s.insert.text, in)
	if err := s.typeInput(in); err != nil {
		s.fail(err)
	}
}

// typeInput は、入力の記録の 1 件をカーソルの位置に行う。
//
//   - 文字（editcmd.InsertRune）は入れる。'\n' は行を分ける。他の制御文字は文字のまま入れる（typeRune）
//   - editcmd.InsertDeletePrev はカーソルの前の文字を消す。行の先頭では前の行とつなぎ、バッファの先頭では何もしない
//     （'backspace' が "indent,eol,start" の動き）
//   - editcmd.InsertDeleteChar はカーソルの文字を消し、editcmd.InsertDeleteToLineEnd はカーソルから行末までを消す。
//     どちらもカーソルは動かない。行末の位置では、どちらも次の行をつなげる（次の行の先頭の空白は残す）。
//     最後の行の行末と空のバッファでは何もしない。InsertDeleteChar は Vim 9.1 のインサートモードの <Del>
//     （'backspace' が "indent,eol,start"）と同じ動きである。InsertDeleteToLineEnd は Vim に対応するキーが無く、
//     行末の位置で次の行をつなげるのは Emacs の C-k に合わせた（#257 の決定）
//   - editcmd.InsertDeletePrevWord はカーソルの前の単語を消す（prevWordStart）。行の先頭では InsertDeletePrev と同じく前の行とつなぐ
//   - editcmd.InsertDeleteToLineStart はカーソルから行の先頭（列 0）までを消す。字下げの手前で止めない。行の先頭では何もしない
//   - InsertDeletePrevWord と InsertDeleteToLineStart は、このインサートで入力した文字より前の文字も消す。
//     Vim 9.1 の <C-w> と <C-u> は、'backspace' が "start" なら入力の始まりで 1 度止まり、<C-u> は行の先頭で前の行とつなぐ
//     （#260 の決定。docs/vim-differences.md）
//
// 消したテキストは、レジスターにも履歴にも入れない。
//
// req: FR-127, FR-128
func (s *State) typeInput(in editcmd.InsertInput) error {
	c := s.cursor
	switch in.Op {
	case editcmd.InsertRune:
		return s.typeRune(in.Rune)
	case editcmd.InsertDeleteChar, editcmd.InsertDeleteToLineEnd:
		line := s.line(c.Line)
		var to buffer.Position
		switch {
		case c.Byte < len(line) && in.Op == editcmd.InsertDeleteChar:
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
	case editcmd.InsertDeletePrev, editcmd.InsertDeletePrevWord, editcmd.InsertDeleteToLineStart:
		var from buffer.Position
		switch {
		case c.Byte > 0 && in.Op == editcmd.InsertDeletePrev:
			from = pos(c.Line, prevChar(s.line(c.Line), c.Byte))
		case c.Byte > 0 && in.Op == editcmd.InsertDeletePrevWord:
			from = s.prevWordStart(c)
		case c.Byte > 0:
			from = pos(c.Line, 0)
		case c.Line > 0 && in.Op != editcmd.InsertDeleteToLineStart:
			from = s.lineEnd(c.Line - 1)
		default:
			return nil
		}
		if err := s.apply(buffer.Edit{Range: buffer.Range{Start: from, End: c}}); err != nil {
			return err
		}
		s.cursor = from
	}
	s.updateWant()
	return nil
}

// prevWordStart は、インサートモードの insert.delete-prev-word で消す範囲の始まりを返す。c は行の先頭より後ろにある。
// ノーマルモードの b（word-prev）と同じ単語の区切りで、カーソルの前の空白を飛ばし、その前の単語の先頭まで戻る（db の範囲）。
// 文字の分類と、結合文字を前の文字と一緒に扱うことも b と同じである。
// b が前の行へ戻るとき（カーソルの前が字下げか空白だけのとき）は、行の先頭で止める。行をまたいでは消さない（#260 の決定）。
//
// req: FR-128
func (s *State) prevWordStart(c buffer.Position) buffer.Position {
	start := pos(c.Line, 0)
	f, ok := target.LookupMotion("word-prev")
	if !ok {
		return start
	}
	res, err := f(s.buf, target.Request{Cursor: c, Count: 1, Columns: s.Columns})
	if err != nil || res.Pos.Line != c.Line {
		return start
	}
	return res.Pos
}

// typeRune は、文字 1 つをカーソルの位置に入れる。'\n' は行を分け、他の文字（制御文字を含む）はそのまま入れる。
func (s *State) typeRune(r rune) error {
	c := s.cursor
	if r == '\n' {
		if err := s.apply(buffer.Edit{Range: emptyRange(c), Text: "\n"}); err != nil {
			return err
		}
		s.cursor = pos(c.Line+1, 0)
	} else {
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
// カーソルを 1 文字左へ戻し、アンドゥのグループを閉じ、入力の記録を値の Inserted に埋めてドットリピートに記録する（FR-06）。
// カーソルキーで区切った後、まだ文字を入力していなければ（moved）、区切ったときに閉じて記録してあるので、カーソルを戻すだけにする。
//
// req: FR-06, FR-07
func (s *State) finishInsert() {
	in := s.insert
	if n := in.repeat - 1; n > 0 && !in.moved {
		size := 0
		for _, x := range in.text {
			size += max(utf8.RuneLen(x.Rune), 1)
		}
		if size*n > maxPutBytes {
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
			for _, x := range in.text {
				if err := s.typeInput(x); err != nil {
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
		cmd.Inserted = slices.Clone(in.text)
		s.dot = &cmd
	}
	s.insert = insertState{}
}
