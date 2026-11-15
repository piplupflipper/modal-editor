// Package exthost は、拡張機能のプロセスの起動、監視、期限、再起動、差分の送信を受け持つ（基本設計 2 章、5.2〜5.4）。
//
// 本体のイベントループ（internal/editor）は、[Host] を 1 つ作り、次のように使う。
//
//   - バッファを開く、変える、閉じるたびに [Host.OpenBuffer]、[Host.ChangeBuffer]、[Host.CloseBuffer] を呼ぶ。
//     稼働中の拡張機能へ全文と差分を送る（FR-16）
//   - 拡張機能の操作が呼ばれたら、[Host.Send] で問い合わせ、結果を [QueryResult] のイベントで受け取る（5.4）。
//     Send は待たずに戻るので、イベントループは結果を待つ間も描画と利用者の中断を処理できる。
//     期限が正の設定（FR-17）では期限を渡し、期限 0 の設定（FR-18）では結果が届いた時点で照合する。
//     応答を待って止まってよい呼び出し側（テストや計測のコマンド）は、[Host.Call] と [Host.ResolveMotion] などを使う
//   - [Host.Events] のチャネルから、拡張機能からの要求と通知、状態の変化を受け取り、イベントループの goroutine で処理する
//   - バッファを保存するときは、[Host.SaveHookExtensions] の拡張機能へ 1 つずつ buffer/willSave を問い合わせ（[Host.SendWillSave]）、
//     書き出した後に [Host.DidSave] を呼ぶ（基本設計 6.7 の保存の流れ）
//
// 拡張機能のプロセスとの通信と監視は、このパッケージの中の goroutine が行う。
// それらの goroutine はバッファ（internal/buffer）を触らない。
// 拡張機能を再起動したときと buffer/resync に応じるときに全文を送れるように、[Host] は開いているバッファの写し（行の配列）を持つ。
// 写しは、[Host.OpenBuffer] で受け取った全文に、[Host.ChangeBuffer] で受け取った差分を適用して保つ。
//
// [Host] のメソッドは、複数の goroutine から並行に呼んでよい。
// ただし、問い合わせの結果とバッファの版を照らし合わせるのは呼び出し側の責務であり、
// その照合はイベントループの goroutine で行う前提である（基本設計 2.1）。
package exthost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/piplupflipper/modal-editor/ext"
)

// 設定の既定値。基本設計 10 章で未決の値なので、すべて仮である。
const (
	// DefaultInitTimeout は、起動から initialize の応答までの期限の既定値（仮）である。
	DefaultInitTimeout = 5 * time.Second
	// DefaultPingInterval は、生存確認の間隔の既定値（仮）である。
	DefaultPingInterval = 5 * time.Second
	// DefaultPingTimeout は、生存確認の応答の期限の既定値（仮）である。
	DefaultPingTimeout = 2 * time.Second
	// DefaultRestartDelay は、1 回目の失敗の後の再起動の待ち時間の既定値（仮）である。
	DefaultRestartDelay = 500 * time.Millisecond
	// DefaultMaxRestartDelay は、再起動の待ち時間の上限の既定値（仮）である。
	DefaultMaxRestartDelay = 30 * time.Second
	// DefaultMaxFailures は、無効にするまでの連続の失敗の回数の既定値（仮）である。
	DefaultMaxFailures = 5
	// DefaultResetAfter は、連続の失敗の数え直しに要る、稼働の継続時間の既定値（仮）である。
	DefaultResetAfter = time.Minute
	// DefaultSendTimeout は、期限を指定しない Send の待ちの上限の既定値（仮）である。
	DefaultSendTimeout = 10 * time.Second
	// DefaultMaxOutboxBytes は、拡張機能 1 つあたりの送信の列の上限の既定値（仮）である。
	DefaultMaxOutboxBytes = 64 << 20
	// DefaultWillSaveTimeout は、保存の流れで buffer/willSave の応答を待つ期限の既定値（仮）である（基本設計 6.7）。
	// 整形が言語サーバーの処理を待つので、キー入力ごとの問い合わせの期限より長くする。
	DefaultWillSaveTimeout = time.Second
	// DefaultMaxQueuedEvents は、拡張機能 1 つあたりの、イベントループへ渡していないイベントの数の上限の既定値（仮）である。
	DefaultMaxQueuedEvents = 1024
	// DefaultShutdownTimeout は、shutdown を送ってから、拡張機能が自分で終わるのを待つ期限の既定値（仮）である。
	// 言語サーバーの shutdown と exit、デバッグアダプターの disconnect を待てる長さにする。
	DefaultShutdownTimeout = 3 * time.Second
	// DefaultTerminateTimeout は、SIGTERM を送ってから、SIGKILL を送るまでの期限の既定値（仮）である。
	DefaultTerminateTimeout = time.Second
)

