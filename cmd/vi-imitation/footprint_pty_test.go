package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/piplupflipper/vi-imitation/internal/term/ptytest"
)

// envBuildDir は、`make build` が実行ファイルを置いたディレクトリを渡す環境変数の名前である（internal/distcheck と同じ）。
// 起動時間とメモリの検査は、テストの実行ファイル（-race と -cover の計測を含む）ではなく、`make build` の本体を測る。
const envBuildDir = "VI_IMITATION_BUILD_DIR"

// NFR-05 の目標値（要件定義 5 章）と、検査の条件。
// 条件は docs/measurements/nfr05-footprint.md の道具（scripts/nfr05-footprint.py）に合わせる。
const (
	footprintStartupLimit = 20 * time.Millisecond // 起動から、文字を含む最初の画面を描き終えるまで
	footprintRSSLimitKiB  = 16 * 1024             // 本体の VmRSS（16 MiB）
	footprintLines        = 10_000                // 開くテキストファイルの行数
	footprintWarmup       = 3                     // 判定に使わない最初の回数（ページキャッシュと、実行ファイルの読み込み）
	footprintRuns         = 20                    // 中央値を取る回数
	footprintSettle       = 500 * time.Millisecond
	footprintTimeout      = 10 * time.Second
)

// footprintEscapes は、端末への出力のエスケープシーケンス（CSI、OSC、DCS と APC と PM、その他の 2 バイトの列）である。
var footprintEscapes = regexp.MustCompile("\x1b\\[[0-?]*[ -/]*[@-~]" +
	"|\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)" +
	"|\x1b[P_^][^\x1b]*\x1b\\\\" +
	"|\x1b[ -/]*[0-~]")

// hasText は、data がエスケープシーケンスを除いて、空白でない文字を含むかを返す。
func hasText(data []byte) bool {
	return slices.ContainsFunc(footprintEscapes.ReplaceAll(data, nil), func(b byte) bool { return b > 0x20 && b != 0x7f })
}

// footprintSample は、1 回の起動で測った値である。
type footprintSample struct {
	startup time.Duration // 起動から、出力が途切れるまでの、文字を含む最後の read まで
	rssKiB  int           // 最初の画面の後の本体の VmRSS
}

