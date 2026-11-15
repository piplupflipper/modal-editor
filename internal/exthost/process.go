package exthost

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/internal/rpc"
)

// 異常の理由（基本設計 5.2）。StateChange.Err と記録に使う。
var (
	errInitTimeout = errors.New("exthost: initialize timed out")
	errPingTimeout = errors.New("exthost: ping timed out")
	errExited      = errors.New("exthost: extension process exited")
	errConnClosed  = errors.New("exthost: connection closed")
)

// stderrDrainTimeout は、子プロセスを終わらせた後に、標準エラー出力の残りを読み切るのを待つ上限である。
// プロセスグループの外へ抜けた孫プロセスがパイプを持ち続けても、記録の goroutine が残らないようにする。
const stderrDrainTimeout = time.Second

// maxStderrLine は、標準エラー出力の 1 行として記録する上限のバイト数である。超えた分は次の行として記録する。
const maxStderrLine = 64 << 10

// run は、拡張機能のプロセスを 1 回起動した単位である。再起動すると新しい run を作る。
// conn と runningSince は Host.mu が守る。
type run struct {
	conn *rpc.Conn
	// runningSince は、稼働中になった時刻である。稼働中になる前はゼロ値。
	runningSince time.Time
	// queued は、このプロセスから届き、まだイベントループへ渡していないイベントの数である。
	queued int

	failOnce sync.Once
	failErr  error
	// failed は、通信の goroutine が異常を見つけたときに閉じる（イベントの列の上限の超過など）。
	failed chan struct{}
}

func newRun() *run {
	return &run{failed: make(chan struct{})}
}

// fail は、プロセスを異常と判断したことを、監視の goroutine へ知らせる。最初の理由だけを残す。
func (r *run) fail(err error) {
	r.failOnce.Do(func() {
		r.failErr = err
		close(r.failed)
	})
}

// supervise は、拡張機能のプロセスを起動して監視し、異常で終わったら待ち時間の後に再起動する。
// 連続の失敗が上限に達するか、Host が閉じると戻る（基本設計 5.2、図 4）。
//
// req: NFR-03
func (h *Host) supervise(e *extension, r *run) {
	defer h.wg.Done()
	id := e.spec.Manifest.ID
	for {
		err := h.runOnce(e, r)
		if h.ctx.Err() != nil {
			return
		}

		h.mu.Lock()
		if !r.runningSince.IsZero() && time.Since(r.runningSince) >= h.cfg.ResetAfter {
			e.failures = 0
		}
		e.failures++
		e.run = nil
		h.purgeLocked(r)
		if e.failures >= h.cfg.MaxFailures {
			h.setStateLocked(e, StateDisabled, fmt.Errorf("%w (%d consecutive failures)", err, e.failures))
			h.mu.Unlock()
			return
		}
		failures := e.failures
		delay := h.restartDelay(failures)
		h.setStateLocked(e, StateRestartWait, err)
		h.mu.Unlock()

		h.log.Info("extension restart scheduled", "extension", id, "delay", delay, "failures", failures)
		t := time.NewTimer(delay)
		select {
		case <-t.C:
		case <-h.ctx.Done():
			t.Stop()
			return
		}

		h.mu.Lock()
		if h.closing {
			h.mu.Unlock()
			return
		}
		r = newRun()
		e.run = r
		h.setStateLocked(e, StateStarting, nil)
		h.mu.Unlock()
	}
}

// restartDelay は、連続の失敗が failures 回のときの再起動の待ち時間を返す。
func (h *Host) restartDelay(failures int) time.Duration {
	d := h.cfg.RestartDelay
	for i := 1; i < failures && d < h.cfg.MaxRestartDelay; i++ {
		d *= 2
	}
	return min(d, h.cfg.MaxRestartDelay)
}

// runOnce は、子プロセスを 1 回起動し、初期化し、異常が起きるまで監視する。
// 戻る前に、プロセスグループごと強制終了し、接続を閉じる。返す値は異常の理由である。
func (h *Host) runOnce(e *extension, r *run) error {
	id := e.spec.Manifest.ID
	p, err := startProcess(e.spec.command())
	if err != nil {
		return err
	}
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		h.logStderr(id, p.stderr)
	}()

	conn := rpc.NewConn(p.stdout, p.stdin, h.handler(e, r), rpc.WithMaxOutboxBytes(h.cfg.MaxOutboxBytes))
	h.mu.Lock()
	r.conn = conn
	h.mu.Unlock()

	defer func() {
		p.kill()
		_ = conn.Close()
		<-p.exited
		t := time.NewTimer(stderrDrainTimeout)
		select {
		case <-stderrDone:
		case <-t.C:
		}
		t.Stop()
		_ = p.stderr.Close()
		<-stderrDone
	}()

	if err := h.initialize(e, r, p); err != nil {
		return err
	}
	return h.monitor(r, p)
}