// Config は、[Host] の動作の設定である。
// ゼロ値の項目には、対応する Default で始まる定数（仮の値）を使う。
type Config struct {
	// InitTimeout は、子プロセスを起動してから initialize の応答が届くまでの期限である（5.2 の異常の条件）。
	InitTimeout time.Duration
	// PingInterval は、稼働中の拡張機能へ ping を送る間隔である。
	PingInterval time.Duration
	// PingTimeout は、ping の応答の期限である。超えたら異常と判断し、強制終了する（5.2）。
	PingTimeout time.Duration
	// RestartDelay は、1 回目の失敗の後に再起動するまでの待ち時間である。
	// 失敗が続くたびに 2 倍にし、MaxRestartDelay で頭打ちにする（図 4 の「待ち時間は失敗ごとに延長」）。
	RestartDelay time.Duration
	// MaxRestartDelay は、再起動の待ち時間の上限である。
	MaxRestartDelay time.Duration
	// MaxFailures は、連続の失敗の上限である。連続の失敗がこの回数に達したら、再起動せずに無効にする。
	MaxFailures int
	// ResetAfter は、連続の失敗を数え直すのに要る、稼働の継続時間である。
	// 稼働中になってからこの時間より長く動いた後の失敗は、連続の 1 回目として数える。
	ResetAfter time.Duration
	// SendTimeout は、[SendOptions] の Timeout を指定しない [Host.Send] が、起動と応答を待つ上限である。
	// 期限 0 の設定（FR-18）でも、応答しない問い合わせの待ち受けが溜まり続けないようにする。
	SendTimeout time.Duration
	// WillSaveTimeout は、[Host.SendWillSave] と [Host.WillSave] で、期限を指定しないときに使う期限である（基本設計 6.7 の保存の流れ）。
	WillSaveTimeout time.Duration
	// MaxOutboxBytes は、拡張機能 1 つあたりの、送信の列に積んだまま書いていないバイト数の上限である。
	// 拡張機能が標準入力を読まなくなったときに、本体のメモリを使い切らないための上限である（NFR-03）。
	// 送る時点で、列に残っている量がこの上限を超えていたら、送らずに異常と判断する（[ErrOutboxFull]）。
	// 列が空なら、上限より大きいメッセージ（大きなバッファの全文）も送る。
	// このため列の量は、最大で上限と 1 件のメッセージの和に収まる。
	MaxOutboxBytes int
	// MaxQueuedEvents は、拡張機能 1 つあたりの、イベントループへ渡していないイベントの数の上限である。
	// 拡張機能が通知を送り続けたときに、本体のメモリを使い切らないための上限で、超えたら異常と判断する（NFR-03）。
	MaxQueuedEvents int
	// ShutdownTimeout は、拡張機能を正常に止めるとき（[Host.Close]、[Host.Disable]）に、
	// shutdown を送ってから、拡張機能のプロセスが自分で終わるのを待つ期限である（基本設計 5.2）。
	// 超えたらプロセスグループへ SIGTERM を送る。
	ShutdownTimeout time.Duration
	// TerminateTimeout は、SIGTERM を送ってから、プロセスが終わるのを待つ期限である。超えたら SIGKILL を送る。
	TerminateTimeout time.Duration
	// Logger は、拡張機能の標準エラー出力と、状態の変化を記録する先である。nil なら slog.Default() を使う。
	Logger *slog.Logger
}

