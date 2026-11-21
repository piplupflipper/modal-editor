package editor

import (
	"context"
	"unicode/utf8"
)

// 補完の候補の選び方と、候補をイベントループの外で集める仕事（基本設計 7.4。FR-198、FR-199）。
//
// コマンドラインの補完（cmdcomplete.go）が使う。後に作るインサートモードの補完（6.3 の未決）と、拡張機能に引数を補わせる口も、
// 候補をどこへ入れるか（コマンドラインの語の置き換えか、バッファの編集か）と、一覧をどこに置くかだけを変えて、
// ここの選び方（completionChoice）と集める仕事（completionJob）をそのまま使う想定である。

// completionChoice は、候補の列と、選んでいる候補である（Neovim の expand_T の xp_files、xp_orig、xp_selected）。
// 選び方は Neovim 0.12.5 の get_next_or_prev_match と同じで、最後の候補の次と最初の候補の前は、補い始めたときの語（orig）に戻る。
type completionChoice struct {
	// items は、候補を入れる形で並べたものである。shown は、一覧に出す形である。
	items, shown []string
	// orig は、補い始めたときの語である。
	orig string
	// selected は、入れている候補の添字である。-1 なら orig を入れている。
	selected int
}

// text は、i 番目の候補（-1 なら補い始めたときの語）を返す。
func (c *completionChoice) text(i int) string {
	if i < 0 {
		return c.orig
	}
	return c.items[i]
}

// step は、選んでいる候補から delta が正なら次へ、負なら前へ動いた先の添字を返す（-1 は補い始めたときの語）。
// 補い始めたときの語の前は、最後の候補である。
//
// req: FR-198
func (c *completionChoice) step(delta int) int {
	n := len(c.items)
	i := c.selected
	if delta < 0 {
		if i == -1 {
			i = n
		}
		i--
	} else {
		i++
	}
	if i < 0 || i >= n {
		return -1
	}
	return i
}

// page は、一覧の高さ height（2 行を残す）だけ先（down なら）か前へ動いた先の添字を返す（Neovim の WILD_PAGEDOWN と WILD_PAGEUP）。
// 最後の候補で先へ、最初の候補で前へ送ると補い始めたときの語に戻り、その語からは最初か最後の候補へ移る。
//
// req: FR-198
func (c *completionChoice) page(down bool, height int) int {
	n := len(c.items)
	if height > 3 {
		height -= 2
	}
	height = max(height, 1)
	i := c.selected
	switch {
	case down && i == n-1, !down && i == 0:
		return -1
	case down && i < 0:
		return 0
	case !down && i < 0:
		return n - 1
	case down:
		return min(i+height, n-1)
	}
	return max(i-height, 0)
}

// longestCommonPrefix は、items の共通の先頭を返す。文字の単位で比べ、大文字と小文字を区別する（Neovim の find_longest_match）。
func longestCommonPrefix(items []string) string {
	if len(items) == 0 {
		return ""
	}
	p := items[0]
	for _, it := range items[1:] {
		n := 0
		for n < len(p) && n < len(it) {
			r1, s1 := utf8.DecodeRuneInString(p[n:])
			r2, _ := utf8.DecodeRuneInString(it[n:])
			if r1 != r2 {
				break
			}
			n += s1
		}
		p = p[:n]
	}
	return p
}

// completionJob は、イベントループの外で補完の候補を集めている仕事である（client ごとに 1 つ。clientView.completionJob）。
//
// 集めている間は、その client のキーと貼り付けを列に残し（keyAllowed）、キーマップの展開の残りを heldInput に止める（suspension）。
// 外部のコマンドと再生中の拡張機能の結果を待つときと同じ形である。集め終えて補った後に、止めた入力を順に続ける。
// このため、<Tab> と同じ読み込みで届いた <CR> や、キーマップの展開の中の <Tab><CR> も、補った後のコマンドラインで働く（FR-199）。
// 集めている間も、イベントループは止まらず、ほかの client のキー、拡張機能のイベント、描画は進む。
// <C-c> で集めるのを打ち切れ（interruptCompletion）、集める関数が返るのを待たずに、補う前の語のまま、<C-c> までに打ったキーを捨てて
// その client のキーの処理に戻る。
type completionJob struct {
	heldInput
	cancel context.CancelFunc
	// done は、集めた候補（取り消したか集められなければ err）を受け取って補う。集め終えた client を読み込んでから、
	// イベントループの goroutine で呼ぶ。
	done func(items, shown []string, err error)
}

