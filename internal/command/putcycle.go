package command

import (
	"errors"

	"github.com/piplupflipper/modal-editor/internal/buffer"
	"github.com/piplupflipper/modal-editor/internal/editcmd"
)

// putRecord は、直前の貼り付け（p、P、一覧からの貼り付け）の記録である。<C-p> と <C-n> が、貼ったテキストを履歴の別の項目に置き換えるのに使う（FR-55）。
type putRecord struct {
	// after と count は、貼り付けの向き（p なら true）と回数である。置き換えも同じ向きと回数で入れる。
	after bool
	count int
	// before は、貼り付けの前のカーソルである。置き換えは、カーソルをここへ戻してから入れる。
	before buffer.Position
	// index は、いま入っているテキストの、履歴の新しいほうからの添字である。-1 なら orig である。
	index int
	// orig は、名前を指定した貼り付け（"ap）と、上限を超えた無名のレジスターの貼り付けで入れたテキストである。
	// 履歴の項目でないので index を -1 にし、<C-n> で履歴の先頭から戻る先にする。履歴の項目を貼ったときは空（empty）である。
	orig Register
	// rg は、入れたテキストの範囲である。マクロの再生の中の置き換えで、入れたテキストを消すのに使う。
	rg buffer.Range
	// version、pushes、cursor、playDepth は、貼り付けの直後のバッファのバージョン番号、Registers.Pushes、カーソル、マクロの再生の深さである。
	// どれかが変わっていれば、貼り付けの後に別の操作（拡張機能の編集、保留した結果の適用など）が起きたとみて、置き換えを失敗させる。
	version   uint64
	pushes    uint64
	cursor    buffer.Position
	playDepth int
}

// notePut は、貼り付けの記録（putRecord）を残す。name は貼ったレジスターの名前、reg はその内容、before は貼り付けの前のカーソル、
// rg は入れたテキストの範囲である。名前を省略して履歴の先頭を貼ったときは、その位置（添字 0）から置き換えを始める。
//
// req: FR-55
func (s *State) notePut(name rune, reg Register, after bool, count int, before buffer.Position, rg buffer.Range) {
	p := &putRecord{after: after, count: count, before: before, rg: rg, index: -1, orig: reg}
	if s.registers.fromHistory(name) {
		p.index, p.orig = 0, Register{}
	}
	s.markPut(p)
}

// markPut は、p にバッファ、履歴、カーソル、再生の深さの今の値を入れ、直前の貼り付けとして記録する。
func (s *State) markPut(p *putRecord) {
	p.version, p.pushes, p.cursor, p.playDepth = s.buf.Version(), s.registers.Pushes(), s.cursor, s.playDepth
	s.lastPut = p
}

// actPutCycle は、貼り付けの直後の <C-p>（older が true）と <C-n> である（FR-55。基本設計 4.5）。
//
// 直前の貼り付けで入れたテキストを、履歴の 1 つ古い（<C-n> は新しい）項目に置き換える。回数を付けると、その数だけ進む。
// 名前を指定した貼り付け（"ap）の直後の <C-p> は履歴の先頭に置き換え、そこからの <C-n> は元のレジスターの内容に戻す。
// 置き換えは、元の貼り付けと同じ向き、同じ回数で、貼り付けの前のカーソルの位置に入れる。履歴の順は変えない。
// 中身が空の文字単位のテキスト（何も入らない項目）と、いま入っているテキストと同じ内容（テキストと行単位かどうか）の項目は飛ばす。
// 飛ばした先に項目が無ければ失敗する。
// 進む先の項目が貼り付けの上限（maxPutBytes）を超えるなら、元の貼り付けを残したまま、メッセージの行に出す誤りで失敗する。
//
// 置き換えは、元の貼り付けとアンドゥの 1 単位にまとめる。元の貼り付けをアンドゥで取り消してから入れ直すので、
// u の 1 回で貼り付けの前に戻る。マクロの再生の中では、再生全体が 1 つのアンドゥの単位なので、入れたテキストを消してから入れ直す。
//
// 直前の値が貼り付けでなければ失敗する（貼り付けの後に別の操作を挟んだ場合）。進む先の項目が無いときも失敗するが、
// 貼り付けの記録は残すので、続けて反対の向きのキーは使える。ドットリピートの対象は、元の貼り付けのまま変えない。
//
// req: FR-55
func actPutCycle(older bool) actionFunc {
	return func(s *State, cmd editcmd.Command) error {
		p := s.prevPut
		if p == nil || p.version != s.buf.Version() || p.pushes != s.registers.Pushes() ||
			p.cursor != s.cursor || p.playDepth != s.playDepth {
			return failf("no put to replace")
		}
		step := 1
		if !older {
			step = -1
		}
		lowest := 0
		if !p.orig.empty() {
			lowest = -1
		}
		hist := s.registers.History()
		entry := func(i int) Register {
			if i < 0 {
				return p.orig
			}
			return hist[i]
		}
		cur := entry(p.index)
		// 何も入らない項目と、いま入っているテキストと同じ内容の項目は、置き換えても画面が変わらないので飛ばす。
		skip := func(r Register) bool {
			return (r.Text == "" && !r.Linewise) || (r.Text == cur.Text && r.Linewise == cur.Linewise)
		}
		i := p.index
		for range max(cmd.Count, 1) {
			i += step
			for i >= lowest && i < len(hist) && skip(entry(i)) {
				i += step
			}
		}
		if i < lowest || i >= len(hist) {
			s.lastPut = p
			return failf("no more register history")
		}
		if err := checkPutSize(entry(i), p.count); err != nil {
			// 入れられない項目なら、元の貼り付けを取り除く前に止め、バッファを変えない。
			s.lastPut = p
			return errCycleTooLarge
		}
		removed, err := s.unput(p)
		if err != nil {
			return err
		}
		s.cursor = p.before
		s.buf.BeginUndoGroup()
		rg, _, err := s.putText(entry(i), p.after, p.count)
		s.buf.EndUndoGroup()
		if err != nil {
			// 大きさは先に確かめたので、ここで失敗するのはバッファの誤りだけだが、元の貼り付けを戻してから失敗させる。
			s.reput(p, removed)
			return err
		}
		next := *p
		next.index, next.rg = i, rg
		s.markPut(&next)
		return nil
	}
}

