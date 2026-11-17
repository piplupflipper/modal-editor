package editor

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/internal/buffer"
	"github.com/piplupflipper/modal-editor/internal/command"
	"github.com/piplupflipper/modal-editor/internal/command/ex"
	"github.com/piplupflipper/modal-editor/internal/ui"
)

// バッファの切り替え（:ls、:b、:bn、:bp、<C-^>）とジャンプリスト（<C-o>、<C-i>）。
//
// # 隠れたバッファ
//
// 開いたバッファは、切り替えて画面から外れても閉じずに読み込んだまま持つ（Vim の 'hidden' がオンのときの隠れたバッファと同じ）。
// 拡張機能へは、開いたときの buffer/open だけを送り、切り替えでは buffer/open も buffer/close も送らない。
// 拡張機能は、画面に出ていないバッファの複製も持ち続ける（LSP の didOpen が続く）。
//
// 未保存の変更のあるバッファから離れる操作（:b、:bn、:bp、<C-^>、別のバッファへの <C-o>）は、
// Vim の 'hidden' がオフの既定と同じく E37 で断る。"!" を付けた :b!、:bn!、:bp! は、変更を残したまま離れる（Vim 9.1 と同じ）。
// 離れたバッファの未保存の変更は、:q が E162 で知らせる。
// 拡張機能の editor/open（定義へ移動など）は、未保存の変更があっても移る（変更は残る）。
//
// # ジャンプリスト
//
// ジャンプリストは Vim のウィンドウごとのものに当たり、ウィンドウが 1 つなのでエディタに 1 つ持つ。
// 項目はバッファの番号と位置で、上限は Vim と同じ 100 件である。
// 規則は Vim 9.1（'jumpoptions' が空）に合わせ、手元の Vim 9.1 で getjumplist() を読んで確かめた。
//
//   - ジャンプ（jumpMotions のモーション、:s、:g、:d、バッファの切り替え、拡張機能の editor/open による移動）の前の位置を末尾に足し、
//     指す位置（jumpIdx）を末尾の次にする。<C-o> で戻った途中からジャンプしても、後ろの項目は消さずに末尾に足す
//   - 同じバッファの同じ行の項目は、新しいものだけを残す（<C-o> と <C-i> の前に整理する。Vim の cleanup_jumplist）
//   - 末尾の次を指しているときの <C-o> は、現在の位置を末尾に足してから戻る。足す前に、末尾の項目が現在の行と同じなら消す
//   - 行の挿入と削除に合わせて行をずらし、消した行の中の項目は消した範囲の最初の行へ寄せる（Vim の mark_adjust）。列は動かさない
type jumpEntry struct {
	buf ext.BufferID
	pos buffer.Position
}

// maxJumps は、ジャンプリストの項目の上限である（Vim の JUMPLISTSIZE と同じ）。
const maxJumps = 100

// 画面に出す誤り（Vim のエラー番号を先頭に付ける）。
var (
	errNoAlternate     = errors.New("E23: 裏バッファがありません")
	errInvalidMarkLine = errors.New("E19: マークの行番号が正しくありません")
	errLeaveChanged    = errors.New("E37: 最後の変更が保存されていません（! を付けると変更を残したまま移ります）")
)

// markJumpAt は、bs の位置 p をジャンプリストの末尾に足す（Vim の setpcmark）。
// 先に、bs の State が処理中の操作で適用した変更を、ずらす対象として取り込む（commitChange）。
// 記録した位置は、それより後の変更でずらす。
func (e *Editor) markJumpAt(bs *bufferState, p buffer.Position) {
	e.syncChanges(bs, bs.state.PendingChanges())
	e.jumps = append(e.jumps, jumpEntry{buf: bs.id, pos: p})
	if n := len(e.jumps); n > maxJumps {
		e.jumps = append([]jumpEntry(nil), e.jumps[n-maxJumps:]...)
	}
	e.jumpIdx = len(e.jumps)
}

