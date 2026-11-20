package main

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"syscall"

	"github.com/piplupflipper/vi-imitation/internal/protocol"
)

// stdinArg は、起動の引数で、標準入力から読んだ中身を開くことを表す名前である（FR-158。Vim の -）。
// 名前が - のファイルは、./- と書けば開ける（Vim と同じ）。
const stdinArg = "-"

// ttyPath は、起動の引数の - で標準入力を読んだ後に、キーを読む端末である（FR-158）。
const ttyPath = "/dev/tty"

// stdinMessage は、標準入力を読む間に出す行である（Vim 9.1 の "Vim: Reading from stdin..." に当たる）。
const stdinMessage = "vi-imitation: 標準入力から読んでいます..."

// hasStdinArg は、起動の引数のファイル files に stdinArg があるかを返す。
// Vim 9.1 と同じく、stdinArg はほかのファイルと一緒に書けない（2 つ書くのも誤り）。
// そのときは Vim の "Too many edit arguments" と同じく、2 つ目の引数を示す誤りを返す（2026-11-20 に Vim 9.1 で、vim - a.txt、vim a.txt -、vim - - が
// どれも Too many edit arguments で終了コード 1 になることを確かめた）。
func hasStdinArg(files []string) (bool, error) {
	if !slices.Contains(files, stdinArg) {
		return false, nil
	}
	if len(files) > 1 {
		return false, fmt.Errorf("編集の引数が多すぎる: %q（- はほかのファイルと一緒に書けない）", files[1])
	}
	return true, nil
}

// readStdin は、r を終わり（EOF）まで読み、中身を返す（FR-158）。読み始める前に msg へ stdinMessage を 1 行出す。
// Vim 9.1 と同じく、読み終えるまで画面を開かない。
//
// 読む間に SIGINT（端末で打った <C-c>）を受けたら、Vim 9.1 と同じく、それまでに読んだ中身を捨て、
// 終わりまで読み続けてから（読んだものは捨てる）空の中身を返す。呼び出し側は、空の名前の無いバッファを開く。
// 端末の <C-c> は前面のプロセスグループ全体に届くので、ふつうは書く側（tail -f など）も終わり、そこで読み終わる
// （2026-11-20 に Vim 9.1 で、SIGINT を無視する書く側でも、<C-c> の後に書く側が閉じるまで待ち、空のバッファで開くことを確かめた）。
// 読む間のほかのシグナル（SIGTERM、SIGHUP）は、既定の動作で終わる（端末をまだ変えていないので、戻すものが無い）。
//
// 読んだものは stdinBlockSize ごとの塊に溜め、読み終えたら中身の大きさちょうどの文字列へ写す（stdinSink.text）。
// 1 つの領域を倍々に広げて溜めると、広げるたびに前の領域が残り、最後に余った領域も返す文字列ごと残る
// （中身の行は、この文字列の一部を指したままバッファに入る）。塊に溜めれば、読む間の最大は中身のおよそ 2 倍で、読んだ後は中身の大きさになる。
//
// req: FR-158
func readStdin(r io.Reader, msg io.Writer) (string, error) {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT)
	defer signal.Stop(sigs)
	return readStdinUntil(r, msg, sigs)
}

// readStdinUntil は readStdin の本体で、intr に届いていたら SIGINT を受けたものとして扱う（テストはシグナルを使わずに送る）。
// intr は、読んだ塊を溜めるたびと、読み終えたときに見る。
func readStdinUntil(r io.Reader, msg io.Writer, intr <-chan os.Signal) (string, error) {
	_, _ = fmt.Fprintln(msg, stdinMessage) // 出せなくても読む
	sink := &stdinSink{intr: intr}
	if _, err := io.Copy(sink, r); err != nil {
		return "", fmt.Errorf("標準入力を読めない: %w", err)
	}
	text := sink.text()
	// 写し終えた塊（中身と同じ大きさ）を、ここで回収しておく。回収しないと、次の GC の目安が塊と文字列の両方を数えた大きさになり、
	// server へ送る間の一時的な領域（塊ごとの JSON）が回収されずに溜まる
	// （2026-11-20 に 900 MB の入力を別のプロセスの server へ送ったとき、client の最大の RSS が入力の約 4.1 倍になった）。
	runtime.GC()
	return text, nil
}

// releaseMemory は、server へ送り終えた標準入力の中身の領域を、すぐに OS へ返す（FR-158）。
// 送り終えた client は画面を中継するだけで、中身はもう要らない。返さないと、Go のランタイムが領域を返すまで（PR #304 のレビューで、
// 100 MiB の入力で約 4 分）入力の約 2 倍の RSS を持ち続ける。中身への参照を切ってから呼ぶ。
func releaseMemory() {
	debug.FreeOSMemory()
}

// serverAlive は、path の server のソケットに繋げるかを返す。繋げたら、何も送らずに閉じる。
// 起動の引数の - で標準入力を読む前に、VI_IMITATION_SOCKET の server がまだいるか（待つだけの client として動けるか）を確かめるのに使う。
// server の側は、Hello を送らずに閉じた接続として断る（記録に 1 行残る）。
func serverAlive(path string) bool {
	conn, err := protocol.Dial(path)
	if err != nil {
		return false
	}
	_ = conn.Close() // 確かめるためだけの接続。閉じる誤りは扱えない
	return true
}

// stdinBlockSize は、readStdin が読んだものを溜める塊 1 つの大きさである（仮）。
const stdinBlockSize = 1 << 20

// stdinSink は、readStdin が読んだ中身を stdinBlockSize ごとの塊に溜める。intr に届いた後は、溜めた中身を手放し、後の塊も捨てる。
// 読む goroutine だけが触る。
type stdinSink struct {
	intr        <-chan os.Signal
	interrupted bool
	blocks      [][]byte
	size        int
}

// stopped は、intr に届いたかを返す。
func (s *stdinSink) stopped() bool {
	if !s.interrupted {
		select {
		case <-s.intr:
			s.interrupted = true
			s.blocks, s.size = nil, 0 // 溜めた中身を手放す
		default:
		}
	}
	return s.interrupted
}

func (s *stdinSink) Write(p []byte) (int, error) {
	if s.stopped() {
		return len(p), nil
	}
	n := len(p)
	for len(p) > 0 {
		if len(s.blocks) == 0 || len(s.blocks[len(s.blocks)-1]) == stdinBlockSize {
			s.blocks = append(s.blocks, make([]byte, 0, stdinBlockSize))
		}
		last := &s.blocks[len(s.blocks)-1]
		k := min(len(p), stdinBlockSize-len(*last))
		*last = append(*last, p[:k]...)
		p = p[k:]
	}
	s.size += n
	return n, nil
}

// text は、溜めた中身を、大きさちょうどの文字列にして返す。写した塊から手放す。intr に届いていたら空を返す。
func (s *stdinSink) text() string {
	if s.stopped() {
		return ""
	}
	var b strings.Builder
	b.Grow(s.size)
	for i, blk := range s.blocks {
		b.Write(blk)
		s.blocks[i] = nil
	}
	s.blocks = nil
	return b.String()
}