// errCycleTooLarge は、貼り直す項目が貼り付けの上限（maxPutBytes）を超えるので、置き換えなかったことを表す。
// ErrFailed を包まないので、呼び出し側（internal/editor）はメッセージの行に出す。
var errCycleTooLarge = errors.New("貼り直す項目が貼り付けの上限を超えるので、置き換えませんでした")

// unput は、直前の貼り付け p で入れたテキストを取り除き、取り除いたテキストを返す（actPutCycle）。
// マクロの再生の外では、貼り付けのアンドゥの単位を取り消す（置き換えを元の貼り付けと 1 つのアンドゥの単位にするため）。
// 再生の中では、再生全体のアンドゥのグループが開いているので、取り消さずに入れた範囲を消す。
func (s *State) unput(p *putRecord) (string, error) {
	if s.playDepth > 0 {
		text, err := s.buf.Slice(p.rg)
		if err != nil {
			return "", err
		}
		return text, s.apply(buffer.Edit{Range: p.rg})
	}
	ch, ok := s.buf.Undo()
	if !ok {
		return "", failf("no put to replace")
	}
	s.res.Changes = append(s.res.Changes, ch)
	return "", nil
}

// reput は、unput で取り除いた貼り付けを戻し、カーソルを貼り付けの直後の位置へ戻す。
// マクロの再生の外ではリドゥで、再生の中では取り除いたテキスト removed を入れ直して戻す。
func (s *State) reput(p *putRecord, removed string) {
	if s.playDepth > 0 {
		_ = s.apply(buffer.Edit{Range: emptyRange(p.rg.Start), Text: removed}) // 消した範囲に戻すだけなので失敗しない
	} else if ch, ok := s.buf.Redo(); ok {
		s.res.Changes = append(s.res.Changes, ch)
	}
	s.cursor = p.cursor
}

// PutFromList は、レジスターの一覧（:registers）で選んだ項目 e を、p と同じくカーソルの後に貼る（FR-54。基本設計 4.5）。
// 履歴の項目は、履歴の先頭へ移してから（Registers.Promote）、名前を省略した p として貼る。名前付きのレジスターは "ap として貼る。
// どちらも p と同じくドットリピートの対象になり、直後の <C-p> と <C-n> で置き換えられる（FR-55）。
// pushes は一覧を作ったときの Registers.Pushes で、その後に履歴の先頭が変わっていれば、選んだ項目が別の項目を指しうるので失敗する。
// ノーマルモードでなければ失敗する。
//
// req: FR-54
func (s *State) PutFromList(e RegisterEntry, pushes uint64) Result {
	s.res = Result{}
	switch {
	case s.mode != ModeNormal || s.parser.Pending():
		s.fail(failf("put from the register list outside normal mode"))
		return s.take()
	case pushes != s.registers.Pushes():
		s.fail(errListStale)
		return s.take()
	}
	cmd := editcmd.Command{Kind: editcmd.KindAction, Action: editcmd.Ref{ID: "put-after"}, Count: 1, Register: e.Name}
	if e.Name == 0 && !s.registers.Promote(e.Index) {
		s.fail(errListStale)
		return s.take()
	}
	s.run(cmd)
	return s.take()
}

// errListStale は、一覧を出した後にレジスターの履歴が変わったので、一覧から貼れないことを表す。
// ErrFailed を包まないので、呼び出し側（internal/editor）はメッセージの行に出す。
var errListStale = errors.New("一覧を出した後にレジスターの履歴が変わったので貼れません。:registers で出し直してください")
