package editor

import (
	"errors"
	"sync"
	"time"

	"github.com/piplupflipper/modal-editor/internal/command"
	"github.com/piplupflipper/modal-editor/internal/editcmd"
)

// keyQueue は、端末から読んだキーの列である。読む goroutine が積み、イベントループが取り出す。
// 上限を持たないのは、イベントループが長い操作の途中でも、読む goroutine が <C-c> を読めるようにするためである。
type keyQueue struct {
	mu    sync.Mutex
	items []editcmd.Key
	// ready は、列が空でないことを知らせる。容量 1 で、取り出した後も残っていれば知らせ直す。
	ready chan struct{}
}

func newKeyQueue() *keyQueue { return &keyQueue{ready: make(chan struct{}, 1)} }

func (q *keyQueue) push(k editcmd.Key) {
	q.mu.Lock()
	q.items = append(q.items, k)
	q.mu.Unlock()
	signal(q.ready)
}

func (q *keyQueue) pop() (editcmd.Key, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return editcmd.Key{}, false
	}
	k := q.items[0]
	q.items = q.items[1:]
	if len(q.items) > 0 {
		signal(q.ready)
	}
	return k, true
}

func (q *keyQueue) empty() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items) == 0
}

// dropThrough は、列の先頭から k までのキー（k を含む）を捨てる。k が無ければ何もしない。
// 中断（<C-c>）の後に、中断の前に先打ちしたキーを実行しないために使う（Vim と同じ）。
func (q *keyQueue) dropThrough(k editcmd.Key) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, x := range q.items {
		if x == k {
			q.items = q.items[i+1:]
			return
		}
	}
}

// handleKeys は、列からキーを 1 個取り出して処理する。残りがあれば ready が知らせ直すので、次の周回で処理する。
// 1 個ずつにするのは、キーの間に届いた拡張機能のイベントも、届いた順に近く処理するためである。
func (e *Editor) handleKeys() {
	k, ok := e.keys.pop()
	if !ok {
		return
	}
	e.handleKey(k)
	e.afterKey()
}

// afterKey は、キーの処理の後に、中断の後始末と、曖昧なキーの列の時間切れの準備をする。
func (e *Editor) afterKey() {
	if e.interrupt.Load() {
		// 処理の途中に <C-c> が押された。中断はもう効いたので印を下ろし、<C-c> までの先打ちを捨てる。
		e.interrupt.Store(false)
		e.keys.dropThrough(interruptKey)
	}
	if e.remap.pendingKeys() || e.cur.state.Pending() {
		e.armKeyTimer()
	} else {
		e.stopKeyTimer()
	}
}

func (e *Editor) armKeyTimer() {
	if e.keyTimer == nil {
		e.keyTimer = time.NewTimer(e.keyTimeout())
	} else {
		e.keyTimer.Reset(e.keyTimeout())
	}
	e.keyTimeC = e.keyTimer.C
}

func (e *Editor) stopKeyTimer() {
	if e.keyTimer != nil {
		e.keyTimer.Stop()
	}
	e.keyTimeC = nil
}

// handleKey は、利用者のキーを 1 個処理する。
func (e *Editor) handleKey(k editcmd.Key) {
	if k == interruptKey {
		e.interrupt.Store(false)
		k = editcmd.Esc
	}
	e.popup = nil
	if e.confirm != nil {
		// 答えは、問いを出したバッファの State へ渡す。問いの間は拡張機能のイベントを取っておくので、そのバッファと範囲は変わっていない。
		cf, bs := e.confirm, e.confirmBS
		e.confirm, e.confirmBS = nil, nil
		e.message = noMessage
		yes := k == editcmd.Rune('y')
		_ = e.handleResult(bs, bs.state.Confirm(cf, yes)) // 失敗は handleResult が知らせる
		return
	}
	if e.activeChoice() != nil {
		e.choiceKey(k)
		return
	}
	e.message = noMessage
	for _, k := range e.remap.feed(e.mapMode(), k) {
		if !e.feedKey(k) {
			e.remap.reset()
			break
		}
	}
}

// flushKeys は、曖昧なキーの列を、時間切れとして確定させて実行する。
func (e *Editor) flushKeys() {
	for _, k := range e.remap.flush() {
		if !e.feedKey(k) {
			break
		}
	}
	_ = e.handleResult(e.cur, e.cur.state.Flush()) // 失敗は handleResult が知らせる
	e.afterKey()
}

// feedKey は、キーを 1 個 command.State へ渡し、結果を処理する。操作が失敗したら false を返す。
// マクロの記録はこの Feed が行うので、コマンドラインのキーも記録される。
func (e *Editor) feedKey(k editcmd.Key) bool {
	bs := e.cur
	return e.handleResult(bs, bs.state.Feed(k)) == nil
}

// mapMode は、利用者のキーマップを引くモードを返す。
// ノーマルモードとビジュアルモードでは、キーの列の解釈の途中（"a、d の後など）では引かない。
func (e *Editor) mapMode() mapMode {
	st := e.cur.state
	switch st.Mode() {
	case command.ModeNormal:
		if !st.Pending() {
			return mapNormal
		}
	case command.ModeVisual, command.ModeVisualLine:
		if !st.Pending() {
			return mapVisual
		}
	case command.ModeInsert:
		return mapInsert
	case command.ModeCommandLine:
		return mapCommandLine
	}
	return mapNone
}

// handleResult は、command.State の結果を処理し、操作の失敗（無ければ nil）を返す。
// 変更を exthost へ送り（FR-16）、表示のデータをずらし、Ex コマンドの動作を行い、失敗を知らせる。
//
// マクロの再生の途中でカーソルのあるバッファが移った（command.ErrBufferSwitched）なら、
// 再生の残りを、移った先のバッファで続ける（Vim と同じく、マクロのキーはカーソルのあるバッファへ入る）。
func (e *Editor) handleResult(bs *bufferState, res command.Result) error {
	for _, ch := range res.Changes {
		e.commitChange(bs, ch)
	}
	e.trackDeferred(bs, res.Deferred)
	switched := errors.Is(res.Err, command.ErrBufferSwitched)
	switch {
	case res.Confirm != nil:
		// Err は確認を求めた E493 なので知らせず、問いを出す（render）。
		e.confirm, e.confirmBS = res.Confirm, bs
	case res.Err != nil && !switched:
		e.reportError(res.Err)
	}
	for _, a := range res.Actions {
		if err := e.doAction(bs, a); err != nil {
			e.showError(err)
			break
		}
	}
	e.doPendingOpens()
	if switched {
		if len(res.Remaining) == 0 || e.cur == bs {
			return nil
		}
		next := e.cur
		return e.handleResult(next, next.state.ContinueReplay(res.Remaining))
	}
	return res.Err
}

// reportError は、操作の失敗を知らせる。
// Vim がビープを鳴らすだけの失敗（command.ErrFailed）と、期限 0 の結果の破棄（command.ErrStale）は知らせない。
// 期限 0 で破棄したことを利用者へ知らせる方法は、要件定義 7 章で未決である。
func (e *Editor) reportError(err error) {
	switch {
	case errors.Is(err, command.ErrFailed), errors.Is(err, command.ErrStale):
		e.log.Debug("操作の失敗", "err", err)
	case errors.Is(err, command.ErrInterrupted):
		e.message = infoMessage("中断しました")
	default:
		e.showError(err)
	}
}
