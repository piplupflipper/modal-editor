package editor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/piplupflipper/vi-imitation/ext"
	"github.com/piplupflipper/vi-imitation/internal/buffer"
	"github.com/piplupflipper/vi-imitation/internal/command"
	"github.com/piplupflipper/vi-imitation/internal/command/ex"
	"github.com/piplupflipper/vi-imitation/internal/editcmd"
)

// 端末を子プロセスへ渡す実行と、待つだけの client（基本設計 4.7、4.9。FR-79、FR-80、FR-82、FR-83、FR-116、FR-117）。
//
// # 端末を渡す実行
//
// :!{cmd}（FR-80）と、権限を上げた保存（FR-79、FR-116）は、client の端末を子プロセスへ渡して動かす。端末を持つのは client なので、
// イベントループは、コマンドを打った client の端末（TerminalHandover）へ実行を頼み、結果を TermEvent.Terminal で受け取る。
// 実行は外部のコマンド（shellJob）として 1 つだけ持ち、実行中はコマンドを打った client のキーを処理しない（:{range}! と同じ）。
// 他の client のキーと拡張機能のイベントは処理を続ける。
//
// # 待つだけの client
//
// 本体が起動する子プロセス（:! のシェル、:{range}! のシェル、拡張機能）の環境変数には、server のソケットのパス（VI_IMITATION_SOCKET）と
// EDITOR、GIT_EDITOR を入れる（FR-82。値は呼び出し側が Config.ChildEnv で渡す）。:! と :{range}! では、コマンドを打った client の識別子
// （EnvClient）も入れる。この環境変数のもとで起動した vi-imitation は、待つだけの client として server に繋ぎ（Wait）、
// 渡されたファイルを、その識別子の client のタブで開かせ、それらのタブがすべて閉じられるまで待つ（FR-83）。
// 識別子が無い（拡張機能の子プロセスなど）か、その client がもういなければ、操作中の client のタブに開く。
//
// 開いた先の client が、外部のコマンドの実行中なら（git commit の中で git が起動したエディタ）、その client のキーを処理するようにし、
// 端末を渡した実行なら、端末を取り戻させて画面を描く（FR-117。reclaimed）。
// タブがすべて閉じられたら、端末を子プロセスへ渡し直させ、渡し直したことを受けてから待つだけの client を終わらせる。
// 待つだけの client が終わると、git がコミットを続ける。

// EnvClient は、:! と :{range}! の子プロセスの環境変数に入れる、コマンドを打った client の識別子の名前である（仮。基本設計 4.9）。
const EnvClient = "VI_IMITATION_CLIENT"

// DefaultElevateCommand は、権限を上げて書くときに tee の前に付けるコマンドの既定値である（FR-79）。
var DefaultElevateCommand = []string{"sudo"}

// TerminalHandover は、端末を子プロセスへ渡せる client の端末が実装する口である（internal/server の clientTerm）。
// イベントループの goroutine から呼び、送り終えるのを待たない。結果は Terminal.Input の TermEvent.Terminal で届く。
// 実装しない端末では、:! と権限を上げた保存は使えない。
type TerminalHandover interface {
	// HandsOverTerminal は、この client が端末の受け渡しを扱えるか（client が Hello で宣言したか）を返す。
	// false なら、:! と権限を上げた保存を E319 で断る（版の違う client が頼みを読み飛ばし、そのキーを待ち続けないため。PR #234 のレビューの必須 1）。
	HandsOverTerminal() bool
	// RunInTerminal は、端末を子プロセスへ渡して job を動かすよう頼む。終わったら TerminalResult（Released が false）が届く。
	RunInTerminal(job TerminalJob)
	// ReclaimTerminal は、実行中の id の間に、端末を取り戻して画面を描くよう頼む（FR-117）。
	ReclaimTerminal(id uint64)
	// ReleaseTerminal は、ReclaimTerminal で取り戻した端末を、子プロセスへ渡し直すよう頼む。渡し直したら Released の TerminalResult が届く。
	ReleaseTerminal(id uint64)
}