// initialize は、initialize を送って応答を待ち、届いたら稼働中にして、開いているバッファの全文を送る。
func (h *Host) initialize(e *extension, r *run, p *process) error {
	f, err := r.conn.Go(ext.MethodInitialize, ext.InitializeParams{APIVersion: ext.APIVersion, Settings: e.spec.Settings})
	if err != nil {
		return fmt.Errorf("%w: %w", errConnClosed, err)
	}
	t := time.NewTimer(h.cfg.InitTimeout)
	defer t.Stop()
	select {
	case <-f.Ready():
	case <-t.C:
		_ = f.Cancel()
		return fmt.Errorf("%w after %s", errInitTimeout, h.cfg.InitTimeout)
	case <-p.exited:
		return p.exitError()
	case <-r.conn.Done():
		return connEnded(r, p)
	case <-r.failed:
		return r.failErr
	case <-h.ctx.Done():
		return ErrClosed
	}
	var res ext.InitializeResult
	if err := f.Result(&res); err != nil {
		var rpcErr *rpc.Error
		if errors.As(err, &rpcErr) {
			return fmt.Errorf("exthost: initialize failed: %w", err)
		}
		return fmt.Errorf("%w: %w", errConnClosed, err)
	}
	id := e.spec.Manifest.ID
	if res.APIVersion != ext.APIVersion {
		// 食い違った場合の扱いは未決なので、記録だけして続ける（基本設計 10 章）。
		h.log.Warn("extension api version differs", "extension", id, "host", ext.APIVersion, "extension_api", res.APIVersion)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closing {
		return ErrClosed
	}
	r.runningSince = time.Now()
	h.setStateLocked(e, StateRunning, nil)
	for _, d := range h.sortedDocsLocked() {
		h.notifyLocked(id, r.conn, ext.MethodBufferOpen, d.openParams())
	}
	return nil
}

// monitor は、稼働中のプロセスへ定期的に ping を送り、異常を見つけたら理由を返す。
func (h *Host) monitor(r *run, p *process) error {
	tick := time.NewTicker(h.cfg.PingInterval)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			if err := h.ping(r, p); err != nil {
				return err
			}
		case <-p.exited:
			return p.exitError()
		case <-r.conn.Done():
			return connEnded(r, p)
		case <-r.failed:
			return r.failErr
		case <-h.ctx.Done():
			return ErrClosed
		}
	}
}

// ping は、生存確認を 1 回行う。応答が期限までに届かなければ誤りを返す。
// 拡張機能が ping に誤りで応答しても、応答したので生きているとみなす。
func (h *Host) ping(r *run, p *process) error {
	f, err := r.conn.Go(ext.MethodPing, ext.PingParams{})
	if err != nil {
		return fmt.Errorf("%w: %w", errConnClosed, err)
	}
	t := time.NewTimer(h.cfg.PingTimeout)
	defer t.Stop()
	select {
	case <-f.Ready():
		if err := f.Result(nil); err != nil && errors.Is(err, rpc.ErrClosed) {
			return fmt.Errorf("%w: %w", errConnClosed, err)
		}
		return nil
	case <-t.C:
		_ = f.Cancel()
		return fmt.Errorf("%w after %s", errPingTimeout, h.cfg.PingTimeout)
	case <-p.exited:
		return p.exitError()
	case <-r.conn.Done():
		return connEnded(r, p)
	case <-r.failed:
		return r.failErr
	case <-h.ctx.Done():
		return ErrClosed
	}
}

// handler は、プロセス r の接続で、拡張機能から届いたリクエストと通知を受け取るハンドラーを作る。
// 読み取りの goroutine から呼ばれるので、Host.mu を短く持つだけで、イベントループを待たない。
func (h *Host) handler(e *extension, r *run) rpc.Handler {
	id := e.spec.Manifest.ID
	return rpc.HandlerFuncs{
		Request: func(_ context.Context, req *rpc.Request, reply rpc.Replier) {
			if req.Method != ext.MethodBufferEdit {
				_ = reply(nil, &rpc.Error{Code: rpc.CodeMethodNotFound, Message: "method not found: " + req.Method})
				return
			}
			var p ext.BufferEditParams
			if err := json.Unmarshal(req.Params, &p); err != nil {
				_ = reply(nil, &rpc.Error{Code: rpc.CodeInvalidParams, Message: err.Error()})
				return
			}
			h.mu.Lock()
			err := h.pushLocked(&EditRequest{Extension: id, Params: p, reply: reply}, r)
			h.mu.Unlock()
			if err != nil {
				r.fail(err)
				_ = reply(nil, err)
			}
		},
		Notification: func(_ context.Context, n *rpc.Notification) {
			var ev Event
			switch n.Method {
			case ext.MethodDecorationSet:
				var p ext.DecorationSetParams
				if !h.decode(id, n, &p) {
					return
				}
				ev = &DecorationSet{Extension: id, Params: p}
			case ext.MethodMessageShow:
				var p ext.MessageShowParams
				if !h.decode(id, n, &p) {
					return
				}
				ev = &MessageShow{Extension: id, Params: p}
			case ext.MethodBufferResync:
				var p ext.BufferResyncParams
				if !h.decode(id, n, &p) {
					return
				}
				h.resync(e, r, p)
				return
			default:
				h.log.Debug("unknown notification from extension", "extension", id, "method", n.Method)
				return
			}
			h.mu.Lock()
			err := h.pushLocked(ev, r)
			h.mu.Unlock()
			if err != nil {
				r.fail(err)
			}
		},
	}
}

