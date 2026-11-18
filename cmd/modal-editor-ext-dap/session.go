package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// clientAddrPlaceholder は、デバッグアダプターの引数の中で、拡張機能が待ち受けるソケットのアドレスに置き換える文字列である。
// 引数のどれかがこれを含むと、拡張機能は Unix ドメインソケットで待ち受け、デバッグアダプターからの接続を受けて DAP を話す。
// 含まなければ、デバッグアダプターの標準入出力で DAP を話す。
//
// delve の dlv dap は TCP かソケットでしか DAP を話さず、--client-addr で指定したアドレスへ自分から接続する
// （https://github.com/go-delve/delve/blob/master/Documentation/usage/dlv_dap.md）。
// アドレスは "unix:" に続けてソケットのパスを書く形で、dlv の --client-addr の書き方に合わせる。
const clientAddrPlaceholder = "${clientAddr}"

// outputChannel は、output/append で書き足す欄の名前である（基本設計 6.4）。
const outputChannel = "dap"

// session は、1 回のデバッグである。デバッグアダプターの子プロセスと、それとの DAP の接続を持つ。
//
// 開始の手順（run）は DAP の仕様の Overview の Launch Sequencing に従う。
// initialize の response を受け取った後に launch を送り、initialized の event を待って、
// ブレークポイントを setBreakpoints で送り、configurationDone を送ってから launch の response を待つ。
// デバッグアダプターは configurationDone の response の後に launch に応答してよい（同 Overview）ので、
// launch の response を待ってから設定を送ると詰まる。
type session struct {
	d   *debugger
	cmd *exec.Cmd
	// ln は、ソケットで話すときに待ち受けるソケットである。標準入出力で話すときは nil。
	ln      *net.UnixListener
	sockDir string
	// stdio は、標準入出力で話すときの、子プロセスの標準入力への書き込み口と標準出力の読み取り口である。
	stdinW  *os.File
	stdoutR *os.File

	conn   *dapConn
	connMu sync.Mutex

	events *eventQueue
	// initialized は、initialized の event を受け取ると閉じる。
	initialized     chan struct{}
	initializedOnce sync.Once
	// procDone は、子プロセスが終わると閉じる。
	procDone chan struct{}

	// caps は、initialize の応答の capabilities である。stop が別の goroutine から読むので atomic にする。
	caps atomic.Pointer[capabilities]
	// terminatedCh は、terminated の event を受け取ると閉じる。
	terminatedCh   chan struct{}
	terminatedOnce sync.Once
	// stopping は、利用者が debug.terminate か debug.restart で終わらせたか、拡張機能が終わるところであることを表す。
	stopping atomic.Bool
	// terminated は、terminated の event を受け取ったことを表す。
	terminated atomic.Bool
	// lastThread は、最後に止まったスレッドの ID である。0 は未知。
	lastThread atomic.Int64
	// ended は、後始末が終わると閉じる。
	ended chan struct{}
	// eventCtx は、event の処理（stopped）が本体とデバッグアダプターへ送る request の ctx の親である。
	// stop の始めと後始末の終わりに cancelEvents で取り消す。止める間に event の処理が本体の応答を待ち続けると、
	// その後ろに並んだ terminated の event を処理できず、stop が期限まで待つからである。
	// 本体から shutdown を受け取った後は、Server のループが止まるので、WaitBuffer などは応答しない。
	eventCtx     context.Context
	cancelEvents context.CancelFunc
}

// linesStartAt1 は、initialize で送る linesStartAt1 と columnsStartAt1 である。
// DAP の既定に合わせて 1 から数え、受け取った値は fromDAPLine と utf16ColumnToByte で本体の位置へ変換する。
const linesStartAt1 = true