// cleanupJumps は、同じバッファの同じ行の項目を、新しいものだけを残して消し、指す位置を同じ項目に保つ（Vim 9.1 の cleanup_jumplist）。
func (e *Editor) cleanupJumps() {
	to := 0
	n := len(e.jumps)
	for from := range n {
		if e.jumpIdx == from {
			e.jumpIdx = to
		}
		dup := false
		for _, later := range e.jumps[from+1:] {
			if later.buf == e.jumps[from].buf && later.pos.Line == e.jumps[from].pos.Line {
				dup = true
				break
			}
		}
		if !dup {
			e.jumps[to] = e.jumps[from]
			to++
		}
	}
	if e.jumpIdx == n {
		e.jumpIdx = to
	}
	e.jumps = e.jumps[:to]
}

// jumpListText は、:jumps の一覧を、Vim 9.1 と同じ形の行を改行でつないで返す。先に整理する（cleanupJumps）。
//
//	 jump line  col file/text
//	   2     1    0 package main
//	   1     7    6 util.go
//	>
//
// 番号は指す位置からの距離、行は 1 始まり、列は 0 始まりのバイト位置である。
// カーソルのあるバッファの項目にはその行の先頭の空白を除いたテキストを、他のバッファの項目にはバッファの名前を出す。
// 指す位置の項目の先頭に ">" を付け、末尾の次を指していれば最後に ">" だけの行を置く。
func (e *Editor) jumpListText() string {
	e.cleanupJumps()
	lines := []string{" jump line  col file/text"}
	for i, j := range e.jumps {
		mark := ' '
		if i == e.jumpIdx {
			mark = '>'
		}
		bs := e.findBuffer(j.buf)
		text := ""
		switch {
		case bs == nil:
		case bs == e.cur && j.pos.Line < bs.buf.LineCount():
			text = strings.TrimLeft(bs.buf.Line(j.pos.Line), " \t")
		default:
			text = e.bufferName(bs)
		}
		dist := i - e.jumpIdx
		if dist < 0 {
			dist = -dist
		}
		lines = append(lines, fmt.Sprintf("%c %2d %5d %4d %s", mark, dist, j.pos.Line+1, j.pos.Byte, text))
	}
	if e.jumpIdx == len(e.jumps) {
		lines = append(lines, ">")
	}
	return strings.Join(lines, "\n")
}

// shiftJumps は、バッファ bs のジャンプリストの項目の行を、変更 ch に合わせてずらす（Vim の mark_adjust）。
func (e *Editor) shiftJumps(bs *bufferState, ch buffer.Change) {
	for i := range e.jumps {
		if e.jumps[i].buf != bs.id {
			continue
		}
		for _, ed := range ch.Edits {
			e.jumps[i].pos = shiftMarkLine(e.jumps[i].pos, ed)
		}
	}
}

// shiftMarkLine は、印の位置 p を編集 ed の後のテキストの位置へずらす。Vim の印と同じく行だけを動かし、列はそのまま残す。
//
//   - 編集より前の行は動かさない。1 行の中の編集（改行を含まない置き換え）でも動かさない
//   - 消した範囲の中の位置は、消した最初の行へ寄せる（Vim の one_adjust_nodel）。範囲が行の途中（前の行の行末）から始まり、
//     位置が範囲の始まりより後の行にあれば、始まりの次の行を最初の行とする。最後の行まで消した dG では、
//     その行はもう無いが、Vim と同じく行を残す。その項目へ移ると、Vim 9.1 と同じく E19 で移らない（jumpBy）
//   - 範囲の終わりより後ろの位置は、増減した行数だけ動かす。範囲の終わりと同じ行で、終わりより後ろにあれば、
//     その行の残りが移った先の行と列へ動かす（行の結合と分割）
func shiftMarkLine(p buffer.Position, ed buffer.Edit) buffer.Position {
	s, end := ed.Range.Start, ed.Range.End
	nl := strings.Count(ed.Text, "\n")
	switch {
	case p.Line < s.Line:
		return p
	case s.Line == end.Line && nl == 0:
		return p
	case p.Less(end):
		if p.Line > s.Line && s.Byte > 0 {
			return buffer.Position{Line: s.Line + 1, Byte: p.Byte}
		}
		return buffer.Position{Line: s.Line, Byte: p.Byte}
	case p.Line > end.Line || end.Byte == 0:
		return buffer.Position{Line: p.Line + s.Line + nl - end.Line, Byte: p.Byte}
	}
	return shiftPos(p, ed)
}

