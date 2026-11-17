package editor

import (
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/piplupflipper/modal-editor/internal/command"
	"github.com/piplupflipper/modal-editor/internal/editcmd"
	"github.com/piplupflipper/modal-editor/internal/ui"
)

// keyInput は、端末から届いた入力 1 件である。paste が空でなければ貼り付けで、key は読まない。
type keyInput struct {
	key   editcmd.Key
	paste string
}

// keyQueue は、端末から読んだキーと貼り付けの列である。読む goroutine が積み、イベントループが取り出す。
// 上限を持たないのは、イベントループが長い操作の途中でも、読む goroutine が <C-c> を読めるようにするためである。
type keyQueue struct {
	mu    sync.Mutex
	items []keyInput
	// ready は、列が空でないことを知らせる。容量 1 で、取り出した後も残っていれば知らせ直す。
	ready chan struct{}
}

func newKeyQueue() *keyQueue { return &keyQueue{ready: make(chan struct{}, 1)} }

func (q *keyQueue) push(in keyInput) {
	q.mu.Lock()
	q.items = append(q.items, in)
	q.mu.Unlock()
	signal(q.ready)
}

func (q *keyQueue) pop() (keyInput, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return keyInput{}, false
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

// dropThrough は、列の先頭から k までのキー（k を含む）と、その間の貼り付けを捨てる。k が無ければ何もしない。
// 中断（<C-c>）の後に、中断の前に先打ちしたキーを実行しないために使う（Vim と同じ）。
func (q *keyQueue) dropThrough(k editcmd.Key) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, x := range q.items {
		if x.paste == "" && x.key == k {
			q.items = q.items[i+1:]
			return
		}
	}
}

// handleKeys は、列からキーを 1 個取り出して処理する。残りがあれば ready が知らせ直すので、次の周回で処理する。
// 1 個ずつにするのは、キーの間に届いた拡張機能のイベントも、届いた順に近く処理するためである。
func (e *Editor) handleKeys() {
	in, ok := e.keys.pop()
	if !ok {
		return
	}
	if in.paste != "" {
		e.handlePaste(in.paste)
	} else {
		e.handleKey(in.key)
	}
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
	if e.extRan {
		// コマンドラインから実行した拡張機能のコマンドの後は、記録した外での変更の問いを出してよい時点である（askNotice）。
		// ノーマルモードのキーに割り当てたコマンドの後は出さない。続けて打つ l や o を答えに取らないためである（PR #149 の再レビュー）。
		// 出さなかった問い、打ちかけのキーや先読みのキーがあって出せなかった問いは、記録したまま次の安全な時点へ回る。
		e.extRan = false
		if e.fromCmdline {
			e.askNotice()
		}
	}
	e.fromCmdline = false
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
	if e.prompt != nil {
		e.promptKey(k)
		return
	}
	if e.activeChoice() != nil {
		e.choiceKey(k)
		return
	}
	e.endChoices()
	if e.hitEnter() && (k == editcmd.Special(editcmd.CodeEnter) || k == editcmd.Rune(' ')) {
		// Vim の hit-enter の問いと同じく、<CR> と <Space> はメッセージを消すだけで、コマンドとしては使わない。
		// 他のキーは、メッセージを消してからコマンドとして使う。
		e.message = noMessage
		return
	}
	e.message = noMessage
	// 利用者がコマンドラインで打ったキー（:acp.prompt ...<CR> の <CR>）かを覚える。afterKey が、拡張機能のコマンドの後に
	// 記録した問いを出してよいかを決めるのに使う。キーマップの展開で入ったコマンドラインは、利用者のキーがノーマルモードなので数えない。
	e.fromCmdline = e.cur.state.Mode() == command.ModeCommandLine
	for _, k := range e.remap.feed(e.mapMode(), k) {
		if !e.feedKey(k) {
			e.remap.reset()
			break
		}
	}
}

