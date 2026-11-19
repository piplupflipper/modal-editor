package term

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	xterm "golang.org/x/term"

	"github.com/piplupflipper/modal-editor/internal/editcmd"
	"github.com/piplupflipper/modal-editor/internal/ui"
)

// DefaultEscTimeout は、ESC で始まる列の続きを待つ時間の既定値である。
// この時間内に続きが届かなければ、ESC は Esc キーの単独の押下とする。
// Vim の defaults.vim の 'ttimeoutlen'（100 ms）より短い、Neovim の既定と同じ 50 ms にした（仮）。
// 端末が列を分けて送る遅い回線（ssh）では足りないことがあるので、設定で変えられるようにする前提である。
const DefaultEscTimeout = 50 * time.Millisecond

// 端末を開くときと戻すときに出す制御シーケンス。
const (
	// seqEnter は、代替画面に切り替え（DECSET 1049）、bracketed paste を有効にし（DECSET 2004）、
	// 行末での折り返しを止める（DECRST 7。右下のセルに書いても画面が上へずれないようにする）。
	seqEnter = "\x1b[?1049h\x1b[?2004h\x1b[?7l"
	// seqLeave は、seqEnter と、描画が変えた状態（文字の属性、カーソルの形と表示）を戻す。
	// カーソルの形は、端末の既定の形（DECSCUSR 0）に戻す。
	seqLeave = "\x1b[0m\x1b[0 q\x1b[?25h\x1b[?7h\x1b[?2004l\x1b[?1049l"
)

// Event は、端末からのイベント 1 件である。internal/protocol の Event と同じ形で、型の変換で渡せる。
type Event struct {
	// Key は、押されたキーである。Resize が true か Paste が空でなければ読まない。
	Key editcmd.Key
	// Paste は、bracketed paste で貼り付けたテキスト（Decoded.Paste）である。空でなければ、このイベントは貼り付けである。
	Paste string
	// Resize は、端末の大きさが Width と Height に変わったことを表す。
	Resize        bool
	Width, Height int
}

// Options は、Terminal の設定である。ゼロ値の項目は既定値を使う。
type Options struct {
	// EscTimeout は、ESC で始まる列の続きを待つ時間である。0 以下なら DefaultEscTimeout を使う。
	EscTimeout time.Duration
}

// ErrNotTerminal は、Open に渡したファイルが端末でないことを表す。
var ErrNotTerminal = errors.New("term: not a terminal")

// Terminal は、raw モードにした実物の端末である。Open で開き、Restore で端末を元に戻し、Close で入力の読み取りを止める。
//
// 入力は、Open が起こす goroutine が in から読んで解読し、Input のチャネルへ送る。
// 端末の大きさの変化（SIGWINCH）も、同じチャネルへ Resize のイベントとして送る。
// 一時停止（SIGTSTP）を受けたら、端末を戻してから SIGSTOP で止まり、再開（SIGCONT）で raw モードと代替画面に入り直して、
// Resize のイベント（大きさは今の端末のもの）を送ってエディタに全体を描き直させる。
// 一時停止を受けるのは、kill -TSTP など外から送られたときである。raw モードでは <C-z> はキーとして届き、シグナルにならない。
// Draw、SetClipboard、Size は、1 つの goroutine（エディタのイベントループ）から呼ぶ。
// Restore は、どの goroutine からも何度でも呼べ、2 回目以降は何もしない。
//
// # 端末の受け渡し
//
// Release で端末を子プロセスへ渡し（:!。FR-80）、Reacquire で取り戻す（handover.go）。
// 渡している間は in を読まない。読み取りの goroutine は、in を read で待たずに poll(2) で待つので、
// 渡す前に止められる（止めないと、子プロセスへ打ったキーをこちらが先に読んでしまう）。
type Terminal struct {
	in, out     *os.File
	inFd, outFd int
	state       *xterm.State
	opts        Options

	// mu は、out への書き込みを守る。Draw と Restore が別の goroutine から呼ばれても、制御シーケンスが混ざらないようにする。
	mu       sync.Mutex
	restored bool
	// suspended は、一時停止（SIGTSTP）のために端末を戻してあり、再開（SIGCONT）を待っていることを表す。
	suspended bool
	// away は、Release で端末を子プロセスへ渡してあり、raw モードでも代替画面でもないことを表す（handover.go）。
	away bool
	// noScreen は、Reacquire(false) で raw モードに戻したが、代替画面にはまだ入っていないことを表す（「続けるには ENTER」の問い）。
	noScreen bool
	// prevFg は、Reacquire が端末の前面のプロセスグループを自分へ移す前の前面のプロセスグループである。0 なら移していない。
	// Release で、そのプロセスグループへ返す（基本設計 4.9 の端末の取り合い）。
	prevFg  int
	painter Painter
	buf     bytes.Buffer

	events chan Event
	stop   chan struct{}
	// readerDone は、in を読む goroutine が終わると閉じる。
	readerDone chan struct{}
	closeOnce  sync.Once
	// winch は、SIGWINCH、SIGTSTP、SIGCONT を受ける。
	winch chan os.Signal

	// gate は、読み取りの goroutine を止めて再開させる仕組みである（handover.go の readGate）。
	gate readGate
}