// navigator は、command.Navigator の実装である。バッファごとに 1 つ持ち、そのバッファの State へ渡す。
type navigator struct {
	e  *Editor
	bs *bufferState
	// st は、この navigator を渡した State である。:e が bs の State を作り直した（replaceBuffer）かを、bs.state と比べて知る。
	st *command.State
}

// MarkJump は、command.Navigator.MarkJump である。
func (n navigator) MarkJump(from buffer.Position) { n.e.markJumpAt(n.bs, from) }

// Navigate は、command.Navigator.Navigate である。<C-o>、<C-i>、<C-^> を行う。
func (n navigator) Navigate(a ex.Action, cursor buffer.Position) (buffer.Position, bool, error) {
	e := n.e
	switch {
	case a.Kind == ex.ActionJump:
		return e.jumpBy(n.bs, cursor, a.Count)
	case a.Kind == ex.ActionBuffer && a.Buffer == ex.BufferAlternate:
		target, err := e.alternate(a.Count)
		if err != nil {
			return buffer.Position{}, false, err
		}
		if err := e.gotoBuffer(n.bs, target, false); err != nil {
			return buffer.Position{}, false, err
		}
		if e.cur != n.bs {
			return buffer.Position{}, false, command.ErrBufferSwitched
		}
		return buffer.Position{}, false, nil
	}
	return buffer.Position{}, false, fmt.Errorf("内部の誤り: 移動として渡せない動作 %d", a.Kind)
}

// RunActions は、command.Navigator.RunActions である。マクロの再生の途中の Ex コマンドの動作（:w、:e、:b など）を、その場で行う（#172）。
//
// 動作の前に、再生でまだ知らせていない変更を exthost へ送る（:w が書き出す内容と、移る前のバッファの複製を揃える）。
// :w が外での変更を見つけたら、再生の外と同じく書き込むかを問い（askWrite）、再生はそこで止める。
// Vim 9.1 も問いでは利用者の答えを待つが、y か n の答えの後にマクロの残りを続ける。ここでは残りを捨てる（#174）。
//
// 起動したときの名前の無い空のバッファで :e すると、bufferState は同じまま buf と state を作り直す（replaceBuffer）。
// このときも、呼んだ State は捨てた State なので ErrBufferSwitched を返し、再生の残りを作り直した State で続ける。
func (n navigator) RunActions(acts []ex.Action) (err error) {
	e, bs := n.e, n.bs
	e.syncChanges(bs, n.st.PendingChanges())
	defer func() {
		if bs.state != n.st {
			bs.replacedInReplay = true
			if err == nil {
				err = command.ErrBufferSwitched
			}
		}
	}()
	for i, a := range acts {
		err := e.doAction(bs, a)
		if errors.Is(err, errChangedOnDisk) {
			e.askWrite(bs, a, acts[i+1:])
			return fmt.Errorf("%w: replay stopped at a write prompt", command.ErrFailed)
		}
		if err != nil {
			return err
		}
	}
	if e.cur != bs {
		return command.ErrBufferSwitched
	}
	return nil
}

