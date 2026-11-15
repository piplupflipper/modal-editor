package exthost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/internal/rpc"
)

// Call は、拡張機能 id へリクエスト method を送り、timeout の間だけ応答を待つ（基本設計 5.4）。
// 成功の応答なら、結果の JSON を result（ポインター）へ読み込む。result が nil なら読み捨てる。
//
// 拡張機能が未起動なら起動を始め、起動中なら稼働中になるのを待ち、同じ timeout の中で起動と応答を待つ。
// 再起動待ちと無効の拡張機能へは送らず、すぐに [ErrUnavailable] を包んだ誤りを返す。
//
// timeout を超えたら、[ErrTimeout] を包んだ誤りを返す。リクエストを送った後なら、拡張機能へ $/cancel を送る。
// 期限を超えた後に届いた応答は捨てる（FR-17）。
// 拡張機能が誤りで応答したら、その *rpc.Error を包んだ誤りを返す。
// timeout が 0 以下なら、何も送らずに誤りを返す。待たない問い合わせ（FR-18）には [Host.Send] を使う。
//
// 呼んだ goroutine は応答まで止まる。Host の中の goroutine は呼び出し側を待たないので、
// イベントループの goroutine から呼んでも、拡張機能との通信と監視は止まらない。
//
// req: FR-17
func (h *Host) Call(id, method string, params any, timeout time.Duration, result any) error {
	if timeout <= 0 {
		return fmt.Errorf("exthost: call %s: timeout must be positive, got %s", method, timeout)
	}
	ctx, cancel := context.WithTimeout(h.ctx, timeout)
	defer cancel()
	conn, err := h.waitRunning(ctx, id)
	if err != nil {
		return fmt.Errorf("exthost: call %s on %q: %w", method, id, err)
	}
	if err := conn.Call(ctx, method, params, result); err != nil {
		return fmt.Errorf("exthost: call %s on %q: %w", method, id, h.classify(ctx, err))
	}
	return nil
}

// ResolveMotion は、motion/resolve を期限付きで問い合わせる。誤りの扱いは [Host.Call] と同じである。
// 結果の Version と OK を確かめて適用するかどうかを決めるのは、呼び出し側である。
func (h *Host) ResolveMotion(id string, p ext.MotionResolveParams, timeout time.Duration) (ext.MotionResolveResult, error) {
	var res ext.MotionResolveResult
	err := h.Call(id, ext.MethodMotionResolve, p, timeout, &res)
	return res, err
}

// ResolveTextObject は、textObject/resolve を期限付きで問い合わせる。誤りの扱いは [Host.Call] と同じである。
func (h *Host) ResolveTextObject(id string, p ext.TextObjectResolveParams, timeout time.Duration) (ext.TextObjectResolveResult, error) {
	var res ext.TextObjectResolveResult
	err := h.Call(id, ext.MethodTextObjectResolve, p, timeout, &res)
	return res, err
}

// ApplyOperator は、operator/apply を期限付きで問い合わせる。誤りの扱いは [Host.Call] と同じである。
// 返ったトランザクションは、BaseVersion が現在のバージョン番号と一致した場合だけ適用する（FR-15）。
func (h *Host) ApplyOperator(id string, p ext.OperatorApplyParams, timeout time.Duration) (ext.OperatorApplyResult, error) {
	var res ext.OperatorApplyResult
	err := h.Call(id, ext.MethodOperatorApply, p, timeout, &res)
	return res, err
}

// Send は、拡張機能 id へリクエスト method を送り、応答を待たずに戻る（期限 0 の問い合わせ。FR-18）。
// 結果は、届いた時点で [QueryResult] のイベントとして [Host.Events] へ渡す。tag は、そのイベントの Tag にそのまま入れる。
// イベントループは、tag に問い合わせた時点のバージョン番号とカーソル位置を入れておき、結果が届いたときに照合する。
//
// 拡張機能が未起動なら起動を始め、稼働中になってから送る。
// 拡張機能が無いか、再起動待ちか無効なら、何も送らずに誤りを返す。
// 起動に失敗するか、応答の前に拡張機能が終わったら、Err を設定した QueryResult を渡す。
//
// req: FR-18
func (h *Host) Send(id, method string, params any, tag any) error {
	h.mu.Lock()
	e, err := h.lookupLocked(id)
	if err == nil {
		err = h.ensureStartedLocked(e)
	}
	if err != nil {
		h.mu.Unlock()
		return fmt.Errorf("exthost: send %s to %q: %w", method, id, err)
	}
	h.wg.Add(1)
	h.mu.Unlock()

	go func() {
		defer h.wg.Done()
		qr := &QueryResult{Extension: id, Method: method, Tag: tag}
		qr.Err = h.sendAndWait(id, method, params, &qr.result)
		if qr.Err != nil {
			qr.Err = fmt.Errorf("exthost: send %s to %q: %w", method, id, qr.Err)
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		if !h.closing {
			_ = h.pushLocked(qr, nil)
		}
	}()
	return nil
}

func (h *Host) sendAndWait(id, method string, params any, result *json.RawMessage) error {
	conn, err := h.waitRunning(h.ctx, id)
	if err != nil {
		return err
	}
	f, err := conn.Go(method, params)
	if err != nil {
		return h.classify(h.ctx, err)
	}
	select {
	case <-f.Ready():
	case <-h.ctx.Done():
		_ = f.Cancel()
		return ErrClosed
	}
	if err := f.Result(result); err != nil {
		return h.classify(h.ctx, err)
	}
	return nil
}

// ensureStartedLocked は、未起動の拡張機能を起動する。再起動待ちか無効なら誤りを返す。
func (h *Host) ensureStartedLocked(e *extension) error {
	switch e.state {
	case StateNotStarted:
		h.startLocked(e)
	case StateStarting, StateRunning:
	case StateRestartWait, StateDisabled:
		return fmt.Errorf("%w: state %s", ErrUnavailable, e.state)
	}
	return nil
}

// waitRunning は、拡張機能を必要なら起動し、稼働中になるまで待って、その接続を返す。
// 待つ間に再起動待ちか無効になったら、[ErrUnavailable] を包んだ誤りを返す。
func (h *Host) waitRunning(ctx context.Context, id string) (*rpc.Conn, error) {
	h.mu.Lock()
	e, err := h.lookupLocked(id)
	if err == nil {
		err = h.ensureStartedLocked(e)
	}
	if err != nil {
		h.mu.Unlock()
		return nil, err
	}
	for {
		switch e.state {
		case StateRunning:
			conn := e.run.conn
			h.mu.Unlock()
			return conn, nil
		case StateRestartWait, StateDisabled:
			st := e.state
			h.mu.Unlock()
			return nil, fmt.Errorf("%w: state %s", ErrUnavailable, st)
		case StateNotStarted, StateStarting:
		}
		changed := e.changed
		h.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return nil, h.classify(ctx, ctx.Err())
		}
		h.mu.Lock()
		if h.closing {
			h.mu.Unlock()
			return nil, ErrClosed
		}
	}
}

// classify は、通信の誤りを、このパッケージの誤りの種類で包み直す。
func (h *Host) classify(ctx context.Context, err error) error {
	switch {
	case h.ctx.Err() != nil:
		return fmt.Errorf("%w: %w", ErrClosed, err)
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded):
		return fmt.Errorf("%w: %w", ErrTimeout, err)
	case errors.Is(err, rpc.ErrClosed):
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	return err
}