// TerminalJob は、端末を子プロセスへ渡して動かすもの（protocol.TerminalRun と同じ形）である。
type TerminalJob struct {
	ID       uint64
	Commands []TerminalCommand
	// Env は、子プロセスの環境変数に、client の環境の後に足す "名前=値" の列である。
	Env []string
	// Prompt は、終わった後に「続けるには ENTER を押してください」を出してキーを待つことを表す（:!）。
	Prompt bool
}

// TerminalCommand は、TerminalJob のコマンド 1 つ（protocol.TerminalCommand と同じ形）である。
type TerminalCommand struct {
	// Shell が空でなければ、client のシェルに -c で渡すコマンドである。そうでなければ Argv を起動する。
	Shell string
	Argv  []string
	// Input は、PipeInput のときに標準入力へ渡すテキストである。
	Input     []byte
	PipeInput bool
	// DiscardOutput は、標準出力を捨てることを表す。
	DiscardOutput bool
}

// TerminalResult は、端末を渡した実行の結果（protocol.TerminalDone か protocol.TerminalReleased）である。
type TerminalResult struct {
	ID uint64
	// Released は、ReleaseTerminal を受けて端末を渡し直したこと（TerminalReleased）を表す。false なら実行が終わったこと（TerminalDone）である。
	Released bool
	// Status は、最後に動かしたコマンドの終了コード、Err は起動できなかったかシグナルで終わったときの理由である。
	Status int
	Err    string
}

// termResult は、client c の端末から届いた TerminalResult である。
type termResult struct {
	c *clientView
	r TerminalResult
}

// terminalEnv は、client c の子プロセスへ渡す環境変数（Config.ChildEnv と、c の識別子）を返す。
// client は自分の環境で子プロセスを起動するので、ClientInfo.Env は足さない。
func (e *Editor) terminalEnv(c *clientView) []string {
	env := slices.Clone(e.cfg.ChildEnv)
	if c != nil && c.info.ID != 0 {
		env = append(env, EnvClient+"="+strconv.FormatUint(uint64(c.info.ID), 10))
	}
	return env
}

// handover は、読み込んでいる client の端末が TerminalHandover を実装し、端末の受け渡しを扱えると宣言していれば返す。
func (e *Editor) handover() (TerminalHandover, bool) {
	h, ok := e.term.(TerminalHandover)
	if !ok || !h.HandsOverTerminal() {
		return nil, false
	}
	return h, true
}

// startTerminalShell は、:!{cmd} を、操作中の client の端末を子プロセスへ渡して動かし始める（FR-80。基本設計 4.9）。
// 端末を渡せない client（server を通さずにつないだ端末）では誤りを出す。
// 終わったら terminalResult が shellFinished へ渡し、止めていた再生とキーマップの展開の残りを続ける。
//
// req: FR-80, FR-82
func (e *Editor) startTerminalShell(bs *bufferState, req *ex.ShellRequest, replay []command.ReplaySegment) {
	h, ok := e.handover()
	if !ok {
		e.showError(errors.New("E319: この client の端末は子プロセスへ渡せないので、:! を使えません"))
		return
	}
	e.termSeq++
	job := &shellJob{bs: bs, client: e.op, req: req, cancel: func() {}, replay: replay, terminal: true, id: e.termSeq}
	e.shell = job
	h.RunInTerminal(TerminalJob{
		ID:       job.id,
		Commands: []TerminalCommand{{Shell: req.Cmd}},
		Env:      e.terminalEnv(e.op),
		Prompt:   true,
	})
}

// terminalResult は、client の端末から届いた、端末を渡した実行の結果を扱う。
func (e *Editor) terminalResult(tr termResult) {
	job, r := e.shell, tr.r
	if job == nil || !job.terminal || job.id != r.ID || job.client != tr.c {
		return // 忘れた実行（client が終わった）か、知らない実行の結果
	}
	if r.Released {
		ws := job.releasing
		job.releasing = nil
		for _, w := range ws {
			e.endWaiter(w, "")
		}
		return
	}
	// 渡し直すのを待っていた待つだけの client は、子プロセスが先に終わったので、もう待たない。
	for _, w := range job.releasing {
		e.endWaiter(w, "")
	}
	job.releasing = nil
	if job.save != nil {
		e.shell = nil
		if !e.keys.empty() {
			signal(e.keys.ready)
		}
		e.elevatedDone(job, r)
		return
	}
	var err error
	if r.Err != "" {
		err = errors.New(r.Err)
	}
	e.shellFinished(shellOutcome{job: job, status: r.Status, err: err})
}

