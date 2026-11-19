package client

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"

	"github.com/piplupflipper/modal-editor/internal/editcmd"
	"github.com/piplupflipper/modal-editor/internal/protocol"
	"github.com/piplupflipper/modal-editor/internal/term"
)

// このファイルは、server に頼まれて端末を子プロセスへ渡す処理である（基本設計 4.7、4.9。FR-79、FR-80、FR-117）。
//
// 端末を持つのは client なので、:! のシェルと、権限を上げた保存の sudo tee は client が起動する（server は端末を持たない）。
//
//  1. protocol.TerminalRun を受けたら、端末を子プロセスへ渡し（Handover.Release）、別の goroutine でコマンドを順に動かす。
//     受信の goroutine は止めない（実行中に TerminalReclaim が届くため）
//  2. 実行中に protocol.TerminalReclaim を受けたら、端末を取り戻して画面を描く（FR-117。子プロセスの子孫が起動した待つだけの client の
//     ファイルを、このタブで編集させる）。protocol.TerminalRelease を受けたら、端末を子プロセスへ渡し直し、protocol.TerminalReleased を返す
//  3. コマンドが終わったら、Prompt なら「続けるには ENTER を押してください」を出してキーを 1 つ待ち、端末を取り戻して protocol.TerminalDone を返す。
//     取り戻してあれば（2 の途中で終わった）、問わずに返す
//
// 子プロセスは、Vim と同じく本体と同じプロセスグループで動かす。端末の <C-c> と <C-\> の SIGINT と SIGQUIT は本体にも届くので、
// 子プロセスが動いている間だけ、本体はそれらを signal.Ignore で無視にする（runCommands。term.Terminal.TerminalSignals の doc の順序）。
// 無視の間に起きたシグナルはカーネルが捨てるので、取り戻した後に遅れて届くことは無い（PR #234 の CI の失敗）。
// <C-z>（SIGTSTP）は既定の扱いのままで、本体と子プロセスがまとめて止まり、シェルの fg でまとめて続く（Vim と同じ）。

// Handover は、端末を子プロセスへ渡せる端末の口である。*term.Terminal が満たす。
// Terminal がこれを満たさなければ、client は TerminalRun を断る（TerminalDone の Err）。
type Handover interface {
	// Release は、端末を子プロセスへ渡す（入力の読み取りを止め、代替画面と raw モードを抜ける）。
	Release() error
	// Reacquire は、端末を取り戻す。screen が false なら raw モードだけに戻し（キーを 1 つ読むため）、true なら代替画面にも入る。
	Reacquire(screen bool) error
	// EnsureForeground は、端末の前面のプロセスグループを自分のものへ戻す（対話のシェルが前面を移したまま終わったとき）。
	EnsureForeground()
	// TTY は、子プロセスの標準入出力にする端末のファイルを返す。
	TTY() (in, out *os.File)
	// TerminalSignals は、渡している間の端末の ISIG を切り替える。IgnoreInterrupts と RestoreInterrupts は、本体の SIGINT と SIGQUIT を
	// 無視にし、元に戻す。順序は term.Terminal.TerminalSignals の doc にある。
	TerminalSignals(on bool) error
	IgnoreInterrupts()
	RestoreInterrupts()
}

// Features は、端末 tm の client が Hello で宣言する機能（protocol.Hello.Features）を返す。
// tm が Handover を満たせば、端末の受け渡し（protocol.FeatureTerminal）を宣言する。
func Features(tm Terminal) []string {
	if _, ok := tm.(Handover); ok {
		return []string{protocol.FeatureTerminal}
	}
	return nil
}

// terminatedText は、コマンドがシグナルで終わったときの文である（Vim の "Command terminated"）。
const terminatedText = "コマンドが中断されました"

// promptText は、:! の後に出す問いである（Vim の "Press ENTER or type command to continue"）。
const promptText = "続けるには ENTER を押してください"

// handover は、1 つの client の、端末を子プロセスへ渡す処理の状態である。
type handover struct {
	tm     Terminal
	conn   protocol.ClientConn
	logger *slog.Logger

	mu sync.Mutex
	// job は、実行中の TerminalRun である。無ければ nil。
	job *runningJob
	// prompt は、「続けるには ENTER」の問いの間に、forward がキーを受けたら閉じるチャネルである。問いが無ければ nil。
	prompt chan struct{}
	// closed は、server との接続が切れた（run の受信の loop が終わった）ら閉じる。問いの待ちを戻す。
	closed    chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup
}

// runningJob は、実行中の TerminalRun である。
type runningJob struct {
	id uint64
	// reclaimed は、TerminalReclaim で端末を取り戻していることを表す。
	reclaimed bool
	// exited は、コマンドが終わり、TerminalDone を返す途中であることを表す。この後の Reclaim と Release では端末に触らない。
	exited bool
}