// jumpBy は、ジャンプリストを count だけ進める（負なら戻る）。bs はカーソルのあるバッファ、cursor はそのカーソルである（Vim 9.1 の movemark）。
// 移る先が bs の中なら、その位置と true を返す。別のバッファなら、切り替えてカーソルを置き、command.ErrBufferSwitched を返す。
// 移る先が無ければ command.ErrFailed を包んだエラーを返す（Vim と同じくビープだけで、メッセージは出さない）。
// 別のバッファへ移るときに bs に未保存の変更があれば E37 を返す。このとき、指す位置は移る先の項目のままにする（Vim 9.1 と同じ）。
// 項目が最後の行より後ろを指していれば E19 を返す。
//
// ジャンプリストは要件定義の FR のどれにも当たらないので、req: の注釈は付けない（#142 の PR #150 のレビュー）。
func (e *Editor) jumpBy(bs *bufferState, cursor buffer.Position, count int) (buffer.Position, bool, error) {
	e.syncChanges(bs, bs.state.PendingChanges())
	e.cleanupJumps()
	if n := len(e.jumps); n > 0 && e.jumpIdx == n && e.jumps[n-1].buf == bs.id && e.jumps[n-1].pos.Line == cursor.Line {
		// 末尾の次を指していて、末尾の項目が現在の行なら、その項目を消す（G<C-o> の後の 3<C-o> などで Vim 9.1 と同じ結果になる）。
		e.jumps = e.jumps[:n-1]
		e.jumpIdx--
	}
	fail := fmt.Errorf("%w: jump list has no entry %+d from %d", command.ErrFailed, count, e.jumpIdx)
	if len(e.jumps) == 0 || e.jumpIdx+count < 0 || e.jumpIdx+count >= len(e.jumps) {
		return buffer.Position{}, false, fail
	}
	if e.jumpIdx == len(e.jumps) {
		// 最初の <C-o> は、戻ってこられるように現在の位置を足してから、足した項目を飛ばして戻る。
		e.markJumpAt(bs, cursor)
		e.jumpIdx--
		if e.jumpIdx+count < 0 {
			return buffer.Position{}, false, fail
		}
	}
	e.jumpIdx += count
	j := e.jumps[e.jumpIdx]
	target := e.findBuffer(j.buf)
	if target == nil {
		return buffer.Position{}, false, fail
	}
	if j.pos.Line >= target.buf.LineCount() {
		// 最後の行より後ろを指す項目（dG で消した行の中の項目）へは移らない。指す位置はその項目のままにする（Vim 9.1 と同じ）。
		return buffer.Position{}, false, errInvalidMarkLine
	}
	if target == bs {
		return j.pos, true, nil
	}
	if bs.buf.Modified() {
		return buffer.Position{}, false, errLeaveChanged
	}
	e.alt = bs
	e.switchTo(target)
	target.state.SetCursor(j.pos)
	return buffer.Position{}, false, command.ErrBufferSwitched
}

// enter は、カーソルのあるバッファを bs にする。別のバッファから移るときは、移る前の位置をジャンプリストに足し、
// 移る前のバッファを裏のバッファ（<C-^> の移り先）にする。
func (e *Editor) enter(bs *bufferState) {
	if e.cur == bs {
		return
	}
	if e.cur != nil {
		e.markJumpAt(e.cur, e.cur.state.Cursor())
		e.alt = e.cur
	}
	e.switchTo(bs)
}

// gotoBuffer は、from（カーソルのあるバッファ）から target へ移る（:b、:bn、:bp、<C-^>）。target が from なら何もしない。
// force が false で from に未保存の変更があれば、E37 で移らない（Vim の 'hidden' がオフの既定と同じ）。
// force が true なら、変更を残したまま移る（Vim 9.1 の :b! と同じ）。
//
// req: FR-08
func (e *Editor) gotoBuffer(from, target *bufferState, force bool) error {
	if target == from {
		return nil
	}
	if !force && from.buf.Modified() {
		return errLeaveChanged
	}
	e.enter(target)
	return nil
}