// Open は、in を raw モードにし、out を代替画面に切り替えて、Terminal を返す。
// in と out のどちらかが端末でなければ ErrNotTerminal を返し、端末を変えない。
// 返った Terminal は、使い終えたら Restore と Close を呼ぶ。panic で終わるときも Restore を呼ぶ（基本設計 8 章）。
func Open(in, out *os.File, opts Options) (*Terminal, error) {
	inFd, err := fdOf(in)
	if err != nil {
		return nil, err
	}
	outFd, err := fdOf(out)
	if err != nil {
		return nil, err
	}
	if !xterm.IsTerminal(inFd) || !xterm.IsTerminal(outFd) {
		return nil, ErrNotTerminal
	}
	if opts.EscTimeout <= 0 {
		opts.EscTimeout = DefaultEscTimeout
	}
	state, err := xterm.MakeRaw(inFd)
	if err != nil {
		return nil, fmt.Errorf("term: make raw: %w", err)
	}
	t := &Terminal{
		in:         in,
		out:        out,
		inFd:       inFd,
		outFd:      outFd,
		state:      state,
		opts:       opts,
		events:     make(chan Event, 64),
		stop:       make(chan struct{}),
		readerDone: make(chan struct{}),
		winch:      make(chan os.Signal, 4),
	}
	if err := t.gate.init(); err != nil {
		_ = xterm.Restore(inFd, state) // 元の誤りを返す
		return nil, err
	}
	if _, err := out.WriteString(seqEnter); err != nil {
		_ = xterm.Restore(inFd, state) // 元の誤りを返す
		t.gate.close()
		return nil, fmt.Errorf("term: write: %w", err)
	}
	signal.Notify(t.winch, syscall.SIGWINCH, syscall.SIGTSTP, syscall.SIGCONT)
	chunks := make(chan []byte, 16)
	go t.read(chunks)
	go t.loop(chunks)
	return t, nil
}

// Input は、キー、貼り付け、リサイズのイベントを届いた順に送るチャネルを返す。
// 入力が終わる（in の読み取りの誤り、Close）と閉じる。
func (t *Terminal) Input() <-chan Event { return t.events }

// Size は、端末の現在の列数と行数を返す。取れなければ 80 列 24 行を返す。
func (t *Terminal) Size() (width, height int) {
	w, h, err := xterm.GetSize(t.outFd)
	if err != nil || w <= 0 || h <= 0 {
		return 80, 24
	}
	return w, h
}

// Draw は、画面 s を端末へ出す。前回の Draw から変わったセルだけを書く（Painter）。
// Restore の後と、端末を子プロセスへ渡している間（Release）は、何も書かずに nil を返す。
func (t *Terminal) Draw(s ui.Screen) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.screenOn() {
		return nil
	}
	t.buf.Reset()
	t.painter.Paint(&t.buf, s)
	if t.buf.Len() == 0 {
		return nil
	}
	if _, err := t.out.Write(t.buf.Bytes()); err != nil {
		// 書けた所が分からないので、次は全体を書き直す。
		t.painter.Invalidate()
		return fmt.Errorf("term: write: %w", err)
	}
	return nil
}