// spawn は、設定 cfg のデバッグアダプターを子プロセスとして起動する。DAP の初期化はまだ行わない（run が行う）。
// 起動できなければ誤りを返し、子プロセスとソケットを残さない。
func spawn(d *debugger, cfg adapterConfig) (*session, error) {
	if cfg.Command == "" {
		return nil, errors.New("adapter command is empty")
	}
	s := &session{
		d:            d,
		events:       newEventQueue(),
		initialized:  make(chan struct{}),
		terminatedCh: make(chan struct{}),
		procDone:     make(chan struct{}),
		ended:        make(chan struct{}),
	}
	args := slices.Clone(cfg.Args)
	useSocket := false
	for _, a := range args {
		if strings.Contains(a, clientAddrPlaceholder) {
			useSocket = true
		}
	}
	if useSocket {
		dir, err := os.MkdirTemp("", "modal-editor-ext-dap-")
		if err != nil {
			return nil, err
		}
		path := filepath.Join(dir, "dap.sock")
		ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
		if err != nil {
			_ = os.RemoveAll(dir)
			return nil, err
		}
		s.ln, s.sockDir = ln, dir
		for i, a := range args {
			args[i] = strings.ReplaceAll(a, clientAddrPlaceholder, "unix:"+path)
		}
	}
	// 子プロセスの実行ファイルと引数は、利用者の設定から来る。拡張機能はそれを起動するためにある。
	cmd := exec.Command(cfg.Command, args...) //nolint:gosec // 利用者が設定したデバッグアダプターを起動する
	cmd.Dir = cfg.Cwd
	s.cmd = cmd
	var parentEnds, childEnds []*os.File
	closeAll := func(fs []*os.File) {
		for _, f := range fs {
			_ = f.Close()
		}
	}
	fail := func(err error) (*session, error) {
		closeAll(parentEnds)
		closeAll(childEnds)
		s.closeSocket()
		return nil, err
	}
	if useSocket {
		// ソケットで話すときは、子プロセスの標準出力と標準エラー出力をデバッグ対象の出力として欄へ書き足す。
		// dlv dap は、デバッグ対象の標準出力と標準エラー出力を output の event で送らず、自分の標準出力と標準エラー出力へ書く
		// （https://github.com/go-delve/delve/blob/master/Documentation/api/dap/README.md）。
		r, w, err := os.Pipe()
		if err != nil {
			return fail(err)
		}
		parentEnds, childEnds = append(parentEnds, r), append(childEnds, w)
		cmd.Stdout, cmd.Stderr = w, w
		go s.forward(r, func(text string) { d.appendOutput(text) })
	} else {
		inR, inW, err := os.Pipe()
		if err != nil {
			return fail(err)
		}
		parentEnds, childEnds = append(parentEnds, inW), append(childEnds, inR)
		outR, outW, err := os.Pipe()
		if err != nil {
			return fail(err)
		}
		parentEnds, childEnds = append(parentEnds, outR), append(childEnds, outW)
		errR, errW, err := os.Pipe()
		if err != nil {
			return fail(err)
		}
		parentEnds, childEnds = append(parentEnds, errR), append(childEnds, errW)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = inR, outW, errW
		s.stdinW, s.stdoutR = inW, outR
		// 標準入出力で話すときは、標準エラー出力はデバッグアダプターのログなので、拡張機能のログ（本体が記録する）へ書く。
		go s.forward(errR, func(text string) { d.logf("adapter: %s", strings.TrimRight(text, "\n")) })
	}
	if err := startAdapter(cmd); err != nil {
		return fail(err)
	}
	s.eventCtx, s.cancelEvents = context.WithCancel(context.Background())
	closeAll(childEnds)
	go func() {
		_ = cmd.Wait()
		close(s.procDone)
		// 子プロセスが終わったら、接続とソケットを閉じ、読み取りと Accept を終わらせる。
		s.closeSocket()
		if c := s.getConn(); c != nil {
			c.close()
		}
	}()
	return s, nil
}

// forward は、r から読んだテキストを、塊ごとに f へ渡す。r が終わるまで続ける。
func (s *session) forward(r io.ReadCloser, f func(string)) {
	defer r.Close() //nolint:errcheck // 読み取りの口を閉じるだけで、失敗しても失うデータが無い
	buf := make([]byte, 32<<10)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			f(string(buf[:n]))
		}
		if err != nil {
			return
		}
	}
}