// handlePaste は、端末から貼り付けたテキストを、カーソルのあるバッファへ入れる（#114）。
// 貼り付けはキーマップを通さない（Vim と同じ）。キーマップの接頭辞を待っていれば、時間切れと同じく先に確定させて実行する。
// 確認の問いと choice/ask の問いを出している間の貼り付けは、答えにせずに捨てる。choice/ask の問いでは、捨てたことを数えて出す（#151）。
func (e *Editor) handlePaste(text string) {
	e.popup = nil
	if e.asking() {
		return
	}
	if e.activeChoice() != nil {
		e.dropChoiceInput()
		return
	}
	e.endChoices()
	e.message = noMessage
	for _, k := range e.remap.flush() {
		if !e.feedKey(k) {
			e.remap.reset()
			break
		}
	}
	bs := e.cur
	_ = e.handleResult(bs, bs.state.Paste(text)) // 失敗は handleResult が知らせる
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
	// State を呼んだ入り口によらず、結果を扱い終えた時点で、履歴の先頭が変わっていればホストのクリップボードへ送る（基本設計 4.5）。
	// 動作（:e など）でバッファを切り替えても、レジスターは TakeOver で引き継ぐので、扱い終えた後のカーソルのあるバッファのものを読む。
	defer e.syncClipboard()
	for _, ch := range res.Changes {
		e.commitChange(bs, ch)
	}
	e.trackDeferred(bs, res.Deferred)
	if res.Message.Text != "" {
		// 検索したパターンと、検索の折り返しの警告。後に続く動作と失敗の知らせで上書きされる。
		level := ui.MessageInfo
		if res.Message.Warning {
			level = ui.MessageWarning
		}
		e.message = ui.Message{Text: res.Message.Text, Level: level}
	}
	switched := errors.Is(res.Err, command.ErrBufferSwitched)
	// Ex コマンドの動作（:w など）を先に行い、操作の失敗はその後に知らせる。
	// ex は誤りのコマンドで止まるので、動作はどれも誤りより左のコマンドのものである。
	// 先に知らせると、:w|foo の E492 が :w の「書き込みました」に上書きされて見えない（Vim は :w の後に E492 を出す）。
	// 動作が失敗したら、そこで止めてその誤りを知らせ、右のコマンドの誤りは知らせない（Vim は :w が失敗すると右を実行しない）。
	// :w が外での変更を見つけて問いを出したときも、失敗と同じく右のコマンドの問いと誤りは出さない（runActions）。
	actionFailed := !e.runActions(bs, res.Actions)
	switch {
	case res.Confirm != nil && !actionFailed:
		// Err は確認を求めた E493 なので知らせず、問いを出す（render）。
		// 左の動作が失敗したら問いを出さない（:w! dir|3,1d。Vim 9.1 は :w が失敗すると右のコマンドを実行しない）。
		e.confirm, e.confirmBS = res.Confirm, bs
	case res.Err != nil && !switched && !actionFailed:
		e.reportError(res.Err)
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

// MaxClipboardEncoded は、ホストのクリップボードへ送るテキストを base64 にした後の大きさの上限（バイト）である（仮。基本設計 4.5）。
// 元のテキストでおよそ 750,000 バイトに当たる。tmux 3.4 が base64 で 1 MiB を超える OSC 52 を黙って捨てた実測（PR #170 のレビュー）による。
// 送り出しはイベントループの上で同期に書くので、この上限は書き終わるまで入力と描画が止まる時間も抑える。
const MaxClipboardEncoded = 1_000_000

// syncClipboard は、カーソルのあるバッファのレジスターの履歴の先頭が、前に判定したときから変わっていれば、
// その内容を Terminal.SetClipboard でホストのクリップボードへ送る（FR-56、基本設計 4.5）。
// 変わったかは command.Registers.Pushes の値を clipPushes と比べて判定する。1 回の結果の中で何度変わっても、送るのは最後の先頭だけである。
// 行単位のテキストは、Vim と同じく最後に改行を付けて送る。
// 上限を超えた無名のレジスターに置いただけでは Pushes が変わらないので送らない。
// base64 にした大きさが MaxClipboardEncoded を超えるテキストは送らず、送らなかったことをメッセージの行に出す。
// Config.NoClipboard なら送らない（判定した値だけ進める）。
//
// 切り替えの前のバッファの State に保留した拡張機能の結果が書き込んだレジスターは、カーソルのあるバッファの履歴ではないので送らない。
//
// req: FR-56
func (e *Editor) syncClipboard() {
	if e.cur == nil {
		return
	}
	regs := e.cur.state.Registers()
	n := regs.Pushes()
	if n <= e.clipPushes {
		return
	}
	e.clipPushes = n
	if e.cfg.NoClipboard {
		return
	}
	reg, ok := regs.Head()
	if !ok {
		return
	}
	text := reg.Text
	if reg.Linewise {
		text += "\n"
	}
	if n := base64.StdEncoding.EncodedLen(len(text)); n > MaxClipboardEncoded {
		e.message = ui.Message{
			Text:  fmt.Sprintf("%d バイトのテキストは、base64 で %d バイトになり上限の %d バイトを超えるので、クリップボードへ送りませんでした", len(text), n, MaxClipboardEncoded),
			Level: ui.MessageWarning,
		}
		return
	}
	if err := e.term.SetClipboard(text); err != nil {
		e.log.Error("クリップボードへ送れない", "err", err)
	}
}

// reportError は、操作の失敗を知らせる。
// Vim がビープを鳴らすだけの失敗（command.ErrFailed）は知らせない。
// 期限 0 の結果の破棄（command.ErrStale）は、どの操作の結果かを知る applyDeferred が showDiscarded で知らせるので、ここでは知らせない。
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