// SetClipboard は、text を OSC 52 の制御シーケンス（ESC ] 52 ; c ; <base64> BEL）で端末へ送り、
// ホストのクリップボードに入れる（FR-56）。端末の応答は待たず、端末が受け取ったかは分からない。
// 画面の出力と混ざらないよう、Draw と同じロックの中で書く。Restore の後、一時停止の間、端末を渡している間は何も書かずに nil を返す。
//
// req: FR-56
func (t *Terminal) SetClipboard(text string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.screenOn() {
		return nil
	}
	if _, err := t.out.Write(osc52(text)); err != nil {
		// 書いた途中で止まったら、画面の差分の前提が崩れているかもしれないので、次は全体を書き直す。
		t.painter.Invalidate()
		return fmt.Errorf("term: write: %w", err)
	}
	return nil
}

// osc52 は、text をクリップボード（c）に入れる OSC 52 の制御シーケンスを返す。終端は BEL で、tmux と多くの端末が受け付ける。
func osc52(text string) []byte {
	const prefix, suffix = "\x1b]52;c;", "\a"
	out := make([]byte, 0, len(prefix)+base64.StdEncoding.EncodedLen(len(text))+len(suffix))
	out = append(out, prefix...)
	out = base64.StdEncoding.AppendEncode(out, []byte(text))
	return append(out, suffix...)
}

// Restore は、端末を Open の前の状態に戻す（代替画面から戻り、bracketed paste を止め、raw モードを解く）。
// 2 回目以降の呼び出しは何もせず nil を返す。戻した後の Draw は何も書かない。
func (t *Terminal) Restore() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.restored {
		return nil
	}
	t.restored = true
	if t.suspended || t.away {
		return nil // 一時停止のときか、子プロセスへ渡したときに戻してある
	}
	_, werr := t.out.WriteString(seqLeave)
	rerr := xterm.Restore(t.inFd, t.state)
	if rerr != nil {
		return fmt.Errorf("term: restore: %w", rerr)
	}
	if werr != nil {
		return fmt.Errorf("term: write: %w", werr)
	}
	return nil
}

// suspend は、一時停止（SIGTSTP）の前に、端末を Open の前の状態に戻す。Restore と違い、resume で入り直せる。
// 端末を子プロセスへ渡している間（Release の後）は、端末に触らない（子プロセスと一緒に止まるだけにする）。
func (t *Terminal) suspend() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.restored || t.suspended || t.away {
		return
	}
	t.suspended = true
	_, _ = t.out.WriteString(seqLeave) // 書けなくても止まる
	_ = xterm.Restore(t.inFd, t.state) // 戻せなくても止まる
}

// resume は、一時停止からの再開（SIGCONT）で、raw モードと代替画面に入り直し、次の Draw で全体を書き直させる。
// suspend を通らない停止（外からの SIGSTOP）の後も、シェルが端末の設定を変えているかもしれないので入り直す。
// 端末を子プロセスへ渡している間は、何もしない。代替画面に入る前の問いの間（noScreen）は、raw モードにだけ戻す。
func (t *Terminal) resume() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.restored || t.away {
		return
	}
	t.suspended = false
	_, _ = xterm.MakeRaw(t.inFd) // 入り直せなくても、描き直しは試みる
	if t.noScreen {
		return
	}
	_, _ = t.out.WriteString(seqEnter) // 同上
	t.painter.Invalidate()
}

// RestoreOnPanic は、goroutine の始めに defer で呼ぶ（defer t.RestoreOnPanic()）。
// その goroutine が panic したら、端末を戻してから同じ値で panic を続ける（基本設計 8 章）。
// Go の panic は他の goroutine の recover で捕まえられないので、端末を使う間に動く goroutine はそれぞれこれを置く。
// panic していなければ何もしない。
func (t *Terminal) RestoreOnPanic() {
	if p := recover(); p != nil {
		_ = t.Restore() // panic を続けるので、戻す誤りは捨てる
		panic(p)
	}
}