// alternate は、<C-^> の移り先を返す。n が 0 より大きければ番号 n のバッファ、そうでなければ裏のバッファである。
func (e *Editor) alternate(n int) (*bufferState, error) {
	if n > 0 {
		bs := e.findBuffer(ext.BufferID(n))
		if bs == nil {
			return nil, fmt.Errorf("E92: バッファ %d が見つかりません", n)
		}
		return bs, nil
	}
	if e.alt == nil || e.findBuffer(e.alt.id) != e.alt {
		return nil, errNoAlternate
	}
	return e.alt, nil
}

// bufferAction は、:ls、:b、:bn、:bp を行う。
//
// req: FR-08
func (e *Editor) bufferAction(bs *bufferState, a ex.Action) error {
	switch a.Buffer {
	case ex.BufferList:
		e.message = infoMessage(e.bufferList())
		return nil
	case ex.BufferGo:
		target, err := e.bufferArg(a)
		if err != nil || target == nil {
			return err
		}
		return e.gotoBuffer(bs, target, a.Force)
	case ex.BufferNext, ex.BufferPrev:
		i := 0
		for k, o := range e.bufs {
			if o == bs {
				i = k
			}
		}
		step := a.Count
		if a.Buffer == ex.BufferPrev {
			step = -step
		}
		n := len(e.bufs)
		return e.gotoBuffer(bs, e.bufs[((i+step)%n+n)%n], a.Force)
	case ex.BufferAlternate:
		target, err := e.alternate(a.Count)
		if err != nil {
			return err
		}
		return e.gotoBuffer(bs, target, a.Force)
	}
	return fmt.Errorf("未知のバッファの操作 %d", a.Buffer)
}

// bufferArg は、:b と :bd の引数（ex.Action の Count の番号か Text の名前）に当たるバッファを返す。
// 引数が無ければ nil を返す。番号のバッファが無ければ E86 を返す。
func (e *Editor) bufferArg(a ex.Action) (*bufferState, error) {
	switch {
	case a.Count > 0:
		target := e.findBuffer(ext.BufferID(a.Count))
		if target == nil {
			return nil, fmt.Errorf("E86: バッファ %d はありません", a.Count)
		}
		return target, nil
	case a.Text != "":
		return e.bufferByName(a.Text)
	}
	return nil, nil
}

// deleteBuffer は、:bd を行う。引数のバッファ（無ければ bs）を閉じ、拡張機能へ buffer/close を送り、一覧とジャンプリストから除く。
// "!" が無く、閉じるバッファに未保存の変更があれば E89 で閉じない（Vim 9.1 と同じ）。"!" があれば変更を捨てる。
// カーソルのあるバッファを閉じるときは、裏のバッファ、無ければ一覧の次（無ければ前）のバッファへ移る。
// 最後のバッファを閉じるときは、名前の無い空のバッファを新しい番号で作って移る（Vim と同じ）。
//
// Vim の :bd は、バッファを一覧から外すだけで番号とジャンプリストの項目を残すが、ここでは閉じたバッファを持たないので、
// その項目もジャンプリストから消す。
//
// req: FR-08, FR-16
func (e *Editor) deleteBuffer(bs *bufferState, a ex.Action) error {
	target, err := e.bufferArg(a)
	if err != nil {
		return err
	}
	if target == nil {
		target = bs
	}
	if !a.Force && target.buf.Modified() {
		return fmt.Errorf("E89: バッファ %d の変更は保存されていません (! を付けると破棄します)", target.id)
	}
	if target == e.cur {
		next := e.alt
		if next == nil || next == target || e.findBuffer(next.id) != next {
			next = nil
			i := slices.Index(e.bufs, target)
			switch {
			case i+1 < len(e.bufs):
				next = e.bufs[i+1]
			case i > 0:
				next = e.bufs[i-1]
			}
		}
		if next == nil {
			b := buffer.New("")
			b.SetFixEndOfLine(e.fixEOL)
			next = e.newBuffer(b, "", "")
		}
		e.enter(next)
	}
	if err := e.host.CloseBuffer(target.id); err != nil {
		e.log.Error("buffer/close を送れない", "err", err)
	}
	e.bufs = slices.DeleteFunc(e.bufs, func(o *bufferState) bool { return o == target })
	if e.alt == target {
		e.alt = nil
	}
	if e.deferred != nil && e.deferred.bs == target {
		e.deferred = nil
	}
	kept := e.jumps[:0]
	for i, j := range e.jumps {
		if j.buf == target.id {
			if i < e.jumpIdx {
				e.jumpIdx--
			}
			continue
		}
		kept = append(kept, j)
	}
	e.jumps = kept
	return nil
}