// TestFootprintInPTY は、拡張機能なしで 10,000 行のテキストファイルを開いたときの、
// 起動から最初の画面までの時間と本体の RSS が、NFR-05 の上限に収まることを確かめる。
// 共用のマシンでの揺れに耐えるよう、最初の数回を捨て、20 回の中央値で判定する。
// `make footprint-check` が、`make build` の本体のディレクトリを環境変数で渡して実行する。指定が無ければ飛ばす。
//
// req: NFR-05
func TestFootprintInPTY(t *testing.T) {
	dir := os.Getenv(envBuildDir)
	if dir == "" {
		t.Skipf("%s が指定されていない（make footprint-check で実行する）", envBuildDir)
	}
	bin := filepath.Join(dir, "vi-imitation")
	if _, err := os.Stat(bin); err != nil { //nolint:gosec // make footprint-check が渡す bin/ の実行ファイルを確かめる
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "lines10000.txt")
	var b strings.Builder
	for i := 1; i <= footprintLines; i++ {
		fmt.Fprintf(&b, "line %06d: the quick brown fox jumps over the lazy dog 0123456789\n", i)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	var startups, rsses []float64
	for i := range footprintWarmup + footprintRuns {
		s := measureFootprint(t, bin, path)
		if i < footprintWarmup {
			continue
		}
		startups = append(startups, float64(s.startup.Microseconds())/1000)
		rsses = append(rsses, float64(s.rssKiB))
	}
	startup, rss := median(startups), median(rsses)
	t.Logf("起動から最初の画面まで: 中央値 %.1f ms（最小 %.1f、最大 %.1f）、上限 %v",
		startup, slices.Min(startups), slices.Max(startups), footprintStartupLimit)
	t.Logf("本体の VmRSS: 中央値 %.0f KiB（最小 %.0f、最大 %.0f）、上限 %d KiB",
		rss, slices.Min(rsses), slices.Max(rsses), footprintRSSLimitKiB)
	if limit := float64(footprintStartupLimit.Microseconds()) / 1000; startup > limit {
		t.Errorf("起動から最初の画面までの中央値 %.1f ms が上限 %.1f ms を超えた（%d 回: %v）", startup, limit, footprintRuns, startups)
	}
	if rss > footprintRSSLimitKiB {
		t.Errorf("本体の VmRSS の中央値 %.0f KiB が上限 %d KiB を超えた（%d 回: %v）", rss, footprintRSSLimitKiB, footprintRuns, rsses)
	}
}

// median は、v の中央値を返す。偶数個なら中央の 2 つの平均である。
func median(v []float64) float64 {
	s := slices.Sorted(slices.Values(v))
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// ptyRead は、偽の端末から 1 回の read で読んだ出力と、読んだ時刻である。
type ptyRead struct {
	at   time.Time
	data []byte
}

// measureFootprint は、bin で path を 80 列 24 行の偽の端末の上に開き、1 回分を測ってから :q! で終わらせる。
// 出力が footprintSettle の間途切れたら、最初の画面を描き終えたとみなす。
func measureFootprint(t *testing.T, bin, path string) footprintSample {
	t.Helper()
	tmp := t.TempDir()
	master, tty := ptytest.Open(t, 80, 24)
	cmd := exec.Command(bin, path)                        //nolint:gosec // make build の本体をエディタとして起動する
	cmd.Env = append(muxFreeEnv(), "TERM=xterm-256color") // 1 つのプロセスの構成（FR-60）を測る
	for _, name := range []string{"CONFIG", "DATA", "STATE", "CACHE"} {
		d := filepath.Join(tmp, strings.ToLower(name))
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
		cmd.Env = append(cmd.Env, "XDG_"+name+"_HOME="+d)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}

	reads := make(chan ptyRead, 256)
	go func() {
		defer close(reads)
		buf := make([]byte, 65536)
		for {
			n, err := master.Read(buf)
			if n > 0 {
				reads <- ptyRead{at: time.Now(), data: bytes.Clone(buf[:n])}
			}
			if err != nil {
				return
			}
		}
	}()

	start := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill() // 終わらなかったときの後始末
			if err := <-done; err != nil {
				t.Logf("後始末で止めたエディタ: %v", err)
			}
		}
	}()

	var lastText time.Time
	var out bytes.Buffer
	settle := time.NewTimer(footprintTimeout)
	deadline := time.After(footprintTimeout)
wait:
	for {
		select {
		case r, ok := <-reads:
			if !ok {
				t.Fatalf("最初の画面の前に端末の出力が閉じた。出力: %q", out.String())
			}
			out.Write(r.data)
			if hasText(r.data) {
				lastText = r.at
			}
			settle.Reset(footprintSettle)
		case <-settle.C:
			break wait
		case <-deadline:
			t.Fatalf("%v の間、出力が途切れない。出力: %q", footprintTimeout, out.String())
		}
	}
	if lastText.IsZero() {
		t.Fatalf("文字を含む出力が無い。出力: %q", out.String())
	}
	s := footprintSample{startup: lastText.Sub(start), rssKiB: vmRSSKiB(t, cmd.Process.Pid)}

	// 終わるまで出力を読み続ける（読まないと、エディタが書き込みで止まりうる）。
	go func() {
		for range reads {
			// 読み捨てる
		}
	}()
	if _, err := master.WriteString("\x1b:q!\r"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("エディタの終了 = %v", err)
		}
	case <-time.After(footprintTimeout):
		t.Fatal(":q! でエディタが終わらない")
	}
	return s
}

// vmRSSKiB は、/proc/<pid>/status の VmRSS を KiB で返す。
func vmRSSKiB(t *testing.T, pid int) int {
	t.Helper()
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.Lines(string(b)) {
		if v, ok := strings.CutPrefix(line, "VmRSS:"); ok {
			n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(v), " kB"))
			if err != nil {
				t.Fatalf("VmRSS を読めない: %q", line)
			}
			return n
		}
	}
	t.Fatalf("/proc/%d/status に VmRSS が無い", pid)
	return 0
}
