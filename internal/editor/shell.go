package editor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"syscall"
	"time"

	"github.com/piplupflipper/vi-imitation/internal/command"
	"github.com/piplupflipper/vi-imitation/internal/command/ex"
)

// shellOutputLimit は、:{range}! と :r ! の外部のコマンドの出力（標準出力と標準エラー出力の合計）の上限のバイト数である（仮）。
// 出力はメモリに溜めてから行に分けるので、上限が無いと :r !yes のようなコマンドでメモリを使い切り、保存していないバッファを失う。
// 値は、レジスターの 1 件の上限（command.DefaultMaxEntryBytes、1 MiB）と同じく「1 回の操作でバッファへ入れる量として十分に大きく、
// 溜めても本体が落ちない量」として決め、整形やソートで扱う大きなファイルが収まるように 64 倍の 64 MiB にした。
// 行に分けた写しとバッファへの適用で、一時的にこの数倍のメモリを使う。
// 超えたらプロセスグループを終わらせ、バッファは変えずに誤りを出す。テストが小さくするために変数にしている。
var shellOutputLimit = 64 << 20

// shellWaitDelay は、シェルが終わった後か中断した後に、パイプを持ったままの子孫（背景に回したコマンドや setsid で
// プロセスグループから抜けたコマンド）を待つ上限である（os/exec の Cmd.WaitDelay。仮）。
// 過ぎたらパイプを閉じて戻る。Vim は出力を一時ファイルで受けるので、シェルが終われば子孫を待たずに戻る。それに近づけるための短い値である。
const shellWaitDelay = 200 * time.Millisecond

// errShellOutputLimit は、外部のコマンドの出力が shellOutputLimit を超えたことを表す。
var errShellOutputLimit = errors.New("output limit exceeded")

// errShellBusy は、外部のコマンド（:!、:{range}!、:r !、権限を上げた保存）の実行中に、次の外部のコマンドを始めようとしたことを表す。
// 外部のコマンドは server に 1 つだけ動かせる（基本設計 10 章）。
var errShellBusy = errors.New("E319: 外部のコマンドの実行中です")

// shellJob は、実行中の外部のコマンドである。イベントループの goroutine だけが触る。
type shellJob struct {
	// bs は、コマンドを打ったバッファである。終わったら、このバッファの State の FinishShell に結果を渡す。
	bs *bufferState
	// client は、コマンドを打った client である。実行中は、この client のキーだけを列に残す（keyAllowed）。
	client *clientView
	req    *ex.ShellRequest
	// cancel は、子プロセスのプロセスグループを終わらせる。
	cancel context.CancelFunc
	// interrupted は、<C-c> で中断したかである。
	interrupted bool
	// replay は、マクロの再生の途中で始めたときの、再生の残りである（command.ErrShellPending）。
	// heldInput は、キーマップの展開の残りと貼り付けである。
	// どれも、出力を入れ終えた後に、この順で続ける。実行できなかった、中断したときは捨てる（Vim の再生とキーマップが誤りで止まるのと同じ）。
	replay []command.ReplaySegment
	heldInput

	// terminal は、client の端末を子プロセスへ渡した実行（:! と権限を上げた保存。handover.go）であることを表す。
	// 結果は shellDone ではなく、client の端末から TerminalResult で届く。id はその実行の ID である。
	terminal bool
	id       uint64
	// reclaimed は、実行中に、子孫が起動した待つだけの client（FR-83）のタブをコマンドを打った client に開き、
	// その client のキーを処理していることを表す（FR-117。keyAllowed）。端末を渡した実行では、端末も取り戻している。
	reclaimed bool
	// releasing は、タブが閉じられ、端末を子プロセスへ渡し直すのを待っている待つだけの client である。
	// client が渡し直したこと（TerminalResult.Released）を受けてから終わらせる。
	releasing []*waiter
	// save は、権限を上げた保存（FR-79、FR-116）のときの、終わった後の処理である。:! なら nil。
	save *elevatedSave
}

// shellOutcome は、外部のコマンドを動かす goroutine からイベントループへ送る結果である。
type shellOutcome struct {
	job    *shellJob
	out    []byte
	status int
	err    error
}