func (s *session) closeSocket() {
	if s.ln != nil {
		_ = s.ln.Close()
	}
	if s.sockDir != "" {
		_ = os.RemoveAll(s.sockDir)
	}
}

func (s *session) getConn() *dapConn {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	return s.conn
}

// connect は、DAP の接続を作る。ソケットで話すときは、デバッグアダプターからの接続を timeout まで待つ。
func (s *session) connect(timeout time.Duration) error {
	onEvent := func(event string, body json.RawMessage) { s.events.push(dapEvent{name: event, body: body}) }
	var c *dapConn
	if s.ln == nil {
		c = newDAPConn(s.stdoutR, s.stdinW, multiCloser{s.stdinW, s.stdoutR}, onEvent)
	} else {
		if err := s.ln.SetDeadline(time.Now().Add(timeout)); err != nil {
			return err
		}
		nc, err := s.ln.Accept()
		s.closeSocket()
		if err != nil {
			return fmt.Errorf("accept connection from adapter: %w", err)
		}
		c = newDAPConn(nc, nc, nc, onEvent)
	}
	s.connMu.Lock()
	s.conn = c
	s.connMu.Unlock()
	select {
	case <-s.procDone:
		// 子プロセスが接続の前に終わっていたら、後始末の goroutine は接続を閉じられないので、ここで閉じる。
		c.close()
	default:
	}
	return nil
}

type multiCloser []io.Closer

func (m multiCloser) Close() error {
	var errs []error
	for _, c := range m {
		errs = append(errs, c.Close())
	}
	return errors.Join(errs...)
}

// run は、DAP の初期化から終わりまでを行う。spawn の後に別の goroutine で呼ぶ。
// launch は launch の引数である。
func (s *session) run(launch map[string]any) {
	handled := make(chan struct{})
	go func() {
		defer close(handled)
		s.handleEvents()
	}()
	err := s.start(launch)
	if err != nil {
		s.d.showError("デバッグを開始できない: " + err.Error())
		s.kill()
	} else {
		s.d.showInfo("デバッグを開始した")
	}
	if c := s.getConn(); c != nil {
		<-c.doneCh()
	}
	<-s.procDoneOrTimeout()
	s.kill()
	s.events.close()
	<-handled
	abnormal := err == nil && !s.stopping.Load() && !s.terminated.Load()
	s.d.sessionEnded(s, abnormal, err != nil)
	s.cancelEvents()
	close(s.ended)
}

// procDoneOrTimeout は、子プロセスが終わるか、stop の期限が過ぎると閉じる通り道を返す。
func (s *session) procDoneOrTimeout() <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		select {
		case <-s.procDone:
		case <-time.After(s.d.timeouts.stop):
		}
		close(ch)
	}()
	return ch
}

// start は、DAP の初期化の手順を行う。
func (s *session) start(launch map[string]any) error {
	t := s.d.timeouts
	if err := s.connect(t.initialize); err != nil {
		return err
	}
	c := s.getConn()
	ctx, cancel := context.WithTimeout(context.Background(), t.initialize)
	defer cancel()
	var caps capabilities
	err := c.call(ctx, "initialize", initializeArguments{
		ClientID:        "modal-editor",
		ClientName:      "modal-editor",
		AdapterID:       s.d.settings.AdapterID,
		LinesStartAt1:   linesStartAt1,
		ColumnsStartAt1: linesStartAt1,
		PathFormat:      "path",
	}, &caps)
	if err != nil {
		return err
	}
	s.caps.Store(&caps)

	launchCtx, cancelLaunch := context.WithTimeout(context.Background(), t.launch)
	defer cancelLaunch()
	launched := make(chan error, 1)
	go func() { launched <- c.call(launchCtx, "launch", launch, nil) }()

	select {
	case <-s.initialized:
	case err := <-launched:
		// launch に失敗の response が返ったか、接続が終わった。
		if err != nil {
			return err
		}
		// launch の response が initialized より先に届いた。initialized を待ち続ける。
		launched <- nil
		select {
		case <-s.initialized:
		case <-launchCtx.Done():
			return errors.New("initialized event did not arrive")
		}
	case <-launchCtx.Done():
		return errors.New("initialized event did not arrive")
	}

	for _, path := range s.d.configure(s) {
		if err := s.sendBreakpoints(launchCtx, path); err != nil {
			s.d.logf("setBreakpoints %s: %v", path, err)
		}
	}
	if caps.SupportsConfigurationDoneRequest {
		if err := c.call(launchCtx, "configurationDone", nil, nil); err != nil {
			return err
		}
	}
	return <-launched
}