// dropTerminalJob は、端末を渡した実行を打った client が終わったときに、その実行を忘れる。結果はもう届かない。
// 渡し直すのを待っていた待つだけの client は終わらせる。
func (e *Editor) dropTerminalJob() {
	job := e.shell
	e.shell = nil
	for _, w := range job.releasing {
		e.endWaiter(w, "")
	}
	job.releasing = nil
}

// WaitInfo は、待つだけの client（FR-83）が server に知らせる情報である。
type WaitInfo struct {
	// Files は、開かせるファイルの絶対パスである。Files と Stdin がどちらも空なら、名前の無い空のタブを 1 つ開く。
	Files []string
	// ReadOnly は、Files と Stdin を編集を禁止した状態で開くことを表す（-R）。
	ReadOnly bool
	// Stdin は、待つだけの client が起動の引数の - で標準入力から読んだ中身である（FR-158）。nil なら - を書いていない。
	// Files より前に、名前の無いバッファのタブとして開く（ClientInfo.Stdin と同じ）。
	Stdin *string
	// Parent は、タブを開く先の client の識別子（環境変数 EnvClient の値）である。0 か、その client がいなければ、操作中の client に開く。
	Parent ClientID
	// Done は、Files のタブがすべて閉じられたときに、イベントループから 1 回だけ呼ぶ。待つのをやめる理由（タブを開いた client が終わった）が
	// あれば reason に入れる。Run が戻った後と、Wait の cancel の後は呼ばない。イベントループを止めないよう、すぐに戻る関数にする。
	Done func(reason string)
}

// waiter は、待つだけの client 1 つである。
type waiter struct {
	target *clientView
	bufs   []*bufferState
	done   func(reason string)
	ended  bool
}

type waitRequest struct {
	info  WaitInfo
	reply chan waitReply
}

type waitReply struct {
	w   *waiter
	err error
}

