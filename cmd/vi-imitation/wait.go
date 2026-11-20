package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"syscall"

	"github.com/piplupflipper/vi-imitation/internal/editor"
	"github.com/piplupflipper/vi-imitation/internal/protocol"
)

// このファイルは、エディタの中で起動したエディタ（基本設計 4.9。FR-82、FR-83、FR-84）と、端末を子プロセスへ渡している間のシグナルの扱いである。

// withChildEnv は、cfg に、本体が起動する子プロセス（:! と :{range}! と :r ! のシェル、拡張機能とその子プロセス）の環境変数を足して返す（FR-82）。
// VI_IMITATION_SOCKET を server のソケットのパス sock に、EDITOR と GIT_EDITOR を本体の実行ファイルの絶対パスにする。
// その子プロセスが起動した vi-imitation は、待つだけの client になる（waitClient）。
// 実行ファイルのパスを決められなければ、EDITOR と GIT_EDITOR は足さない（利用者の環境のものを残す）。
//
// req: FR-82
func withChildEnv(cfg editor.Config, sock string, logger *slog.Logger) editor.Config {
	env := []string{envSocket + "=" + sock}
	if exe, err := os.Executable(); err == nil {
		env = append(env, "EDITOR="+exe, "GIT_EDITOR="+exe)
	} else {
		logger.Warn("実行ファイルのパスを決められないので、子プロセスの EDITOR を変えない", "err", err)
	}
	cfg.ChildEnv = append(slices.Clone(cfg.ChildEnv), env...)
	cfg.Host.Env = append(slices.Clone(cfg.Host.Env), env...)
	return cfg
}

// errWaitServerLost は、待つだけの client が、終わりの知らせを受けずに server との接続を失ったことを表す。
var errWaitServerLost = errors.New("server との接続が切れた（ファイルの編集を待てない）")

// waitClient は、環境変数 VI_IMITATION_SOCKET の server に繋いだ conn で、待つだけの client として動く（FR-83。基本設計 4.9）。
//
// 端末には描かず、キーも受けない。files（絶対パスにして送る）を開かせ、server がそれらのタブがすべて閉じられたことを知らせる
// （理由の無い終わりの知らせ）まで待ち、nil で戻る（終了コード 0。git はコミットを続ける）。
// ファイルを開けない、タブを開いた client が終わったなど、理由付きの終わりの知らせを受けたら、その理由の誤りで戻る（0 でない終了コード。
// git はコミットをやめる）。終わりの知らせを受けずに接続が切れたときも誤りで戻る。
// stdin が nil でなければ、起動の引数の - で標準入力から読んだその中身を、名前の無いバッファのタブとして開かせる（FR-158）。
// files と stdin がどちらも空なら、名前の無い空のタブを開かせる。
// タブを開く先は、環境変数 VI_IMITATION_CLIENT（editor.EnvClient）の client（:! を実行した client）である。
//
// req: FR-83
func waitClient(conn protocol.ClientConn, getenv func(string) string, files []string, stdin *string, readOnly bool) error {
	defer func() { _ = conn.Close() }() // 閉じる誤りは扱えない
	abs := make([]string, len(files))
	for i, f := range files {
		a, err := filepath.Abs(f)
		if err != nil {
			return fmt.Errorf("%s の絶対パスを決められない: %w", f, err)
		}
		abs[i] = a
	}
	hello := protocol.Hello{Kind: protocol.KindWait, Files: abs, ReadOnly: readOnly, Stdin: stdin}
	if wd, err := os.Getwd(); err == nil {
		hello.WorkDir = wd
	}
	if v := getenv(editor.EnvClient); v != "" {
		if id, err := strconv.ParseUint(v, 10, 64); err == nil {
			hello.Parent = id
		}
	}
	if err := conn.Send(hello); err != nil {
		return fmt.Errorf("server に繋げない: %w", err)
	}
	if stdin != nil {
		// 送り終えた標準入力の中身は、タブが閉じられるまで待つ間に要らない（FR-158）。hello と stdin は、この後は使わない。
		releaseMemory()
	}
	for {
		m, err := conn.Recv()
		switch {
		case errors.Is(err, io.EOF):
			return errWaitServerLost
		case err != nil:
			return fmt.Errorf("%w: %w", errWaitServerLost, err)
		}
		if x, ok := m.(protocol.Exit); ok {
			if x.Reason != "" {
				return errors.New(x.Reason)
			}
			return nil
		}
	}
}

// signalContext は、SIGTERM、SIGHUP、SIGINT、SIGQUIT を受けたら取り消される ctx を返す（signal.NotifyContext と同じく、
// 取り消しの原因は「<シグナル> signal received」）。
// :! の子プロセスは本体と同じプロセスグループで動くので、子プロセスへ打った <C-c> と <C-\> は本体にも届く（Vim と同じ）。
// その間は internal/client が signal.Ignore で SIGINT と SIGQUIT を無視にするので、この受け取りには来ない（無視の間に起きたシグナルはカーネルが捨てる）。
// 子プロセスが終わったら、renotify で SIGINT と SIGQUIT の受け取りを登録し直す（term.Terminal.SetInterruptRestore）。
// raw モードの間は、<C-c> と <C-\> はキーとして届き、シグナルにならない。
// stop は、シグナルの受け付けを止める。
func signalContext() (ctx context.Context, stop, renotify func()) {
	ctx, cancel := context.WithCancelCause(context.Background())
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT, syscall.SIGQUIT)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case sig := <-sigs:
				cancel(errors.New(sig.String() + " signal received"))
			case <-done:
				return
			}
		}
	}()
	return ctx, func() {
			signal.Stop(sigs)
			close(done)
			cancel(context.Canceled)
		}, func() {
			signal.Notify(sigs, syscall.SIGINT, syscall.SIGQUIT)
		}
}