// sendBreakpoints は、path のブレークポイントのすべてを setBreakpoints で送り、応答で印を付け直す。
func (s *session) sendBreakpoints(ctx context.Context, path string) error {
	c := s.getConn()
	if c == nil {
		return errDAPClosed
	}
	bps, gen := s.d.snapshotBreakpoints(path)
	args := setBreakpointsArguments{Source: source{Name: filepath.Base(path), Path: path}, Breakpoints: []sourceBreakpoint{}}
	for _, b := range bps {
		args.Breakpoints = append(args.Breakpoints, sourceBreakpoint{Line: toDAPLine(b.line, linesStartAt1), Condition: b.cond})
	}
	var res setBreakpointsResponse
	if err := c.call(ctx, "setBreakpoints", args, &res); err != nil {
		return err
	}
	s.d.applyBreakpoints(path, gen, res.Breakpoints)
	return nil
}

// threadID は、続行やステップ実行の対象のスレッドを返す。止まったスレッドを知らなければ threads で問い合わせ、最初のスレッドにする。
func (s *session) threadID(ctx context.Context) (int, error) {
	if id := s.lastThread.Load(); id != 0 {
		return int(id), nil
	}
	var res threadsResponse
	if err := s.getConn().call(ctx, "threads", nil, &res); err != nil {
		return 0, err
	}
	if len(res.Threads) == 0 {
		return 0, errors.New("no threads")
	}
	return res.Threads[0].ID, nil
}

// resume は、command（continue、next、stepIn、stepOut）を送る。
func (s *session) resume(ctx context.Context, command string) error {
	c := s.getConn()
	if c == nil {
		return errDAPClosed
	}
	tid, err := s.threadID(ctx)
	if err != nil {
		return err
	}
	return c.call(ctx, command, threadArguments{ThreadID: tid}, nil)
}

// pause は、DAP の pause を送る。実行中は止まったスレッドを知らないので、threads の最初のスレッドを対象にする。
// delve は、pause の threadId に依らずにデバッグ対象の全体を止める。
func (s *session) pause(ctx context.Context) error {
	c := s.getConn()
	if c == nil {
		return errDAPClosed
	}
	var res threadsResponse
	if err := c.call(ctx, "threads", nil, &res); err != nil {
		return err
	}
	tid := 1
	if len(res.Threads) > 0 {
		tid = res.Threads[0].ID
	}
	return c.call(ctx, "pause", threadArguments{ThreadID: tid}, nil)
}

// stop は、disconnect を送ってデバッグ対象とデバッグアダプターを終わらせる。
// 期限までに子プロセスが終わらなければ強制終了する（kill）。後始末が終わるまで待つ。
//
// デバッグアダプターが supportsTerminateRequest を持てば、先に terminate を送り、デバッグ対象を穏やかに終わらせる
// （DAP の仕様の Overview の Debug Session End）。期限までに子プロセスが終わらなければ disconnect を送る。
//
// 手順ごとの期限は timeouts.stop である。ctx が終わると、残りの手順を打ち切って強制終了する。
// grace は、強制終了で SIGTERM を送ってから SIGKILL を送るまでの猶予である。
// ctx が終わった後に stop が戻るまでは、最長で grace に少し足した時間である。
// ctx が終わって強制終了したときは、run の後始末（sessionEnded）を待たずに戻る。
func (s *session) stop(ctx context.Context, grace time.Duration) {
	s.stopping.Store(true)
	s.cancelEvents()
	s.terminate(ctx)
	s.disconnect(ctx, grace)
	wait := time.NewTimer(2 * s.d.timeouts.stop)
	defer wait.Stop()
	select {
	case <-s.ended:
	case <-wait.C:
		s.killWithin(grace)
	case <-ctx.Done():
		s.killWithin(grace)
	}
}

