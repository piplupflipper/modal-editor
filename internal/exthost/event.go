package exthost

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/internal/rpc"
)

// Event は、[Host.Events] からイベントループへ渡すイベントである。
// 値は [EditRequest]、[DecorationSet]、[MessageShow]、[StateChange]、[QueryResult] のポインターのいずれかである。
type Event interface {
	isEvent()
}

// EditRequest は、拡張機能からの buffer/edit のリクエストである（基本設計 5.3）。
//
// イベントループは、Params.Transaction の BaseVersion がバッファの現在のバージョン番号と一致した場合だけ適用し（FR-15）、
// 結果を [EditRequest.Reply] で返す。応答しないと、拡張機能の側の呼び出しは応答を待ち続ける。
type EditRequest struct {
	// Extension は、要求した拡張機能の ID である。
	Extension string
	Params    ext.BufferEditParams

	reply rpc.Replier
}

// Reply は、拡張機能へ buffer/edit の結果を返す。2 回目以降の呼び出しは何も送らずに誤りを返す。
// 拡張機能が既に終わっていれば誤りを返す（返しても害は無い）。
func (r *EditRequest) Reply(result ext.BufferEditResult) error {
	return r.reply(result, nil)
}

// DecorationSet は、拡張機能からの decoration/set の通知である（基本設計 6.1）。
type DecorationSet struct {
	Extension string
	Params    ext.DecorationSetParams
}

// MessageShow は、拡張機能からの message/show の通知である。
type MessageShow struct {
	Extension string
	Params    ext.MessageShowParams
}

// StateChange は、拡張機能の状態が変わったことを知らせる。
// 異常による遷移（再起動待ちと無効）では、Err に異常の内容が入る。利用者への 1 行のメッセージに使う（基本設計 8 章）。
type StateChange struct {
	Extension string
	State     State
	Err       error
}

// QueryResult は、[Host.Send] で送った問い合わせの結果である（FR-18）。
//
// イベントループは、Tag に入れておいたバージョン番号とカーソル位置を現在の値と比べ、
// 同じ場合だけ結果を適用する。違えば破棄する。
type QueryResult struct {
	Extension string
	Method    string
	// Tag は、[Host.Send] に渡した値である。
	Tag any
	// Err は、問い合わせが失敗した理由である。成功したときは nil。
	Err error

	result json.RawMessage
}

// Decode は、成功した結果の JSON を v（ポインター）へ読み込む。失敗した問い合わせなら Err を返す。
func (r *QueryResult) Decode(v any) error {
	if r.Err != nil {
		return r.Err
	}
	if err := json.Unmarshal(r.result, v); err != nil {
		return fmt.Errorf("exthost: decode %s result: %w", r.Method, err)
	}
	return nil
}

func (*EditRequest) isEvent()   {}
func (*DecorationSet) isEvent() {}
func (*MessageShow) isEvent()   {}
func (*StateChange) isEvent()   {}
func (*QueryResult) isEvent()   {}

// queued は、イベントループへ渡す前の列の 1 件である。
// r は、拡張機能から届いたイベントなら、それを受け取ったプロセスである。それ以外は nil。
type queued struct {
	ev Event
	r  *run
}

// errTooManyEvents は、拡張機能からのイベントが列の上限を超えたことを表す。
var errTooManyEvents = errors.New("exthost: too many queued events")

// pushLocked は、イベントを列に積む。r が nil でなければ、r の積んだ数を数え、上限を超えたら積まずに誤りを返す。
func (h *Host) pushLocked(ev Event, r *run) error {
	if r != nil {
		if r.queued >= h.cfg.MaxQueuedEvents {
			return fmt.Errorf("%w: limit %d", errTooManyEvents, h.cfg.MaxQueuedEvents)
		}
		r.queued++
	}
	h.queue = append(h.queue, queued{ev: ev, r: r})
	select {
	case h.wake <- struct{}{}:
	default:
	}
	return nil
}

// purgeLocked は、プロセス r から届いたイベントのうち、まだ渡していないものを捨てる。
// 異常と判断した拡張機能の編集を、後から適用しないためである。
func (h *Host) purgeLocked(r *run) {
	kept := h.queue[:0]
	for _, q := range h.queue {
		if q.r != r {
			kept = append(kept, q)
		}
	}
	clear(h.queue[len(kept):])
	h.queue = kept
	r.queued = 0
}

// pump は、列のイベントを 1 件ずつイベントループへ渡す。Host が閉じたら events を閉じて終わる。
func (h *Host) pump() {
	defer close(h.pumpDone)
	defer close(h.events)
	for {
		h.mu.Lock()
		if len(h.queue) == 0 {
			h.mu.Unlock()
			select {
			case <-h.wake:
				continue
			case <-h.ctx.Done():
				return
			}
		}
		q := h.queue[0]
		h.queue[0] = queued{}
		h.queue = h.queue[1:]
		if q.r != nil {
			q.r.queued--
		}
		h.mu.Unlock()
		select {
		case h.events <- q.ev:
		case <-h.ctx.Done():
			return
		}
	}
}
