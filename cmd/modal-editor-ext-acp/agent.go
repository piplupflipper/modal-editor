package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// agentConfig は、エージェントの起動の方法である（設定の agent。項目の名前は仮）。
type agentConfig struct {
	// Command は、エージェントの実行ファイルの名前かパスである。名前なら PATH から探す。
	Command string `json:"command"`
	// Args は、Command に渡す引数である。
	Args []string `json:"args,omitempty"`
	// Env は、拡張機能の環境変数に足す環境変数である。
	Env map[string]string `json:"env,omitempty"`
}

// agentProcess は、起動したエージェントの子プロセスと、その標準入出力の ACP の接続である。
type agentProcess struct {
	cmd   *exec.Cmd
	conn  *lineConn
	stdin io.WriteCloser

	// sessionID は、session/new で作ったセッションの ID である。作る前は空。
	// session/new の応答を受け取ってから書き、その後は変えない。書くのは runPrompt の goroutine だけで、client.mu で守る。
	sessionID string

	// exited は、子プロセスが終わり、接続の読み取りも終わったときに閉じる。exitErr はその後に読める。
	exited  chan struct{}
	exitErr error

	stopOnce sync.Once
	// stopping は、拡張機能が終わらせようとしたことを表す。異常終了の知らせを出すかどうかの判断に使う。
	stopping chan struct{}
}

// startAgentProcess は、cfg のエージェントを作業ディレクトリ dir で起動し、標準入出力に ACP の接続を張る。
// エージェントの標準エラー出力は、1 行ずつ logger へ書く（ACP の Transports はエージェントのログを標準エラー出力に置く）。
// 子プロセスが終わると、接続を閉じてから onExit を呼ぶ。
func startAgentProcess(cfg agentConfig, dir string, logger *log.Logger,
	onRequest requestHandler, onNotification notificationHandler, onExit func(*agentProcess),
) (*agentProcess, error) {
	cmd := exec.Command(cfg.Command, cfg.Args...) //nolint:gosec // G204: 利用者が設定に書いたエージェントを起動する
	cmd.Dir = dir
	cmd.Env = os.Environ()
	for k, v := range cfg.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := startAgent(cmd); err != nil {
		return nil, fmt.Errorf("start agent %q: %w", cfg.Command, err)
	}
	p := &agentProcess{
		cmd:      cmd,
		stdin:    stdin,
		conn:     newLineConn(stdin, onRequest, onNotification),
		exited:   make(chan struct{}),
		stopping: make(chan struct{}),
	}
	var logs sync.WaitGroup
	logs.Add(1)
	go func() {
		defer logs.Done()
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			logger.Printf("agent: %s", sc.Text())
		}
	}()
	go func() {
		_ = p.conn.serve(stdout)
		// Wait は、標準出力と標準エラー出力を読み終えてから呼ぶ（os/exec の StdoutPipe の doc）。
		logs.Wait()
		p.exitErr = cmd.Wait()
		close(p.exited)
		onExit(p)
	}()
	return p, nil
}

// stop は、エージェントを終わらせ、終わるまで待つ。
// まず標準入力を閉じ（多くのエージェントは標準入力の EOF で終わる）、grace を過ぎたらプロセスグループへ SIGTERM を、
// さらに grace を過ぎたら SIGKILL を送る。2 回目からは、1 回目と同じく終わるのを待つだけである。
// SIGKILL の後も、grace を過ぎたら待つのをやめる。プロセスグループの外へ出た孫のプロセスが標準出力を開いたままだと、
// 接続の読み取りが終わらないためである。
func (p *agentProcess) stop(grace time.Duration) {
	p.stopOnce.Do(func() {
		close(p.stopping)
		_ = p.stdin.Close()
		if p.waitExit(grace) {
			return
		}
		signalGroup(p.cmd, syscall.SIGTERM)
		if p.waitExit(grace) {
			return
		}
		signalGroup(p.cmd, syscall.SIGKILL)
	})
	p.waitExit(grace)
}

func (p *agentProcess) waitExit(d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-p.exited:
		return true
	case <-t.C:
		return false
	}
}

// stopped は、stop が呼ばれたかどうかを返す。
func (p *agentProcess) stopped() bool {
	select {
	case <-p.stopping:
		return true
	default:
		return false
	}
}