// terminate は、デバッグアダプターが対応していれば terminate を送り、terminated の event か子プロセスの終了を、
// timeouts.stop と ctx の早い方まで待つ。
func (s *session) terminate(ctx context.Context) {
	c := s.getConn()
	if caps := s.caps.Load(); c == nil || caps == nil || !caps.SupportsTerminateRequest {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, s.d.timeouts.stop)
	defer cancel()
	if err := c.call(ctx, "terminate", nil, nil); err != nil {
		s.d.logf("terminate: %v", err)
		return
	}
	select {
	case <-s.terminatedCh:
	case <-s.procDone:
	case <-ctx.Done():
	}
}

// disconnect は、disconnect を送り、子プロセスが終わるのを timeouts.stop と ctx の早い方まで待ち、
// 終わらなければ猶予 grace で強制終了する。
func (s *session) disconnect(ctx context.Context, grace time.Duration) {
	if c := s.getConn(); c != nil {
		cctx, cancel := context.WithTimeout(ctx, s.d.timeouts.stop)
		err := c.call(cctx, "disconnect", disconnectArguments{TerminateDebuggee: true}, nil)
		cancel()
		if err != nil && !errors.Is(err, errDAPClosed) && !s.exited() {
			s.d.logf("disconnect: %v", err)
		}
	}
	wait := time.NewTimer(s.d.timeouts.stop)
	defer wait.Stop()
	select {
	case <-s.procDone:
	case <-wait.C:
		s.killWithin(grace)
	case <-ctx.Done():
		s.killWithin(grace)
	}
}

// exited は、子プロセスが終わったかどうかを返す。
func (s *session) exited() bool {
	select {
	case <-s.procDone:
		return true
	default:
		return false
	}
}

// kill は、猶予を killGrace にして killWithin を呼ぶ。
func (s *session) kill() { s.killWithin(killGrace) }

// killWithin は、子プロセスに SIGTERM を送り、grace までに終わらなければ SIGKILL を送り、接続を閉じる。終わっていれば何もしない。
// 先に SIGTERM を送るのは、dlv が SIGTERM でデバッグ対象を終わらせ、ビルドした実行ファイルを消すからである（startAdapter の doc）。
// SIGKILL だと、別のプロセスグループのデバッグ対象が孤児で残る。
func (s *session) killWithin(grace time.Duration) {
	select {
	case <-s.procDone:
	default:
		if s.cmd.Process != nil {
			_ = s.cmd.Process.Signal(syscall.SIGTERM)
			t := time.NewTimer(grace)
			select {
			case <-s.procDone:
			case <-t.C:
				_ = s.cmd.Process.Kill()
			}
			t.Stop()
		}
	}
	if c := s.getConn(); c != nil {
		c.close()
	}
	s.closeSocket()
}

// killGrace は、kill で SIGTERM を送ってから SIGKILL を送るまでの猶予である。値は仮。
const killGrace = 2 * time.Second

// handleEvents は、デバッグアダプターからの event を届いた順に 1 件ずつ処理する。
// 読み取りの goroutine とは別の goroutine で動くので、stopped の処理で stackTrace の応答を待てる。
func (s *session) handleEvents() {
	for {
		e, ok := s.events.pop()
		if !ok {
			return
		}
		s.handleEvent(e)
	}
}