// startCompletionJob は、読み込んでいる client に、collect をイベントループの外の goroutine で動かす仕事を始める。
// collect が終わったら、イベントループへ戻して done を呼び、止めていた入力を続ける（completionJobDone）。
// 拡張機能に引数を補わせる口を足すときは、collect で問い合わせの応答を待つ。
//
// req: FR-199
func (e *Editor) startCompletionJob(collect func(ctx context.Context) (items, shown []string, err error), done func(items, shown []string, err error)) *completionJob {
	ctx, cancel := context.WithCancel(context.Background())
	c, stop := e.view, e.stop
	job := &completionJob{cancel: cancel, done: done}
	c.completionJob = job
	go func() {
		defer e.recoverPanic()
		defer cancel()
		items, shown, err := collect(ctx)
		select {
		case e.inspect <- func() { e.completionJobDone(c, job, items, shown, err) }:
		case <-stop:
		}
	}()
	return job
}

// completionJobDone は、client c の仕事 job が集めた候補を受け取る。イベントループの goroutine で呼ぶ。
// 仕事をもう終えていれば（<C-c> で打ち切った。interruptCompletion）、届いた候補を捨てる。
// 集めている間に <C-c> を打っていれば（印を見る前に候補が届いた）、打ち切ったときと同じに終える。
// そうでなければ、列に残していた c のキーを処理できるようにし、補ってから、止めていた入力を続ける。
//
// req: FR-199
func (e *Editor) completionJobDone(c *clientView, job *completionJob, items, shown []string, err error) {
	if c.completionJob != job {
		return
	}
	if e.keys.interrupted(c) {
		e.abortCompletion(c, job)
		return
	}
	c.completionJob = nil
	if !e.keys.empty() {
		signal(e.keys.ready)
	}
	if c.detached {
		return
	}
	e.useClient(c)
	job.done(items, shown, err)
	e.resumeInput(job.heldInput)
}

// interruptCompletion は、<C-c> を受けた client の、候補を集めている仕事を打ち切る（abortCompletion）。
// 集める関数が返るのを待たない。止まったファイルシステム（応答の無い NFS、切れた sshfs）の読み込みは、取り消しても返らないためである。
// 集める goroutine は、後で返ったら結果を捨てる（completionJobDone）。
//
// req: FR-199
func (e *Editor) interruptCompletion() {
	for _, c := range e.clients {
		if j := c.completionJob; j != nil && e.keys.interrupted(c) {
			e.abortCompletion(c, j)
		}
	}
}

// abortCompletion は、client c の仕事 job を、補わずに終える。集める関数を取り消し、<C-c> までに打った c のキーを捨て、
// 止めていたキーマップの展開の残りも捨てて、列に残した c のキーを処理できるようにする
// （Neovim 0.12.5 の補完の途中の <C-c> と同じく、コマンドラインは抜けず、補う前の語のまま残る）。
func (e *Editor) abortCompletion(c *clientView, job *completionJob) {
	job.cancel()
	c.completionJob = nil
	e.interrupt.Store(false)
	c.intr.Store(false)
	e.keys.dropThrough(interruptKey, c)
	if !e.keys.empty() {
		signal(e.keys.ready)
	}
	if c.detached {
		return
	}
	e.useClient(c)
	job.done(nil, nil, context.Canceled)
	e.remap.reset()
}
