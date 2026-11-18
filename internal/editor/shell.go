package editor

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// shellTimeout は、:{range}! と :r ! の外部のコマンドを待つ上限である（仮）。超えたらプロセスグループごと終わらせ、バッファは変えない。
// テストが短くするために変数にしている。
var shellTimeout = 30 * time.Second

// errShellInterrupted は、外部のコマンドの実行を <C-c> で中断したことを表す。
var errShellInterrupted = errors.New("中断しました")

// runShell は、ex.State.Shell である（:{range}!{cmd} と :r !{cmd}。FR-81、基本設計 4.9）。
//
// シェル（環境変数 SHELL、無ければ /bin/sh）に -c で cmd を渡し、端末を渡さずに起動する。
// input を標準入力へ渡し（nil なら /dev/null）、標準出力と標準エラー出力を 1 つのパイプで受けて、書かれた順のまま返す
// （Vim の 'shellredir' の既定 ">%s 2>&1" と同じく、標準エラー出力もバッファへ入る）。
// 作業ディレクトリは Config.WorkDir、環境変数は本体の環境の後に Config.ChildEnv を足したものである（FR-82 の口）。
// 終了コードが 0 でなくても、出力と終了コードを返す（扱いは ex が Vim に合わせる）。シグナルで終わったら誤りにする。
//
// 結果をその Ex コマンドの続き（"|" の後と :g の次の行）とアンドゥの 1 単位に入れるため、イベントループの上で終わるまで待つ。
// 待つ間は画面を描き直さず、キーも処理しない（先打ちのキーは列に残り、終わってから処理する）。
// 止める時間には上限（shellTimeout）を置き、<C-c> でも中断できる。どちらもプロセスグループごと SIGKILL で終わらせ、誤りを返す。
//
// req: FR-81
func (e *Editor) runShell(cmdline string, input []byte) ([]byte, int, error) {
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "/bin/sh"
	}
	cmd := exec.Command(sh, "-c", cmdline) //nolint:gosec // 利用者が :! に書いたコマンドをシェルで実行するのが、この関数の役目である。
	cmd.Dir = e.cfg.WorkDir
	cmd.Env = append(os.Environ(), e.cfg.ChildEnv...)
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	var out bytes.Buffer
	// 同じ値を渡すと、os/exec は 1 つのパイプで受けるので、標準出力と標準エラー出力の順が保たれる。
	cmd.Stdout, cmd.Stderr = &out, &out
	// 孫プロセス（パイプでつないだコマンド）までまとめて終わらせられるように、新しいプロセスグループの先頭にする。
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, 0, err
	}
	done := make(chan error, 1)
	go func() {
		defer e.recoverPanic()
		done <- cmd.Wait()
	}()
	timer := time.NewTimer(shellTimeout)
	defer timer.Stop()
	var ctxDone <-chan struct{}
	if e.ctx != nil {
		ctxDone = e.ctx.Done()
	}
	var stop error
	for stop == nil {
		select {
		case err := <-done:
			return shellResult(out.Bytes(), err)
		case <-e.intrCh:
			if e.interrupt.Load() {
				stop = errShellInterrupted
			}
		case <-timer.C:
			stop = fmt.Errorf("%v を過ぎても終わりません", shellTimeout)
		case <-ctxDone:
			stop = e.ctx.Err()
		}
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	<-done //nolint:errcheck // 終わらせたプロセスの終わり方（killed）は使わず、stop を返す
	return nil, 0, stop
}

// shellResult は、cmd.Wait の結果を、出力と終了コードと誤りにする。
func shellResult(out []byte, err error) ([]byte, int, error) {
	var ee *exec.ExitError
	switch {
	case err == nil:
		return out, 0, nil
	case errors.As(err, &ee) && ee.ExitCode() >= 0:
		return out, ee.ExitCode(), nil
	default:
		return nil, 0, err
	}
}