func newHandover(tm Terminal, conn protocol.ClientConn, logger *slog.Logger) *handover {
	return &handover{tm: tm, conn: conn, logger: logger, closed: make(chan struct{})}
}

// start は、TerminalRun を受けて、端末を渡してコマンドを動かし始める。
// 端末を渡せない（Handover を満たさない、既に実行中）ときは、すぐに TerminalDone で断る。
//
// req: FR-80
func (h *handover) start(req protocol.TerminalRun) {
	ho, ok := h.tm.(Handover)
	h.mu.Lock()
	busy := h.job != nil
	if ok && !busy {
		h.job = &runningJob{id: req.ID}
	}
	h.mu.Unlock()
	switch {
	case !ok:
		h.send(protocol.TerminalDone{ID: req.ID, Err: "この client の端末は子プロセスへ渡せない"})
		return
	case busy:
		h.send(protocol.TerminalDone{ID: req.ID, Err: "端末を別のコマンドへ渡している途中"})
		return
	}
	if err := ho.Release(); err != nil {
		h.logger.Error("端末を子プロセスへ渡せない", "err", err)
	}
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		defer func() {
			if p := recover(); p != nil {
				_ = h.tm.Restore() // panic を続けるので、戻す誤りは捨てる
				panic(p)
			}
		}()
		h.run(ho, req)
	}()
}

// run は、req のコマンドを動かし、終わったら端末を取り戻して TerminalDone を返す。start が起こす goroutine で動く。
func (h *handover) run(ho Handover, req protocol.TerminalRun) {
	_, out := ho.TTY()
	// 代替画面から戻った通常の画面の、前の出力（前回の :! の問いなど）の行の続きから書き始めないよう、改行してから動かす。
	_, _ = out.Write([]byte("\r\n")) // 書けなくてもコマンドは動かす
	status, errText := runCommands(req, ho)
	h.mu.Lock()
	reclaimed := h.job.reclaimed
	h.job.exited = true
	h.mu.Unlock()
	if !reclaimed {
		ho.EnsureForeground()
		if req.Prompt {
			h.ask(ho, out, status, errText)
		}
		if err := ho.Reacquire(true); err != nil {
			h.logger.Error("端末を取り戻せない", "err", err)
		}
	}
	h.mu.Lock()
	h.job = nil
	h.mu.Unlock()
	h.send(protocol.TerminalDone{ID: req.ID, Status: status, Err: errText})
}

// ask は、子プロセスの出力の後に、終了コードと「続けるには ENTER を押してください」を出し、キーを 1 つ待つ（Vim の :! と同じ）。
// 端末は代替画面の外の raw モードにする。キーは forward が受け取る（answer）。
func (h *handover) ask(ho Handover, out io.Writer, status int, errText string) {
	var b bytes.Buffer
	switch {
	case errText != "":
		fmt.Fprintf(&b, "\r\n%s", errText)
	case status != 0:
		fmt.Fprintf(&b, "\r\nシェルが %d を返しました", status)
	}
	fmt.Fprintf(&b, "\r\n%s", promptText)
	ch := make(chan struct{})
	h.mu.Lock()
	h.prompt = ch
	h.mu.Unlock()
	// 問いを出す前に raw モードにする。問いが見えてから打ったキーが、cooked のモードの端末で行の編集と ICRNL（<CR> を <C-j> にする）を
	// 受けないためである（PR #234 の CI で、問いを見てすぐ打った "\r:q\r" が "\n:q\n" として届き、問いにも :q にもならなかった）。
	if err := ho.Reacquire(false); err != nil {
		h.logger.Error("端末を raw モードにできない", "err", err)
	}
	_, _ = out.Write(b.Bytes()) // 出せなくてもキーは待つ。raw モードなので改行は "\r\n" で書く
	select {
	case <-ch:
	case <-h.closed:
		h.mu.Lock()
		h.prompt = nil
		h.mu.Unlock()
	}
}

// answer は、「続けるには ENTER」の問いの間に端末から届いたイベント ev を、問いの答えにする。答えにしたら true を返す。
// <CR>、<Space>、<Esc> は捨て、それ以外のキーと貼り付けは server へ送る（Vim の hit-enter と同じく、コマンドとして使う）。
// server は、TerminalDone を受けるまでこの client のキーを処理しないので、送ったキーは端末を戻した後に処理される。
// 大きさの変化は答えにしない。
func (h *handover) answer(ev term.Event) bool {
	if ev.Resize {
		return false
	}
	h.mu.Lock()
	ch := h.prompt
	h.prompt = nil
	h.mu.Unlock()
	if ch == nil {
		return false
	}
	k := ev.Key.Normalize()
	if ev.Paste != "" || (k != editcmd.Special(editcmd.CodeEnter) && k != editcmd.Rune(' ') && k != editcmd.Esc) {
		_ = h.conn.Send(protocol.Event(ev)) // 閉じた後の誤りは捨てる
	}
	close(ch)
	return true
}