// bufferByName は、:b の名前の引数に当たるバッファを返す（Vim の buflist_findpat を簡単にしたもの）。
// 次の順に探し、ある段で 1 つに決まればそれを、2 つ以上なら E93 を返す。どの段にも無ければ E94 を返す。
//
//  1. 一覧に出す名前（bufferName）か絶対パスが、引数と一致する
//  2. 一覧に出す名前か、ファイル名（パスの最後の要素）が、引数で始まる
//  3. 一覧に出す名前が、引数を含む
//
// Vim と違い、引数をパターン（* や ? のワイルドカード）としては読まない。
func (e *Editor) bufferByName(name string) (*bufferState, error) {
	abs := e.absPath(name)
	levels := []func(bs *bufferState, shown string) bool{
		func(bs *bufferState, shown string) bool { return shown == name || (bs.path != "" && bs.path == abs) },
		func(bs *bufferState, shown string) bool {
			return strings.HasPrefix(shown, name) || (bs.path != "" && strings.HasPrefix(filepath.Base(bs.path), name))
		},
		func(_ *bufferState, shown string) bool { return strings.Contains(shown, name) },
	}
	for _, match := range levels {
		var found []*bufferState
		for _, bs := range e.bufs {
			if match(bs, e.bufferName(bs)) {
				found = append(found, bs)
			}
		}
		switch len(found) {
		case 0:
			continue
		case 1:
			return found[0], nil
		}
		return nil, fmt.Errorf("E93: %s に一致するバッファが複数あります", name)
	}
	return nil, fmt.Errorf("E94: %s に一致するバッファがありません", name)
}

// bufferName は、:ls に出すバッファの名前を返す。作業ディレクトリの下のファイルは作業ディレクトリからの相対パス、
// それ以外は絶対パス、名前の無いバッファは "[No Name]" である（Vim と同じく、ホームディレクトリを ~ にはしない点だけが違う）。
func (e *Editor) bufferName(bs *bufferState) string {
	if bs.path == "" {
		return "[No Name]"
	}
	if rel, err := filepath.Rel(e.cfg.WorkDir, bs.path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return rel
	}
	return bs.path
}

// bufferList は、:ls の一覧を、Vim 9.1 と同じ形の行を改行でつないで返す。
//
//	1 %a   "a.txt"                        line 5
//	2 #h + "b.txt"                        line 1
//
// 印は、番号の後の 1 文字目が % （カーソルのあるバッファ）か # （裏のバッファ）、2 文字目が a （画面に出ている）か
// h （隠れている）、4 文字目が + （未保存の変更がある）である。バッファは閉じないので、どれも a か h になる。
// 行番号は、そのバッファのカーソルの行である。
func (e *Editor) bufferList() string {
	lines := make([]string, 0, len(e.bufs))
	for _, bs := range e.bufs {
		cur, active, mod := ' ', 'h', ' '
		switch bs {
		case e.cur:
			cur, active = '%', 'a'
		case e.alt:
			cur = '#'
		}
		if bs.buf.Modified() {
			mod = '+'
		}
		head := fmt.Sprintf("%3d %c%c %c \"%s\"", bs.id, cur, active, mod, e.bufferName(bs))
		pad := max(40-ui.StringWidth(head, 8), 1)
		lines = append(lines, fmt.Sprintf("%s%sline %d", head, strings.Repeat(" ", pad), bs.state.Cursor().Line+1))
	}
	return strings.Join(lines, "\n")
}