// startShell は、:{range}!{cmd} と :r !{cmd} の外部のコマンドを別の goroutine で動かし始める（FR-81、基本設計 4.9）。
//
// イベントループは止めない。実行中は、拡張機能のイベント、リサイズ、外での変更の確認の結果などを処理し続ける。
// FR-82 の環境変数のもとで子が起動した vi-imitation（FR-83）が server に繋いでも、イベントループと待ち合わないためである。
// コマンドを打った client のキーは <C-c>（中断）のほかは処理せずに列に残し、終わってから処理する。
// 他の client のキーは処理する（仮。基本設計 4.9。clients.go の doc）。
// 作業ディレクトリと、足す環境変数は、コマンドを打った client のもの（ClientInfo）である。
// 終わったら、結果を shellDone でイベントループへ送り、shellFinished がバッファを変える。
// :!{cmd}（req.Terminal）は startTerminalShell が、:terminal.popup（req.Popup）は端末多重化ソフトの中なら startPopupShell が動かし、
// 外なら :!{cmd} と同じく startTerminalShell が動かす（FR-168）。
//
// req: FR-81, FR-168
func (e *Editor) startShell(bs *bufferState, req *ex.ShellRequest, replay []command.ReplaySegment) {
	// :terminal.popup（FR-168）は、端末多重化ソフトの中ならポップアップで動かし、外部のコマンドの実行（e.shell）として持たない。
	// 外なら、下の :! と同じ経路で動かす。
	if req.Popup && e.startPopupShell(bs, req, replay) {
		return
	}
	if e.shell != nil {
		e.showError(errShellBusy)
		return
	}
	if req.Terminal {
		e.startTerminalShell(bs, req, replay)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	job := &shellJob{bs: bs, client: e.op, req: req, cancel: cancel, replay: replay}
	e.shell = job
	e.message = infoMessage("外部のコマンドを実行しています（<C-c> で中断します）")
	stop := e.stop
	dir, env := e.workDir(), e.childEnv()
	go func() {
		defer e.recoverPanic()
		out, status, err := runShell(ctx, cancel, req.Cmd, req.Input, dir, env)
		cancel()
		select {
		case e.shellDone <- shellOutcome{job: job, out: out, status: status, err: err}:
		case <-stop:
		}
	}()
	if stop != nil {
		go func() {
			// Run が戻ったら、子プロセスを残さない。
			select {
			case <-stop:
				cancel()
			case <-ctx.Done():
			}
		}()
	}
}

// interruptShell は、実行中の外部のコマンドがあり、それを打った client の <C-c> の印が立っていれば中断する。
// 印は下ろさない（キーの列の <C-c> までの先打ちは、終わった後に shellFinished が捨てる）。
// 端末を渡した実行（:!）は中断しない（<C-c> は端末から子プロセスへ届く）。待つだけの client のタブを編集している間（reclaimed）も、
// <C-c> は編集のキー（<Esc>）であり、中断しない。
func (e *Editor) interruptShell() {
	if e.shell != nil && !e.shell.terminal && !e.shell.reclaimed && e.shell.client.intr.Load() && !e.shell.interrupted {
		e.shell.interrupted = true
		e.shell.cancel()
	}
}

// shellFinished は、外部のコマンドの結果を、コマンドを打ったバッファへ入れる（command.State.FinishShell）。
// 中断したときは、Vim と同じく <C-c> までの先打ちのキーを捨てる。残ったキーは、この後に処理する。
//
// req: FR-81
func (e *Editor) shellFinished(o shellOutcome) {
	if o.job != e.shell {
		return
	}
	e.shell = nil
	c := o.job.client
	// 終わったのと同じ頃に <C-c> を打っていた（Run が intrCh より先に終わりを取り出した）ら、中断として扱い、再生を続けない。
	// 印だけを下ろして続けると、外部のコマンドを挟む再帰のマクロが <C-c> で止まらない（PR #249 の 4 回目の確認の必須）。
	pendingIntr := e.keys.interrupted(c)
	if pendingIntr {
		e.interrupt.Store(false)
		c.intr.Store(false)
		e.keys.dropThrough(interruptKey, c)
	}
	if !e.keys.empty() {
		signal(e.keys.ready)
	}
	bs := o.job.bs
	if !slices.Contains(e.bufs, bs) || c.detached {
		return
	}
	e.useClient(c)
	err := o.err
	switch {
	case o.job.interrupted, pendingIntr:
		err = command.ErrInterrupted
	case errors.Is(err, errShellOutputLimit):
		err = fmt.Errorf("外部のコマンドの出力が上限の %d バイトを超えたので、止めました", shellOutputLimit)
	case err != nil:
		err = fmt.Errorf("外部のコマンドを実行できません: %w", err)
	}
	e.message = noMessage
	if !e.finishShellRequest(bs, o.job.req, o.out, o.status, err, o.job.replay) {
		return
	}
	// 続きの中でまた外部のコマンドを始めたか、再生中の拡張機能の結果を待ち始めたら、まだ続けていないものをその待ちに引き継ぐ（resumeInput）。
	e.resumeInput(o.job.heldInput)
}

// finishShellRequest は、外部のコマンドの求め req の結果（出力 out、終了コード status、実行の誤り err）を、
// コマンドを打ったバッファ bs の command.State.FinishShell へ渡し、止めていたマクロの再生の残り replay を続ける。
// :! と :{range}! の終わり（shellFinished）と、:terminal.popup の起動を頼んだ後（startPopupShell）が、同じ手順で終えるための関数である。
// FinishShell が失敗したら（中断、実行できない、バッファが変わった）、失敗は handleResult が知らせ、再生とキーマップの展開の残りを捨てて false を返す。
// キーマップの展開の残りと貼り付け（heldInput）は続けない。外部のコマンドの実行の間に止めていたもの（shellJob.heldInput）は
// 呼び出し側が resumeInput で続ける。:terminal.popup は外部のコマンドの実行として持たず、止めていないので、続けるものが無い。
func (e *Editor) finishShellRequest(bs *bufferState, req *ex.ShellRequest, out []byte, status int, err error, replay []command.ReplaySegment) bool {
	if e.handleResult(bs, bs.state.FinishShell(req, out, status, err)) != nil {
		e.remap.reset()
		return false
	}
	if len(replay) > 0 {
		next := e.cur
		_ = e.handleResult(next, next.state.ContinueReplay(replay)) // 失敗は handleResult が知らせる
	}
	return true
}

// limitedBuffer は、shellOutputLimit までを溜める io.Writer である。超えたら over を立て、cancel で子を止め、誤りを返す。
// Cmd.Stdout と Cmd.Stderr に同じ値を渡すので、os/exec は 1 つの goroutine からだけ Write を呼ぶ。
type limitedBuffer struct {
	buf    bytes.Buffer
	limit  int
	over   bool
	cancel context.CancelFunc
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.buf.Len()+len(p) > b.limit {
		b.over = true
		b.cancel()
		return 0, errShellOutputLimit
	}
	return b.buf.Write(p)
}

// shellPath は、server が外部のコマンドを渡すシェル（環境変数 SHELL、無ければ /bin/sh）を返す（:{range}!、:r !、:terminal.popup）。
// :! は client が自分の環境の SHELL で動かす（internal/client の runCommands）。server の環境は server を起動した編集の client のものである。
func shellPath() string {
	if sh := os.Getenv("SHELL"); sh != "" {
		return sh
	}
	return "/bin/sh"
}

// runShell は、シェル（環境変数 SHELL、無ければ /bin/sh）に -c で cmdline を渡し、端末を渡さずに実行して、終わるまで待つ。
// イベントループの goroutine では呼ばない（startShell）。
//
// input を標準入力へ渡し（nil なら /dev/null）、標準出力と標準エラー出力を 1 つのパイプで受けて、書かれた順のまま返す
// （Vim の 'shellredir' の既定 ">%s 2>&1" と同じく、標準エラー出力もバッファへ入る）。
// 作業ディレクトリは dir、環境変数は本体の環境の後に env を足したものである（FR-82 の口）。
// 終了コードが 0 でなくても、出力と終了コードを返す（扱いは ex が Vim に合わせる）。シグナルで終わったら誤りにする。
// ctx が終わったら（<C-c>、Run の終わり、出力の上限）、プロセスグループごと SIGKILL で終わらせる。
// シェルが終わった後か終わらせた後は、パイプを持つ子孫を shellWaitDelay だけ待ち、過ぎたらパイプを閉じて戻る。
//
// req: FR-81
func runShell(ctx context.Context, cancel context.CancelFunc, cmdline string, input []byte, dir string, env []string) ([]byte, int, error) {
	cmd := exec.CommandContext(ctx, shellPath(), "-c", cmdline) //nolint:gosec // 利用者が :! に書いたコマンドをシェルで実行するのが、この関数の役目である。
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	out := &limitedBuffer{limit: shellOutputLimit, cancel: cancel}
	// 同じ値を渡すと、os/exec は 1 つのパイプで受けるので、標準出力と標準エラー出力の順が保たれる。
	cmd.Stdout, cmd.Stderr = out, out
	// 孫プロセス（パイプでつないだコマンド）までまとめて終わらせられるように、新しいプロセスグループの先頭にする。
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = shellWaitDelay
	err := cmd.Run()
	var ee *exec.ExitError
	switch {
	case out.over:
		return nil, 0, errShellOutputLimit
	case ctx.Err() != nil:
		return nil, 0, ctx.Err()
	case err == nil, errors.Is(err, exec.ErrWaitDelay):
		// ErrWaitDelay は、シェルは成功で終わったが、パイプを持つ子孫を待ちきれずに閉じたことを表す。出力はそこまでのものを使う。
		return out.buf.Bytes(), 0, nil
	case errors.As(err, &ee) && ee.ExitCode() >= 0:
		return out.buf.Bytes(), ee.ExitCode(), nil
	default:
		return nil, 0, err
	}
}
