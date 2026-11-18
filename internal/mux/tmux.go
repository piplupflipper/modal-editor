package mux

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"
)

// Tmux は、tmux 用の実装である（基本設計 1.3）。
//
// server は tmux のペインの中のプロセスではないので、どのコマンドにも対象のペインを -t で渡す。
// -t を省くと、対象はアクティブな window になり、起動したペインの window とは限らない（基本設計 1.2）。
type Tmux struct {
	// Bin は、tmux の実行ファイルである。
	Bin string
	// Socket は、tmux のサーバーのソケットのパスである（環境変数 TMUX の最初のカンマの前）。-S に渡す。
	// 空なら -S を付けず、tmux の既定のサーバーを使う。
	Socket string
	// Pane は、この層を作ったプロセスがいるペインの ID（環境変数 TMUX_PANE）である。
	Pane string
}

var _ Mux = (*Tmux)(nil)

// Kind は KindTmux を返す。
func (*Tmux) Kind() Kind { return KindTmux }

// CurrentPane は、TMUX_PANE のペインの ID を返す。tmux のペインの ID は、ペインを別の window へ動かしても変わらない。
func (t *Tmux) CurrentPane(context.Context) (string, error) {
	return t.Pane, nil
}

// Unit は、TMUX_PANE のペインのある window を、tmux のサーバーのソケットのパス、セッションの ID、window の ID を
// コロンでつないだ文字列で返す（基本設計 1.2 の規則 2）。
func (t *Tmux) Unit(ctx context.Context) (string, error) {
	out, err := t.run(ctx, "display-message", "-p", "-t", t.Pane, "#{socket_path}:#{session_id}:#{window_id}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// SplitPane は、split-window でペインを分割して開く。
// Focus が false なら -d を付け、フォーカスを元のペインに残す。
func (t *Tmux) SplitPane(ctx context.Context, opts SplitOptions) (string, error) {
	if err := checkCommand(opts.Command); err != nil {
		return "", err
	}
	if err := checkPercent("size", opts.Size); err != nil {
		return "", err
	}
	args := []string{"split-window", "-P", "-F", "#{pane_id}", "-t", t.target(opts.Target)}
	switch opts.Direction {
	case Right:
		args = append(args, "-h")
	case Down:
		args = append(args, "-v")
	}
	if !opts.Focus {
		args = append(args, "-d")
	}
	if opts.Size > 0 {
		args = append(args, "-l", strconv.Itoa(opts.Size)+"%")
	}
	args = append(args, t.commandArgs("-c", opts.Command)...)
	out, err := t.run(ctx, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// OpenPopup は、display-popup -E でポップアップを開く。
//
// display-popup は、ポップアップを出す tmux の client（-c）が要る。server は tmux の client ではないので、
// 元のペインを表示している tmux の client を探して -c に渡す（tmuxClient）。
// display-popup はポップアップが閉じるまで戻らないので、プロセスを起動したら終わりを待たずに戻る。
// このため、起動した後に tmux が返した誤りは返せない。
func (t *Tmux) OpenPopup(ctx context.Context, opts PopupOptions) error {
	if err := checkCommand(opts.Command); err != nil {
		return err
	}
	if err := checkPercent("width", opts.Width); err != nil {
		return err
	}
	if err := checkPercent("height", opts.Height); err != nil {
		return err
	}
	target := t.target(opts.Target)
	client, err := t.client(ctx, target)
	if err != nil {
		return err
	}
	args := []string{"display-popup", "-E", "-c", client, "-t", target}
	if opts.Width > 0 {
		args = append(args, "-w", strconv.Itoa(opts.Width)+"%")
	}
	if opts.Height > 0 {
		args = append(args, "-h", strconv.Itoa(opts.Height)+"%")
	}
	args = append(args, t.commandArgs("-d", opts.Command)...)
	// ポップアップの寿命は呼び出しの ctx に結びつけないので、exec.CommandContext を使わない。
	cmd := exec.Command(t.Bin, t.withSocket(args)...) //nolint:gosec // 実行するのは tmux で、引数は本体が組み立てたもの
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("tmux display-popup: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// ClosePopup は、display-popup -C で、target のペインを表示している tmux の client のポップアップを閉じる。
func (t *Tmux) ClosePopup(ctx context.Context, target string) error {
	target = t.target(target)
	client, err := t.client(ctx, target)
	if err != nil {
		return err
	}
	_, err = t.run(ctx, "display-popup", "-C", "-c", client)
	return err
}

// FocusPane は、ペインのある window を選び、そのペインを選ぶ。
// ペインを別の window へ動かした client（FR-112）にもフォーカスを移せるように、window も選ぶ。
func (t *Tmux) FocusPane(ctx context.Context, pane string) error {
	_, err := t.run(ctx, "select-window", "-t", pane, ";", "select-pane", "-t", pane)
	return err
}

// ClosePane は、kill-pane でペインを閉じる。
func (t *Tmux) ClosePane(ctx context.Context, pane string) error {
	_, err := t.run(ctx, "kill-pane", "-t", pane)
	return err
}

// target は、空なら TMUX_PANE のペインを返す。
func (t *Tmux) target(pane string) string {
	if pane == "" {
		return t.Pane
	}
	return pane
}

// commandArgs は、起動するディレクトリ（dirFlag で渡す）、環境変数（-e）、コマンドの文字列の引数を返す。
// 環境変数は、引数の並びが毎回同じになるように名前の順に並べる。
func (*Tmux) commandArgs(dirFlag string, c Command) []string {
	var args []string
	if c.Dir != "" {
		args = append(args, dirFlag, c.Dir)
	}
	keys := make([]string, 0, len(c.Env))
	for k := range c.Env {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		args = append(args, "-e", k+"="+c.Env[k])
	}
	return append(args, "--", shellJoin(c.Args))
}

// client は、target のペインを表示している tmux の client の名前を返す。
// target のペインの window を表示している client を優先し、無ければ同じセッションの client を使う。
// 候補が複数なら、最後に操作された（client_activity の大きい）ものを選ぶ。
func (t *Tmux) client(ctx context.Context, target string) (string, error) {
	where, err := t.run(ctx, "display-message", "-p", "-t", target, "#{session_id}\t#{window_id}")
	if err != nil {
		return "", err
	}
	session, window, _ := strings.Cut(strings.TrimSpace(where), "\t")
	out, err := t.run(ctx, "list-clients", "-F", "#{client_name}\t#{session_id}\t#{window_id}\t#{client_activity}")
	if err != nil {
		return "", err
	}
	type candidate struct {
		name     string
		rank     int // 2: 同じ window、1: 同じセッション
		activity int64
	}
	var best *candidate
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 4 {
			continue
		}
		c := candidate{name: f[0]}
		switch {
		case f[1] == session && f[2] == window:
			c.rank = 2
		case f[1] == session:
			c.rank = 1
		default:
			continue
		}
		c.activity, _ = strconv.ParseInt(f[3], 10, 64)
		if best == nil || c.rank > best.rank || (c.rank == best.rank && c.activity > best.activity) {
			best = &c
		}
	}
	if best == nil {
		return "", fmt.Errorf("tmux: no client is attached to the session of pane %s", target)
	}
	return best.name, nil
}

// withSocket は、Socket があれば先頭に -S を足す。
func (t *Tmux) withSocket(args []string) []string {
	if t.Socket == "" {
		return args
	}
	return append([]string{"-S", t.Socket}, args...)
}

// run は、tmux のコマンドを起動し、標準出力を返す。失敗したら標準エラー出力を誤りに含める。
func (t *Tmux) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, t.Bin, t.withSocket(args)...) //nolint:gosec // 実行するのは tmux で、引数は本体が組み立てたもの
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return "", fmt.Errorf("tmux %s: %w", args[0], err)
		}
		return "", fmt.Errorf("tmux %s: %s: %w", args[0], msg, err)
	}
	return stdout.String(), nil
}
