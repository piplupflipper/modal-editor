package main

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
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
// 中身は、読んだ大きさに対して取っておいた領域が 1.25 倍を超えて余れば、ちょうどの大きさへ写し直す。
// 中身の行は、この文字列の一部を指したままバッファに入るので、余った領域も編集の間ずっと残るからである。
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
	if f, ok := r.(*os.File); ok {
		// ファイルをつないだとき（vi-imitation - < a.txt）は、大きさが分かるので先に取っておく。
		if st, err := f.Stat(); err == nil && st.Mode().IsRegular() {
			sink.b.Grow(int(st.Size()))
		}
	}
	if _, err := io.Copy(sink, r); err != nil {
		return "", fmt.Errorf("標準入力を読めない: %w", err)
	}
	return sink.text(), nil
}

// stdinSink は、readStdin が読んだ中身を溜める。intr に届いた後は、溜めた中身を手放し、後の塊も捨てる。
// 読む goroutine だけが触る。
type stdinSink struct {
	intr        <-chan os.Signal
	interrupted bool
	b           strings.Builder
}

// stopped は、intr に届いたかを返す。
func (s *stdinSink) stopped() bool {
	if !s.interrupted {
		select {
		case <-s.intr:
			s.interrupted = true
			s.b.Reset() // 溜めた中身を手放す
		default:
		}
	}
	return s.interrupted
}

func (s *stdinSink) Write(p []byte) (int, error) {
	if s.stopped() {
		return len(p), nil
	}
	return s.b.Write(p)
}

// text は、溜めた中身を返す。intr に届いていたら空を返す。
func (s *stdinSink) text() string {
	if s.stopped() {
		return ""
	}
	t := s.b.String()
	if s.b.Cap() > len(t)+len(t)/4 {
		t = strings.Clone(t)
	}
	return t
}
