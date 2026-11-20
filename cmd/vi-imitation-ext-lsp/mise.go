package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/piplupflipper/vi-imitation/ext"
)

// 言語サーバーの実行ファイルが見つからないときの扱い（FR-143）。
//
// 言語サーバーと整形の道具を取ってくる仕組みは、vi-imitation には持たせず、mise に任せる（#278 のオーナーの決め）。
// 設定の servers の 1 つに mise の道具の ID（serverConfig.Mise）があり、Command が見つからないとき、次のとおりにする。
//
//   - 自動で入れない設定（既定）: 入れ方（mise use -g <道具の ID>）をメッセージで案内する。同じ案内は、拡張機能が動いている間に 2 回出さない
//   - 自動で入れる設定（serverConfig.AutoInstall）: mise where で入っているかを確かめ、入っていなければ mise install で入れてから、
//     mise exec <道具の ID> -- <Command> <Args> で起動する。mise が見つからなければ、案内に戻る
//
// 入れる段（mise where と mise install）と起動する段（mise exec）を分けるのは、入れるのに時間がかかる（数秒から数十秒）ためである。
// 入れている間は initialize の期限を数えず、別の期限（installTimeout）で待ち、進み具合を message/show で出す。
// どちらも起動の goroutine（client.start）の上で行い、ext/server のメッセージを処理する goroutine を止めない。
//
// mise に MISE_YES を渡さず、標準入力も渡さない。
// 作業のディレクトリ（ワークスペースの根）に信頼していない mise.toml があると、mise は止まるので、その出力をそのまま知らせる。
// MISE_YES で答えると、プロジェクトの設定を利用者の確認なしに信頼することになるためである（#278）。
//
// 言語の名前で分けた処理は持たない。どの言語サーバーも、設定の Command と Mise だけで扱う。

// errReported は、起動できない理由を利用者へもう知らせたので、改めて知らせないことを表す。
var errReported = errors.New("already reported to the user")

// miseLineInterval は、mise install の出力の行を message/show で出す間隔の下限である。
// それより短い間に届いた行はログにだけ書く。値は仮。
const miseLineInterval = time.Second

// miseTailLines は、mise install が失敗したときに、利用者へ知らせる mise の出力の行の数（末尾から）である。値は仮。
const miseTailLines = 5

// command は、言語サーバーを起動する実行ファイルと引数である。
type command struct {
	path string
	args []string
}

// prepare は、言語サーバーを起動するコマンドを決める。起動の goroutine から呼ぶ。
//
// Command が見つかれば、それをそのまま使う。見つからず、Mise があれば、FR-143 の扱い（このファイルの冒頭）にする。
// 起動できないときは誤りを返す。利用者へ理由を知らせ終えたときは errReported を返す。
// 案内だけを出したとき（自動で入れない設定、mise が無い）は、retry を true にする。
// 利用者が案内に従って入れた後、次にファイルを開いたときに探し直すためである。
//
// req: FR-143
func (c *client) prepare(ctx context.Context, root string) (cmd command, retry bool, err error) {
	path, err := exec.LookPath(c.cfg.Command)
	if err == nil {
		return command{path: path, args: c.cfg.Args}, false, nil
	}
	if c.cfg.Mise == "" || !errors.Is(err, exec.ErrNotFound) && !errors.Is(err, fs.ErrNotExist) {
		return command{}, false, err
	}
	if !c.cfg.autoInstall() {
		c.guide("")
		return command{}, true, errReported
	}
	mise, merr := exec.LookPath(c.mise)
	if merr != nil {
		c.x.logf("%s: mise: %v", c.name(), merr)
		c.guide("mise が見つからないので、自動では入れない。mise を入れた後、")
		return command{}, true, errReported
	}
	if !c.miseInstalled(ctx, mise, root) {
		if err := c.miseInstall(ctx, mise, root); err != nil {
			return command{}, false, err
		}
	}
	args := append([]string{"exec", c.cfg.Mise, "--", c.cfg.Command}, c.cfg.Args...)
	return command{path: mise, args: args}, false, nil
}

// guide は、言語サーバーの入れ方を message/show で案内する。note は、入れ方の前に置く説明である。
// 同じ実行ファイルと道具の ID の案内は、拡張機能が動いている間に 2 回出さない（別のファイルの種類の client からも）。
//
// req: FR-143
func (c *client) guide(note string) {
	if !c.x.firstGuide(c.cfg.Command + "\x00" + c.cfg.Mise) {
		return
	}
	c.x.showMessage(ext.MessageWarning, fmt.Sprintf("言語サーバー %s が見つからない。%smise use -g %s で入れられる", c.name(), note, c.cfg.Mise))
}