// decode は、通知の params を v へ読み込む。読めなければ記録して false を返す。
func (h *Host) decode(id string, n *rpc.Notification, v any) bool {
	if err := json.Unmarshal(n.Params, v); err != nil {
		h.log.Warn("invalid notification params from extension", "extension", id, "method", n.Method, "error", err)
		return false
	}
	return true
}

// logStderr は、拡張機能の標準エラー出力を 1 行ずつ記録する。読み取りが終わるまで戻らない。
func (h *Host) logStderr(id string, r io.Reader) {
	br := bufio.NewReaderSize(r, maxStderrLine)
	for {
		line, err := br.ReadSlice('\n')
		if len(line) > 0 {
			if line[len(line)-1] == '\n' {
				line = line[:len(line)-1]
			}
			h.log.Info("extension stderr", "extension", id, "line", string(line))
		}
		if err != nil && !errors.Is(err, bufio.ErrBufferFull) {
			return
		}
	}
}

// process は、起動した子プロセスと、本体の側のパイプの端である。
type process struct {
	cmd    *exec.Cmd
	stdin  *os.File
	stdout *os.File
	stderr *os.File
	// exited は、子プロセスが終わり、cmd.Wait が戻ると閉じる。waitErr はその後に読む。
	exited  chan struct{}
	waitErr error
}

// startProcess は、path の実行ファイルを、新しいプロセスグループの子プロセスとして起動する。
// 標準入出力は os.Pipe でつなぎ、子の側の端は起動後に閉じる。
func startProcess(path string) (p *process, err error) {
	var opened []*os.File
	defer func() {
		if err != nil {
			for _, f := range opened {
				_ = f.Close()
			}
		}
	}()
	pipe := func() (r, w *os.File, err error) {
		r, w, err = os.Pipe()
		if err == nil {
			opened = append(opened, r, w)
		}
		return r, w, err
	}
	inR, inW, err := pipe()
	if err != nil {
		return nil, err
	}
	outR, outW, err := pipe()
	if err != nil {
		return nil, err
	}
	errR, errW, err := pipe()
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(path) //nolint:gosec // 起動するのは利用者が置いた宣言ファイルの command であり、拡張機能の起動がこのパッケージの役割である。
	cmd.Stdin = inR
	cmd.Stdout = outW
	cmd.Stderr = errW
	// 孫プロセスまでまとめて強制終了できるように、子プロセスを新しいプロセスグループの先頭にする。
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("exthost: start %s: %w", path, err)
	}
	_ = inR.Close()
	_ = outW.Close()
	_ = errW.Close()

	p = &process{cmd: cmd, stdin: inW, stdout: outR, stderr: errR, exited: make(chan struct{})}
	go func() {
		p.waitErr = cmd.Wait()
		close(p.exited)
	}()
	return p, nil
}

// kill は、子プロセスのプロセスグループ全体に SIGKILL を送る（基本設計 5.2 の強制終了）。
// 子プロセスが終わった後でも、同じグループに残った孫プロセスを終わらせる。
// 孫プロセスがパイプを持ち続けると、子を終わらせても接続が閉じないためである。
func (p *process) kill() {
	pid := p.cmd.Process.Pid
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		_ = p.cmd.Process.Kill()
	}
}

// exitWaitAfterEOF は、接続が閉じた後に、子プロセスの終了を待つ上限である。
// 子プロセスが終わると、終了コードより先に標準出力の EOF が届くことが多いので、異常の理由を終了コードにするために少し待つ。
const exitWaitAfterEOF = 200 * time.Millisecond

// connEnded は、接続が閉じたときの異常の理由を返す。子プロセスがすぐに終われば、その終了の理由にする。
func connEnded(r *run, p *process) error {
	t := time.NewTimer(exitWaitAfterEOF)
	defer t.Stop()
	select {
	case <-p.exited:
		return fmt.Errorf("%w (%w)", p.exitError(), r.conn.Err())
	case <-t.C:
		return fmt.Errorf("%w: %w", errConnClosed, r.conn.Err())
	}
}

// exitError は、子プロセスが終わったことを表す誤りを返す。exited が閉じた後に呼ぶ。
func (p *process) exitError() error {
	if p.waitErr != nil {
		return fmt.Errorf("%w: %w", errExited, p.waitErr)
	}
	return fmt.Errorf("%w: exit status 0", errExited)
}