func (c Config) withDefaults() Config {
	if c.InitTimeout <= 0 {
		c.InitTimeout = DefaultInitTimeout
	}
	if c.PingInterval <= 0 {
		c.PingInterval = DefaultPingInterval
	}
	if c.PingTimeout <= 0 {
		c.PingTimeout = DefaultPingTimeout
	}
	if c.RestartDelay <= 0 {
		c.RestartDelay = DefaultRestartDelay
	}
	if c.MaxRestartDelay <= 0 {
		c.MaxRestartDelay = DefaultMaxRestartDelay
	}
	if c.MaxFailures <= 0 {
		c.MaxFailures = DefaultMaxFailures
	}
	if c.ResetAfter <= 0 {
		c.ResetAfter = DefaultResetAfter
	}
	if c.SendTimeout <= 0 {
		c.SendTimeout = DefaultSendTimeout
	}
	if c.WillSaveTimeout <= 0 {
		c.WillSaveTimeout = DefaultWillSaveTimeout
	}
	if c.MaxOutboxBytes <= 0 {
		c.MaxOutboxBytes = DefaultMaxOutboxBytes
	}
	if c.MaxQueuedEvents <= 0 {
		c.MaxQueuedEvents = DefaultMaxQueuedEvents
	}
	if c.ShutdownTimeout <= 0 {
		c.ShutdownTimeout = DefaultShutdownTimeout
	}
	if c.TerminateTimeout <= 0 {
		c.TerminateTimeout = DefaultTerminateTimeout
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	return c
}

// Spec は、[Host] が管理する 1 つの拡張機能の定義である。
//
// 宣言ファイルの読み込み（JSONC）と探す場所は未決（#9）なので、このパッケージは読み込んで検査した後の値を受け取る。
type Spec struct {
	// Manifest は、検査済み（[ext.ValidateManifests] が誤りを返さなかった）の宣言ファイルの内容である。
	Manifest ext.Manifest
	// Dir は、宣言ファイルのあるディレクトリである。Manifest.Command が相対パスなら、ここから解決する。
	Dir string
	// Settings は、利用者の設定のうち、この拡張機能に向けた部分である。initialize でそのまま渡す。無ければ nil。
	Settings json.RawMessage
}

// command は、起動する実行ファイルのパスを返す。
func (s *Spec) command() string {
	if filepath.IsAbs(s.Manifest.Command) || s.Dir == "" {
		return s.Manifest.Command
	}
	return filepath.Join(s.Dir, s.Manifest.Command)
}

// State は、拡張機能の状態である（基本設計 5.2、図 4）。
type State int

// 拡張機能の状態。
const (
	// StateNotStarted は、宣言ファイルだけを読み、子プロセスを起動していない状態である。
	StateNotStarted State = iota
	// StateStarting は、子プロセスを起動し、initialize の応答を待っている状態である。
	StateStarting
	// StateRunning は、initialize の応答が届き、問い合わせに応答できる状態である。
	StateRunning
	// StateRestartWait は、異常で子プロセスを終わらせ、再起動の待ち時間が過ぎるのを待っている状態である。
	StateRestartWait
	// StateDisabled は、連続の失敗が上限に達したか、[Host.Disable] で止めたので、自動では起動しない状態である。
	// [Host.Restart] で起動し直せる。
	// 上限に達した失敗でも、いったん StateRestartWait へ移ってから、待たずにこの状態へ移る（図 4）。
	StateDisabled
)

// String は、状態の名前を返す。
func (s State) String() string {
	switch s {
	case StateNotStarted:
		return "not-started"
	case StateStarting:
		return "starting"
	case StateRunning:
		return "running"
	case StateRestartWait:
		return "restart-wait"
	case StateDisabled:
		return "disabled"
	}
	return fmt.Sprintf("State(%d)", int(s))
}

// 誤りの種類。返す誤りはこれらを包むので、errors.Is で判定する。
var (
	// ErrUnknownExtension は、指定した ID の拡張機能が無いことを表す。
	ErrUnknownExtension = errors.New("exthost: unknown extension")
	// ErrUnavailable は、拡張機能が再起動待ちか無効であるか、問い合わせの途中で異常終了したことを表す。
	ErrUnavailable = errors.New("exthost: extension unavailable")
	// ErrTimeout は、期限付きの問い合わせが期限を超えたことを表す（FR-17）。
	ErrTimeout = errors.New("exthost: query timed out")
	// ErrOutboxFull は、拡張機能が標準入力を読まず、送信の列が [Config.MaxOutboxBytes] を超えたことを表す。
	// 異常の理由として [StateChange] の Err に入る。
	ErrOutboxFull = errors.New("exthost: extension is not reading its input (outbox limit exceeded)")
	// ErrClosed は、[Host.Close] の後に呼ばれたことを表す。
	ErrClosed = errors.New("exthost: host closed")
)

// Host は、拡張機能のプロセスを管理する。New で作る。
type Host struct {
	cfg Config
	log *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc
	// wg は、supervise と、Send の待ち受けの goroutine を数える。
	wg     sync.WaitGroup
	events chan Event
	// pumpDone は、イベントを渡す goroutine が終わると閉じる。
	pumpDone chan struct{}
	// wake は、queue に積んだことを、イベントを渡す goroutine へ知らせる。
	wake chan struct{}

	// mu は、ここから下のフィールドと、各 extension と run の可変のフィールドを守る。
	mu      sync.Mutex
	exts    map[string]*extension
	order   []*extension
	docs    map[ext.BufferID]*document
	queue   []queued
	closing bool
}

// extension は、1 つの拡張機能の状態である。可変のフィールドは Host.mu が守る。
type extension struct {
	spec  Spec
	state State
	// run は、起動中か稼働中のプロセスである。それ以外の状態では nil。
	run *run
	// failures は、連続の失敗の回数である。
	failures int
	// changed は、状態が変わると閉じ、新しいチャネルに取り替える。状態の変化を待つのに使う。
	changed chan struct{}
	// stop は、[Host.Disable] で閉じる。起動するたびに新しいチャネルにする。監視の goroutine はこれで止める。
	stop chan struct{}
	// stopping は、stop を閉じたかどうかである。
	stopping bool
}

// New は、specs の拡張機能を管理する Host を作る。子プロセスは起動しない（FR-14、NFR-04）。
// 拡張機能の ID が重複していれば誤りを返す。
// 使い終わったら Close を呼ぶ。
func New(specs []Spec, cfg Config) (*Host, error) {
	cfg = cfg.withDefaults()
	ctx, cancel := context.WithCancel(context.Background())
	h := &Host{
		cfg:      cfg,
		log:      cfg.Logger,
		ctx:      ctx,
		cancel:   cancel,
		events:   make(chan Event),
		pumpDone: make(chan struct{}),
		wake:     make(chan struct{}, 1),
		exts:     make(map[string]*extension),
		docs:     make(map[ext.BufferID]*document),
	}
	for _, s := range specs {
		id := s.Manifest.ID
		if _, dup := h.exts[id]; dup {
			cancel()
			return nil, fmt.Errorf("exthost: duplicate extension id %q", id)
		}
		e := &extension{spec: s, changed: make(chan struct{})}
		h.exts[id] = e
		h.order = append(h.order, e)
	}
	go h.pump()
	return h, nil
}

// Events は、イベントループへ渡すイベントのチャネルを返す。
// イベントの種類と、拡張機能からのリクエストの扱いは [Event] の doc にある。
//
// チャネルはバッファを持たず、Host の中の列から 1 件ずつ渡す。
// 受け取らない間も、拡張機能との通信は止まらない（列の上限は [Config.MaxQueuedEvents]）。
// 拡張機能を異常と判断して終わらせたとき、その拡張機能からのイベントのうち、まだ渡していないものは捨てる。
// [Host.Close] の後に閉じる。
func (h *Host) Events() <-chan Event {
	return h.events
}

// State は、拡張機能の現在の状態を返す。ID が無ければ ok は false である。
func (h *Host) State(id string) (st State, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	e, ok := h.exts[id]
	if !ok {
		return 0, false
	}
	return e.state, true
}

// Activate は、起動の契機 kind が value について成り立ったことを知らせ、その契機を宣言した未起動の拡張機能を起動する。
// value は、操作の契機なら操作の ID、ファイルの種類の契機ならファイルの種類の名前である。
// 未知の種類の契機は、宣言にあっても無視する（[ext.Manifest.KnownActivation]）。
//
// 起動は非同期で、子プロセスの起動と初期化を待たずに戻る。
// 起動を始めた拡張機能の ID を返す。
//
// req: FR-14, NFR-04
func (h *Host) Activate(kind ext.ActivationKind, value string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.activateLocked(kind, value)
}

func (h *Host) activateLocked(kind ext.ActivationKind, value string) []string {
	if h.closing || value == "" {
		return nil
	}
	var started []string
	for _, e := range h.order {
		if e.state != StateNotStarted {
			continue
		}
		for _, a := range e.spec.Manifest.KnownActivation() {
			if a.On != kind {
				continue
			}
			v := a.ID
			if kind == ext.ActivationFileType {
				v = a.Pattern
			}
			if v == value {
				h.startLocked(e)
				started = append(started, e.spec.Manifest.ID)
				break
			}
		}
	}
	return started
}

// Start は、未起動の拡張機能を、契機に依らずに起動する。起動は非同期である。
// 未起動でなければ何もしない。
func (h *Host) Start(id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	e, err := h.lookupLocked(id)
	if err != nil {
		return err
	}
	if e.state == StateNotStarted {
		h.startLocked(e)
	}
	return nil
}

// Restart は、無効になった拡張機能の連続の失敗の数を 0 に戻し、起動し直す（図 4 の「利用者が再起動を指示」）。
// 未起動の拡張機能なら起動する。それ以外の状態では何もしない。起動は非同期である。
func (h *Host) Restart(id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	e, err := h.lookupLocked(id)
	if err != nil {
		return err
	}
	switch e.state {
	case StateDisabled, StateNotStarted:
		e.failures = 0
		h.startLocked(e)
	case StateStarting, StateRunning, StateRestartWait:
	}
	return nil
}

// Disable は、拡張機能を正常に止め、無効にする（利用者の指示による無効化）。止めるのは非同期で、待たずに戻る。
//
// 起動中か稼働中なら、Close と同じ手順（shutdown、期限後に SIGTERM、さらに期限後に SIGKILL）でプロセスを終わらせ、
// 終わった後に [StateDisabled] へ移る。終わるまでは、状態は起動中か稼働中のままである。
// 再起動待ちなら、待つのをやめて無効にする。未起動なら、そのまま無効にする。無効なら何もしない。
// 止めたことは連続の失敗に数えない。[Host.Restart] で起動し直せる。
//
// 止めている間に拡張機能から届いたイベントは捨てずに渡す（異常による終了と違い、拡張機能は正常に動いているため）。
func (h *Host) Disable(id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	e, err := h.lookupLocked(id)
	if err != nil {
		return err
	}
	switch e.state {
	case StateNotStarted:
		h.setStateLocked(e, StateDisabled, nil)
	case StateStarting, StateRunning, StateRestartWait:
		if !e.stopping {
			e.stopping = true
			close(e.stop)
		}
	case StateDisabled:
	}
	return nil
}

// Close は、すべての拡張機能のプロセスを正常に止め、管理の goroutine が終わるのを待つ（基本設計 5.2）。
// その後に [Host.Events] のチャネルを閉じる。
//
// 起動中か稼働中の拡張機能へは shutdown を送り、[Config.ShutdownTimeout] までにプロセスが終わらなければ
// プロセスグループへ SIGTERM を、さらに [Config.TerminateTimeout] を過ぎたら SIGKILL を送る。
// 拡張機能ごとに並行して行うので、Close が待つのは最も長くかかった拡張機能の分である。
// プロセスが終わった後も、同じプロセスグループに残ったプロセスには SIGKILL を送る。
// shutdown を送った後に拡張機能から届いたイベントは、[Host.Events] へ渡さない。
// Close の後の問い合わせは [ErrClosed] を包んだ誤りを返す。2 回目以降の呼び出しは何もしない。
func (h *Host) Close() error {
	h.mu.Lock()
	if h.closing {
		h.mu.Unlock()
		<-h.pumpDone
		return nil
	}
	h.closing = true
	h.mu.Unlock()
	h.cancel()
	h.wg.Wait()
	<-h.pumpDone
	return nil
}

func (h *Host) lookupLocked(id string) (*extension, error) {
	if h.closing {
		return nil, ErrClosed
	}
	e, ok := h.exts[id]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownExtension, id)
	}
	return e, nil
}

// setStateLocked は、拡張機能の状態を変え、状態の変化を待つ者を起こし、StateChange のイベントを積む。
func (h *Host) setStateLocked(e *extension, st State, cause error) {
	e.state = st
	close(e.changed)
	e.changed = make(chan struct{})
	id := e.spec.Manifest.ID
	if cause != nil {
		h.log.Warn("extension state changed", "extension", id, "state", st.String(), "error", cause)
	} else {
		h.log.Info("extension state changed", "extension", id, "state", st.String())
	}
	// 拡張機能から届いたイベントではない（r が nil）ので、上限で拒まれることは無い。
	_ = h.pushLocked(&StateChange{Extension: id, State: st, Err: cause}, nil)
}

// startLocked は、拡張機能の状態を起動中にし、プロセスを管理する goroutine を起動する。
func (h *Host) startLocked(e *extension) {
	r := newRun()
	e.run = r
	e.stop = make(chan struct{})
	e.stopping = false
	h.setStateLocked(e, StateStarting, nil)
	h.wg.Add(1)
	go h.supervise(e, r, e.stop)
}