// miseInstalled は、mise where で、Mise の道具が入っているかを返す。
// mise where は、入っていなければ終了コード 1 で終わる。設定の誤り（信頼していない mise.toml など）でも 1 で終わるので、
// 入っていないと区別せずに false を返し、mise install の誤りとして知らせる。
func (c *client) miseInstalled(ctx context.Context, mise, root string) bool {
	ctx, cancel := context.WithTimeout(ctx, c.installTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, mise, "where", c.cfg.Mise) //nolint:gosec // 起動するのは mise で、道具の ID は利用者の設定である
	cmd.Dir = root
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	cmd.WaitDelay = time.Second
	if err := startProcess(cmd); err != nil {
		c.x.logf("%s: mise where: %v", c.name(), err)
		return false
	}
	if err := cmd.Wait(); err != nil {
		c.x.logf("%s: mise where %s: %v: %s", c.name(), c.cfg.Mise, err, strings.TrimSpace(out.String()))
		return false
	}
	return true
}

// miseInstall は、mise install で Mise の道具を入れる。起動の goroutine から呼ぶ。
// 始めたとき、mise の出力の行（miseLineInterval ごとに 1 行まで）、終えたときを message/show で知らせる。
// 失敗したら、mise の出力の末尾を含めて知らせ、errReported を返す。
// 拡張機能が止まるとき（ctx の取り消し）は、mise を終わらせ、知らせずに誤りを返す。
//
// req: FR-143
func (c *client) miseInstall(ctx context.Context, mise, root string) error {
	ctx, cancel := context.WithTimeout(ctx, c.installTimeout)
	defer cancel()
	c.x.showMessage(ext.MessageInfo, fmt.Sprintf("言語サーバー %s が見つからないので、mise で %s を入れている", c.name(), c.cfg.Mise))
	cmd := exec.CommandContext(ctx, mise, "install", c.cfg.Mise) //nolint:gosec // 起動するのは mise で、道具の ID は利用者の設定である
	cmd.Dir = root
	w := &lineWriter{line: c.miseLine}
	cmd.Stdout = w
	cmd.Stderr = w
	// mise が終わった後も、孫プロセス（npm など）が出力を持ち続けたときに、待ち続けないようにする。
	cmd.WaitDelay = time.Second
	err := startProcess(cmd)
	if err == nil {
		err = cmd.Wait()
	}
	w.flush()
	switch {
	case err == nil:
		c.x.logf("%s: mise install %s: done", c.name(), c.cfg.Mise)
		c.x.showMessage(ext.MessageInfo, fmt.Sprintf("mise で %s を入れた。言語サーバー %s を起動する", c.cfg.Mise, c.name()))
		return nil
	case errors.Is(ctx.Err(), context.Canceled):
		return fmt.Errorf("mise install %s: %w", c.cfg.Mise, ctx.Err())
	}
	c.x.logf("%s: mise install %s: %v", c.name(), c.cfg.Mise, err)
	detail := strings.Join(w.tail(), " ")
	if ctx.Err() != nil {
		detail = fmt.Sprintf("%s 以内に終わらなかった", c.installTimeout)
	} else if detail == "" {
		detail = err.Error()
	}
	c.x.showMessage(ext.MessageError, fmt.Sprintf("mise で %s を入れられない: %s", c.cfg.Mise, detail))
	return errReported
}

// miseLine は、mise install の出力の 1 行をログに書き、前に出してから miseLineInterval が過ぎていれば message/show で出す。
func (c *client) miseLine(line string, last time.Time) bool {
	c.x.logf("%s: mise: %s", c.name(), line)
	if time.Since(last) < miseLineInterval {
		return false
	}
	c.x.showMessage(ext.MessageInfo, "mise: "+line)
	return true
}

// lineWriter は、子プロセスの出力を行に分け、空でない行ごとに line を呼ぶ io.Writer である。
// 末尾の miseTailLines 行を覚える。exec.Cmd が Stdout と Stderr に同じ値を渡すので、Write は並行に呼ばれないが、
// tail は Wait の後に別の goroutine から読むので、mu で守る。
type lineWriter struct {
	// line は、行と、前に line が true を返した時刻を受け取り、行を利用者へ出したかを返す。
	line func(line string, last time.Time) bool

	mu    sync.Mutex
	buf   []byte
	lines []string
	shown time.Time
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		w.emit(string(w.buf[:i]))
		w.buf = w.buf[i+1:]
	}
	return len(p), nil
}

// flush は、改行で終わっていない最後の行を出す。
func (w *lineWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) > 0 {
		w.emit(string(w.buf))
		w.buf = nil
	}
}

// emit は、w.mu を持った状態で、1 行を扱う。進み具合の表示（"\r" で上書きする行）は、最後の部分だけを使う。
func (w *lineWriter) emit(s string) {
	if i := strings.LastIndexByte(strings.TrimRight(s, "\r"), '\r'); i >= 0 {
		s = s[i+1:]
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return
	}
	w.lines = append(w.lines, s)
	if len(w.lines) > miseTailLines {
		w.lines = w.lines[len(w.lines)-miseTailLines:]
	}
	if w.line(s, w.shown) {
		w.shown = time.Now()
	}
}

// tail は、覚えている末尾の行を返す。
func (w *lineWriter) tail() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.lines...)
}