// Close は、入力の読み取りとリサイズの監視を止め、Input のチャネルを閉じる。端末は戻さない（Restore を先に呼ぶ）。
// 読み取りの goroutine は in を poll(2) で待っているので、起こして終わらせる（readGate）。
func (t *Terminal) Close() error {
	t.closeOnce.Do(func() {
		signal.Stop(t.winch)
		close(t.stop)
		t.gate.stop()
		// 読み取りの途中の Read を戻す。期限を付けられないファイルでは誤りになるが、そのときは待たない。
		_ = t.in.SetReadDeadline(time.Now()) // 戻せなくても Close は続ける
	})
	return nil
}

// fdOf は、f のファイル記述子を返す。
// os.File.Fd は、ファイルを blocking のモードにして SetReadDeadline を効かなくするので、使わない（Close が読み取りを戻せなくなる）。
func fdOf(f *os.File) (int, error) {
	rc, err := f.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("term: %w", err)
	}
	var fd int
	if err := rc.Control(func(p uintptr) { fd = int(p) }); err != nil {
		return 0, fmt.Errorf("term: %w", err)
	}
	return fd, nil
}

// read は、in を読み、読んだバイト列を chunks へ送る。読み取りが誤りで終わると chunks を閉じる。
// 読む前に、in が読めるようになるのを poll(2) で待つ（readGate.wait）。端末を子プロセスへ渡している間は、そこで止まる。
func (t *Terminal) read(chunks chan<- []byte) {
	defer close(t.readerDone)
	defer close(chunks)
	defer t.gate.exited()
	defer t.RestoreOnPanic()
	buf := make([]byte, 4096)
	for {
		if !t.gate.wait(t.inFd) {
			return
		}
		n, err := t.in.Read(buf)
		if isAgain(err) {
			continue // poll の後に別の読み手が読んだ（起きない想定）か、偽の目覚め
		}
		if n > 0 {
			select {
			case chunks <- append([]byte(nil), buf[:n]...):
			case <-t.stop:
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// loop は、読んだバイト列を解読してキーのイベントにし、SIGWINCH をリサイズのイベントにして、events へ送る。
// ESC で始まる列の続きを EscTimeout だけ待ち、届かなければ保留を確定させる。
func (t *Terminal) loop(chunks <-chan []byte) {
	defer close(t.events)
	defer t.RestoreOnPanic()
	var dec Decoder
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	var timeout <-chan time.Time
	for {
		var ds []Decoded
		select {
		case <-t.stop:
			return
		case b, ok := <-chunks:
			if !ok {
				ds = dec.Flush()
				t.send(ds)
				return
			}
			ds = dec.Feed(b)
		case <-timeout:
			ds = dec.Flush()
		case sig := <-t.winch:
			switch sig {
			case syscall.SIGTSTP:
				// 端末を戻してから自分を止める。SIGSTOP は捕まえられないので、そのまま止まる。
				// 再開（SIGCONT）は、このチャネルへ届いてから resume する。
				t.suspend()
				_ = syscall.Kill(os.Getpid(), syscall.SIGSTOP) // 止まれなければ、そのまま続ける
				continue
			case syscall.SIGCONT:
				t.resume()
			}
			// 大きさの変化と再開の後は、リサイズのイベントでエディタに描き直させる。
			w, h := t.Size()
			if !t.emit(Event{Resize: true, Width: w, Height: h}) {
				return
			}
			continue // 保留の時間切れの待ちは、そのまま続ける
		}
		if !t.send(ds) {
			return
		}
		if dec.Pending() {
			timer.Reset(t.opts.EscTimeout)
			timeout = timer.C
		} else {
			timer.Stop()
			timeout = nil
		}
	}
}

// send は、解読したキーと貼り付けを events へ送る。Close で止められたら false を返す。
func (t *Terminal) send(ds []Decoded) bool {
	for _, d := range ds {
		if !t.emit(Event{Key: d.Key, Paste: d.Paste}) {
			return false
		}
	}
	return true
}

func (t *Terminal) emit(ev Event) bool {
	select {
	case t.events <- ev:
		return true
	case <-t.stop:
		return false
	}
}