// reclaim は、実行中の id のコマンドの間に、端末を取り戻して画面を描けるようにする（FR-117）。
// server はこの後に画面を送る。取り戻した後の最初の Draw は、端末の全体を書き直す。
// 実行中でないか、もう終わる途中なら何もしない。
//
// req: FR-117
func (h *handover) reclaim(id uint64) {
	h.mu.Lock()
	j := h.job
	ok := j != nil && j.id == id && !j.exited && !j.reclaimed
	if ok {
		j.reclaimed = true
	}
	h.mu.Unlock()
	if !ok {
		return
	}
	if ho, hok := h.tm.(Handover); hok {
		if err := ho.Reacquire(true); err != nil {
			h.logger.Error("端末を取り戻せない", "err", err)
		}
	}
}

// release は、reclaim で取り戻した端末を、id のコマンドへ渡し直し、TerminalReleased を返す（FR-117）。
// 実行中でないか、取り戻していなければ、渡し直さずに返す。
//
// req: FR-117
func (h *handover) release(id uint64) {
	h.mu.Lock()
	j := h.job
	ok := j != nil && j.id == id && !j.exited && j.reclaimed
	if ok {
		j.reclaimed = false
	}
	h.mu.Unlock()
	if ok {
		if ho, hok := h.tm.(Handover); hok {
			if err := ho.Release(); err != nil {
				h.logger.Error("端末を子プロセスへ渡し直せない", "err", err)
			}
			// コマンドは動いていて、本体は SIGINT と SIGQUIT を無視にしている（runCommands）ので、端末の ISIG を入れ直す。
			_ = ho.TerminalSignals(true) // 入れられなければ、<C-c> が子プロセスに届かないだけである
		}
	}
	h.send(protocol.TerminalReleased{ID: id})
}

// finish は、server との接続が切れたときに呼ぶ。問いの待ちを戻し、子プロセスが終わるのを待つ。
func (h *handover) finish() {
	h.closeOnce.Do(func() { close(h.closed) })
	h.wg.Wait()
}

func (h *handover) send(m protocol.ClientMessage) {
	if err := h.conn.Send(m); err != nil && !errors.Is(err, protocol.ErrClosed) {
		h.logger.Error("server へ送れない", "err", err)
	}
}

// runCommands は、req のコマンドを順に動かす。標準入出力は端末（ho.TTY）で、Input があれば標準入力をそれにし、
// DiscardOutput なら標準出力を捨てる。作業ディレクトリは client のもの、環境変数は client の環境の後に req.Env を足したものである。
// 最後に動かしたコマンドの終了コードを返す。0 でないコマンドがあれば、そこで止める。
// 起動できないか、シグナルで終わったら、その理由を errText に入れて返す。
//
// req: FR-79, FR-80, FR-116
func runCommands(req protocol.TerminalRun, ho Handover) (status int, errText string) {
	in, out := ho.TTY()
	env := append(os.Environ(), req.Env...)
	for _, c := range req.Commands {
		var cmd *exec.Cmd
		switch {
		case c.Shell != "":
			sh := os.Getenv("SHELL")
			if sh == "" {
				sh = "/bin/sh"
			}
			cmd = exec.Command(sh, "-c", c.Shell) //nolint:gosec // 利用者が :! に書いたコマンドをシェルで実行するのが、この関数の役目である
		case len(c.Argv) > 0:
			cmd = exec.Command(c.Argv[0], c.Argv[1:]...) //nolint:gosec // 設定の elevateCommand（sudo など）を起動する
		default:
			return 0, "動かすコマンドが空"
		}
		cmd.Env = env
		cmd.Stdin, cmd.Stdout, cmd.Stderr = in, out, out
		if c.PipeInput {
			cmd.Stdin = bytes.NewReader(c.Input)
		}
		if c.DiscardOutput {
			cmd.Stdout = nil // /dev/null
		}
		// 端末の ISIG を切ったまま起動し（子プロセスは本体の無視を引き継がない）、本体を無視にしてから ISIG を入れる（ファイルの doc）。
		err := cmd.Start()
		if err == nil {
			ho.IgnoreInterrupts()
			_ = ho.TerminalSignals(true) // 入れられなければ、<C-c> が子プロセスに届かないだけである
			err = cmd.Wait()
			_ = ho.TerminalSignals(false) // 切れなければ、無視を解いた後の <C-c> で本体が終わりうるが、続ける
			ho.RestoreInterrupts()
		}
		var ee *exec.ExitError
		switch {
		case err == nil:
			status = 0
		case errors.As(err, &ee) && ee.ExitCode() >= 0:
			return ee.ExitCode(), ""
		case errors.As(err, &ee):
			// シグナルで終わった。Vim 9.1 の :! が出す "Command terminated" に合わせる（PR #234 のレビューの推奨 5）。
			return 0, terminatedText
		default:
			return 0, fmt.Sprintf("%s を実行できない: %v", cmd.Args[0], err)
		}
	}
	return status, ""
}