func (s *session) handleEvent(e dapEvent) {
	switch e.name {
	case "initialized":
		s.initializedOnce.Do(func() { close(s.initialized) })
	case "stopped":
		var body stoppedEvent
		if err := json.Unmarshal(e.body, &body); err != nil {
			s.d.logf("stopped event: %v", err)
			return
		}
		s.stopped(&body)
	case "continued":
		s.d.clearExecution()
	case "exited":
		var body exitedEvent
		if err := json.Unmarshal(e.body, &body); err == nil {
			s.d.showInfo(fmt.Sprintf("デバッグ対象が終了した（終了コード %d）", body.ExitCode))
		}
	case "terminated":
		s.terminated.Store(true)
		s.terminatedOnce.Do(func() { close(s.terminatedCh) })
		s.d.clearExecution()
		// デバッグアダプターがセッションを終えた。disconnect を送ってデバッグアダプターを終わらせる。
		// debug.terminate で終わらせている最中なら、stop が disconnect を送るので、ここでは送らない（二重に送ると、閉じた接続への書き込みになる）。
		if !s.stopping.Load() {
			go s.disconnect(context.Background(), killGrace)
		}
	case "output":
		var body outputEvent
		if err := json.Unmarshal(e.body, &body); err != nil {
			s.d.logf("output event: %v", err)
			return
		}
		// telemetry は利用者に見せずにテレメトリへ送る種類である（仕様の OutputEvent の category）。
		if body.Category == "telemetry" {
			return
		}
		s.d.appendOutput(body.Output)
	default:
		// breakpoint、thread、module などの event は、最初の範囲では使わない。
	}
}

// stopped は、stopped の event を処理する。止まったスレッドの先頭のフレームの位置へカーソルを移し、実行位置の印を付ける。
func (s *session) stopped(body *stoppedEvent) {
	ctx, cancel := context.WithTimeout(s.eventCtx, s.d.timeouts.request)
	defer cancel()
	tid := 0
	if body.ThreadID != nil {
		tid = *body.ThreadID
		s.lastThread.Store(int64(tid))
	} else {
		s.lastThread.Store(0)
		id, err := s.threadID(ctx)
		if err != nil {
			s.d.logf("threads: %v", err)
			s.d.stoppedWithoutSource(body.Reason)
			return
		}
		tid = id
		s.lastThread.Store(int64(tid))
	}
	var st stackTraceResponse
	err := s.getConn().call(ctx, "stackTrace", stackTraceArguments{ThreadID: tid, StartFrame: 0, Levels: 1}, &st)
	if err != nil {
		s.d.logf("stackTrace: %v", err)
	}
	if err != nil || len(st.StackFrames) == 0 || st.StackFrames[0].Source == nil || st.StackFrames[0].Source.Path == "" {
		s.d.stoppedWithoutSource(body.Reason)
		return
	}
	f := st.StackFrames[0]
	line := fromDAPLine(f.Line, linesStartAt1)
	s.d.showStopped(ctx, f.Source.Path, line, f.Column, body.Reason)
}

// dapEvent は、受け取った 1 件の event である。
type dapEvent struct {
	name string
	body json.RawMessage
}

// eventQueue は、読み取りの goroutine から event を処理する goroutine へ渡す、上限の無い列である。
// 読み取りの goroutine を止めない（応答を読み続ける）ために、積む側は待たない。
type eventQueue struct {
	mu     sync.Mutex
	items  []dapEvent
	closed bool
	wake   chan struct{}
}

func newEventQueue() *eventQueue { return &eventQueue{wake: make(chan struct{}, 1)} }

func (q *eventQueue) push(e dapEvent) {
	q.mu.Lock()
	q.items = append(q.items, e)
	q.mu.Unlock()
	q.signal()
}

func (q *eventQueue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// close は、列を閉じる。残っている event は捨てずに pop で返し、空になったら pop は false を返す。
func (q *eventQueue) close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.signal()
}

func (q *eventQueue) pop() (dapEvent, bool) {
	for {
		q.mu.Lock()
		if len(q.items) > 0 {
			e := q.items[0]
			q.items[0] = dapEvent{}
			q.items = q.items[1:]
			q.mu.Unlock()
			return e, true
		}
		closed := q.closed
		q.mu.Unlock()
		if closed {
			return dapEvent{}, false
		}
		<-q.wake
	}
}