// Wait は、待つだけの client を足す（FR-83）。Run を動かしている間に、他の goroutine から呼ぶ。
// イベントループが info.Files をタブとして開き、開いたら戻る。開けなければその誤りを返す。
// 戻った cancel は、待つだけの client の接続が切れたときに呼ぶ（タブは閉じず、Done も呼ばない）。
// Run が終わっているか、ctx が終わったら ErrStopped か ctx.Err() を返す。
//
// req: FR-83
func (e *Editor) Wait(ctx context.Context, info WaitInfo) (cancel func(), err error) {
	req := waitRequest{info: info, reply: make(chan waitReply, 1)}
	select {
	case e.waitCh <- req:
	case <-e.finished:
		return nil, ErrStopped
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	var r waitReply
	select {
	case r = <-req.reply:
	case <-e.finished:
		return nil, ErrStopped
	}
	if r.err != nil {
		return nil, r.err
	}
	return func() {
		select {
		case e.waitGoneCh <- r.w:
		case <-e.finished:
		}
	}, nil
}

// findClient は、識別子が id の繋いでいる client を返す。いなければ nil を返す。
func (e *Editor) findClient(id ClientID) *clientView {
	if id == 0 {
		return nil
	}
	for _, c := range e.clients {
		if c.info.ID == id && !c.detached {
			return c
		}
	}
	return nil
}

// addWaiter は、イベントループで、待つだけの client のファイルをタブとして開く（FR-83）。
// 開く先の client が外部のコマンドを実行中なら、その client のキーを処理するようにし、端末を渡した実行なら端末を取り戻させる（FR-117）。
//
// req: FR-83, FR-117
func (e *Editor) addWaiter(req waitRequest) {
	target := e.findClient(req.info.Parent)
	explicit := target != nil
	if target == nil {
		target = e.op
	}
	if target == nil || len(e.clients) == 0 {
		req.reply <- waitReply{err: ErrStopped}
		return
	}
	w := &waiter{target: target, done: req.info.Done}
	var err error
	e.withView(target, func() { err = e.openWaiterFiles(w, req.info) })
	if err != nil {
		for _, bs := range w.bufs {
			if bs.owner == target && !bs.buf.Modified() {
				e.closeBufferState(bs)
			}
		}
		req.reply <- waitReply{err: err}
		return
	}
	e.waiters = append(e.waiters, w)
	e.reclaim(target, explicit)
	req.reply <- waitReply{w: w}
}

// openWaiterFiles は、読み込んでいる client（w.target）のタブとして、info のファイルを開き、最初のファイルのタブへ移る。
// 起動したときの名前の無い空のタブを、開いたファイルのものに使い直さない（使い直すと、そのタブを閉じたときに client の最後のタブになりうる）。
// 他の client のタブになっているファイルは、そのタブのまま待つ（タブとバッファの 1 対 1 を保つ。FR-62）。
func (e *Editor) openWaiterFiles(w *waiter, info WaitInfo) error {
	var first *bufferState
	switch {
	case info.Stdin != nil:
		// req: FR-158
		bs := e.newStdinBuffer(*info.Stdin, info.ReadOnly)
		w.bufs = append(w.bufs, bs)
		first = bs
	case len(info.Files) == 0:
		b := buffer.New("")
		b.SetFixEndOfLine(e.fixEOL)
		bs := e.newBuffer(b, "", "")
		w.bufs = append(w.bufs, bs)
		first = bs
	}
	for _, f := range info.Files {
		bs, err := e.openFile(f, false)
		if err != nil {
			return fmt.Errorf("%s を開けない: %w", f, err)
		}
		if info.ReadOnly {
			bs.buf.SetReadOnly(true)
		}
		if !slices.Contains(w.bufs, bs) {
			w.bufs = append(w.bufs, bs)
		}
		if first == nil {
			first = bs
		}
	}
	if first == nil {
		return nil
	}
	for _, bs := range w.bufs {
		if bs.owner == nil {
			e.addTab(bs)
		}
	}
	if first.owner == e.view {
		e.enter(first)
	} else {
		e.message = infoMessage(fmt.Sprintf("%s は %s のタブです（閉じるまで待ちます）", e.bufferName(first), ownerName(first)))
	}
	return nil
}

// reclaim は、client c が外部のコマンドを実行中なら、c のキーを処理するようにし、端末を渡した実行なら端末を取り戻させる（FR-117）。
// 端末を取り戻させるのは、待つだけの client が c の :! の子孫であること（環境変数 EnvClient が c を指す。explicit）が分かるときだけである。
// :! の外（拡張機能の子プロセスなど）で起動した待つだけの client のために、:! のコマンドが使っている端末を取り上げないためである。
func (e *Editor) reclaim(c *clientView, explicit bool) {
	job := e.shell
	if job == nil || job.client != c || job.reclaimed || job.save != nil || (job.terminal && !explicit) {
		return
	}
	job.reclaimed = true
	if job.terminal {
		if h, ok := e.viewOf(c).term.(TerminalHandover); ok {
			h.ReclaimTerminal(job.id)
		}
	}
	if !e.keys.empty() {
		signal(e.keys.ready) // 止めていた c のキーを処理する
	}
}

// reapWaiters は、ファイルのタブがすべて閉じられた待つだけの client を終わらせる。
// タブを開いた client が終わっていれば、理由を付けて終わらせる（待つだけの client は 0 でない終了コードで終わり、git はコミットをやめる）。
func (e *Editor) reapWaiters() {
	for _, w := range slices.Clone(e.waiters) {
		switch {
		case w.target.detached:
			e.finishWaiter(w, fmt.Sprintf("タブを開いた client %d が終わった", w.target.info.ID))
		case !slices.ContainsFunc(w.bufs, func(bs *bufferState) bool { return slices.Contains(e.bufs, bs) && bs.owner != nil }):
			e.finishWaiter(w, "")
		}
	}
}

// finishWaiter は、待つだけの client w を一覧から外し、終わらせる。
// w のタブを開いた client が端末を取り戻していて、その client の待つだけの client が他にいなければ、端末を子プロセスへ渡し直させ、
// 渡し直したことを受けてから終わらせる（子プロセスが端末を使い始める前に、こちらが端末を手放すため）。
func (e *Editor) finishWaiter(w *waiter, reason string) {
	e.waiters = slices.DeleteFunc(e.waiters, func(o *waiter) bool { return o == w })
	job := e.shell
	if job == nil || job.client != w.target || !job.reclaimed || slices.ContainsFunc(e.waiters, func(o *waiter) bool { return o.target == w.target }) {
		e.endWaiter(w, reason)
		return
	}
	job.reclaimed = false
	if !job.terminal || w.target.detached {
		e.endWaiter(w, reason)
		return
	}
	h, ok := e.viewOf(w.target).term.(TerminalHandover)
	if !ok {
		e.endWaiter(w, reason)
		return
	}
	job.releasing = append(job.releasing, w)
	h.ReleaseTerminal(job.id)
}

// endWaiter は、待つだけの client w の Done を呼ぶ。2 回目からは何もしない。
func (e *Editor) endWaiter(w *waiter, reason string) {
	if w.ended {
		return
	}
	w.ended = true
	if w.done != nil {
		w.done(reason)
	}
}

// waiterGone は、待つだけの client の接続が切れたとき（Wait の cancel）に、一覧から外す。開いたタブは閉じない。
// その client が端末を取り戻させていたなら、他に待つだけの client がいなければ、端末を渡し直させる。
func (e *Editor) waiterGone(w *waiter) {
	if w.ended || !slices.Contains(e.waiters, w) {
		return
	}
	w.ended = true // 接続はもう無いので、Done は呼ばない
	e.finishWaiter(w, "")
}

// permissionError は、保存が権限の不足（EACCES）で失敗したことを表す（FR-79、FR-116）。Error は E212 の文である。
// runActions は、これを受けたら、権限を上げて書くかを問う（askElevate）。
type permissionError struct {
	path string
	// dir は、書く先の親のディレクトリで、無いか、あるか分からない（検索の権限が無い）ときに入る。権限を上げて mkdir -p で作る（FR-116）。
	dir string
	err error
	// buf は、書こうとしたバッファである（:wa では、カーソルのあるバッファと違いうる）。own は、それがバッファのファイルへの保存
	// （保存の流れ）であることを表す。false なら別のファイルへの書き出し（:w 名前）で、成功してもバッファは保存済みにしない。
	buf *bufferState
	own bool
}

// forBuffer は、err が permissionError なら、書こうとしたバッファ bs と、保存の流れか（own）を入れて返す。そうでなければ err を返す。
func forBuffer(err error, bs *bufferState, own bool) error {
	var pe *permissionError
	if errors.As(err, &pe) {
		pe.buf, pe.own = bs, own
	}
	return err
}

func (p *permissionError) Error() string {
	if p.dir != "" {
		return fmt.Sprintf("E212: ディレクトリ %s を作れません: %v", p.dir, p.err)
	}
	return fmt.Sprintf("E212: %s を書けません: %v", p.path, p.err)
}

func (p *permissionError) Unwrap() error { return p.err }

// asPermissionError は、path への書き出しかディレクトリの作成の誤り err が権限の不足なら、permissionError にして返す。そうでなければ nil を返す。
// path の親のディレクトリが無いか分からなければ、権限を上げて作るよう dir に入れる。
// path がシンボリックリンクなら、たどった先のパスにする（通常の書き出しの buffer.Buffer.WriteFile と同じく、書くのは先のファイルである。
// 問いに先のパスを出し、tee にも先のパスを渡して、利用者が権限を上げる対象を見誤らないようにする。PR #234 のレビューの推奨 2）。
func asPermissionError(path string, err error) *permissionError {
	if !errors.Is(err, syscall.EACCES) {
		return nil
	}
	if target, rerr := filepath.EvalSymlinks(path); rerr == nil {
		path = target
	}
	pe := &permissionError{path: path, err: err}
	if _, serr := os.Stat(filepath.Dir(path)); serr != nil {
		pe.dir = filepath.Dir(path)
	}
	return pe
}

// writeBuffer は、b を path へ書き出す（buffer.Buffer.WriteFile）。
// WriteFile は同じディレクトリに一時ファイルを作って置き換えるので、ファイルには書けてもディレクトリに書けないと EACCES になる。
// そのときは、ファイルが書き込める（access(2) の W_OK）普通のファイルなら、その場で切り詰めて書く
// （Vim 9.1 の既定の 'backupcopy' の auto と同じく、権限を上げずに保存する。PR #234 のレビューの推奨 3）。
// その場で書くと、途中で失敗したときに元の内容は残らない。
func writeBuffer(b *buffer.Buffer, path string) error {
	err := b.WriteFile(path)
	if err == nil || !errors.Is(err, syscall.EACCES) {
		return err
	}
	fi, serr := os.Stat(path)
	if serr != nil || !fi.Mode().IsRegular() || unix.Access(path, unix.W_OK) != nil {
		return err
	}
	f, oerr := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
	if oerr != nil {
		return err
	}
	_, werr := b.WriteTo(f)
	if cerr := f.Close(); werr == nil && cerr != nil {
		werr = fmt.Errorf("buffer: %w", cerr)
	}
	return werr
}

// elevateCommand は、権限を上げるコマンド（Config.ElevateCommand、無ければ DefaultElevateCommand）を返す。
func (e *Editor) elevateCommand() []string {
	if len(e.cfg.ElevateCommand) > 0 {
		return e.cfg.ElevateCommand
	}
	return DefaultElevateCommand
}

// askElevate は、:w が権限の不足で書けなかったときに、権限を上げて書くかを問う（FR-79、FR-116。基本設計 4.7）。
// y なら、端末を子プロセスへ渡して <elevateCommand> tee <パス> で書く（startElevatedSave）。終わったら、続きの動作（:wq の :q など）を行う。
// n か <Esc> なら、E212 を出して何もしない。
//
// req: FR-79, FR-116
func (e *Editor) askElevate(bs *bufferState, a ex.Action, rest []ex.Action, pe *permissionError) {
	what := fmt.Sprintf("\"%s\" を書けません", pe.path)
	if pe.dir != "" {
		what = fmt.Sprintf("ディレクトリ \"%s\" を作れません", pe.dir)
	}
	// パスとコマンドが長くても問いの部分が画面の幅で切れないよう、問いを先に置く（PR #234 のレビューの推奨 4）。
	e.prompt = &prompt{
		text: fmt.Sprintf("権限を上げて書き込みますか (y/n)? 権限が足りないので%s（%s で書きます）", what, strings.Join(e.elevateCommand(), " ")),
		answer: func(k editcmd.Key) bool {
			switch k {
			case editcmd.Rune('y'):
				e.startElevatedSave(bs, a, rest, pe)
			case editcmd.Rune('n'), editcmd.Esc:
				e.showError(pe)
			default:
				return false
			}
			return true
		},
	}
}

// elevatedSave は、権限を上げた保存の、終わった後の処理に要るものである。
type elevatedSave struct {
	path string
	// own は、バッファのファイルへの保存（保存の流れ）であることを表す。false なら別のファイルへの書き出し（:w 名前）で、バッファは保存済みにしない。
	own bool
	// made は、権限を上げて作ったディレクトリである。
	made string
	// version は、書き出したときのバッファのバージョン番号である。終わったときに変わっていれば、保存済みにしない。
	version uint64
	// action と rest は、書けなかった動作と、その後の動作である。from は、それらを行うバッファ（コマンドを打ったときのカーソルのあるバッファ）である。
	action ex.Action
	rest   []ex.Action
	from   *bufferState
}

// startElevatedSave は、端末を子プロセスへ渡し、<elevateCommand> tee <パス> にバッファの中身を標準入力で渡して書く（FR-79。基本設計 4.7）。
// 親のディレクトリが無ければ、先に <elevateCommand> mkdir -p <ディレクトリ> を動かす（FR-116）。
// パスワードは elevateCommand が端末から直接読み、本体は読まない。tee の標準出力は捨てる。
// 終わったら elevatedDone が、外での変更の確認の記録を取り直し、buffer/didSave を送る。
//
// req: FR-79, FR-116
func (e *Editor) startElevatedSave(bs *bufferState, a ex.Action, rest []ex.Action, pe *permissionError) {
	target := pe.buf
	if target == nil || !slices.Contains(e.bufs, target) {
		target = bs
	}
	if e.shell != nil {
		e.showError(errShellBusy)
		return
	}
	h, ok := e.handover()
	if !ok {
		e.showError(fmt.Errorf("%w（この client の端末は子プロセスへ渡せないので、権限を上げて書けません）", pe))
		return
	}
	var data bytes.Buffer
	if _, err := target.buf.WriteTo(&data); err != nil {
		e.showError(err)
		return
	}
	elev := e.elevateCommand()
	argv := func(args ...string) []string { return append(slices.Clone(elev), args...) }
	var cmds []TerminalCommand
	if pe.dir != "" {
		cmds = append(cmds, TerminalCommand{Argv: argv("mkdir", "-p", pe.dir)})
	}
	// パスは絶対パス（"/" で始まる）なので、"-" で始まってオプションと取り違えられることは無い。
	cmds = append(cmds, TerminalCommand{Argv: argv("tee", pe.path), Input: data.Bytes(), PipeInput: true, DiscardOutput: true})
	e.termSeq++
	job := &shellJob{
		bs: target, client: e.op, cancel: func() {}, terminal: true, id: e.termSeq,
		save: &elevatedSave{path: pe.path, own: pe.own, made: pe.dir, version: target.buf.Version(), action: a, rest: rest, from: bs},
	}
	e.shell = job
	h.RunInTerminal(TerminalJob{ID: job.id, Commands: cmds, Env: e.terminalEnv(e.op)})
}

// elevatedDone は、権限を上げた保存の結果を扱う。成功なら、バッファのファイルへの保存では、保存済みにし、外での変更の確認に使う
// ファイルの状態を取り直し（取り直さないと、次の :w で外で変更されたと問われる。PR #178 のレビューの推奨 4）、buffer/didSave を送る。
// その後、続きの動作（:wq の :q など）を行う。失敗なら E212 を出し、バッファは未保存のまま残す。
//
// req: FR-79, FR-116
func (e *Editor) elevatedDone(job *shellJob, r TerminalResult) {
	s, bs := job.save, job.bs
	if !slices.Contains(e.bufs, bs) || job.client.detached {
		return
	}
	e.useClient(job.client)
	elev := strings.Join(e.elevateCommand(), " ")
	switch {
	case r.Err != "":
		e.showError(fmt.Errorf("E212: %s を書けません: %s", s.path, r.Err))
		return
	case r.Status != 0:
		e.showError(fmt.Errorf("E212: %s を書けません: %s が %d を返しました", s.path, elev, r.Status))
		return
	}
	if s.own {
		if bs.buf.Version() == s.version {
			bs.buf.MarkSaved()
		}
		if st, err := statFile(s.path); err == nil {
			bs.setDisk(st)
		}
		if err := e.host.DidSave(ext.BufferDidSaveParams{Buffer: bs.id, Version: ext.Version(bs.buf.Version()), Path: s.path}); err != nil {
			e.log.Error("buffer/didSave を送れない", "err", err)
		}
	}
	e.message = infoMessage(fmt.Sprintf("%s（%s で書き込みました）", writtenText(s.path, s.made), elev))
	next := s.rest
	if s.action.Kind == ex.ActionWriteAll {
		// :wa は、残りの未保存のバッファも書き出す（書き出し済みのバッファは変更が無いので書かない）。
		next = append([]ex.Action{s.action}, s.rest...)
	}
	if len(next) > 0 && slices.Contains(e.bufs, s.from) {
		e.runActions(s.from, next)
	}
}
