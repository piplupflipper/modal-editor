package exthost

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/internal/buffer"
)

// 段階 1 の検証（要件定義 8 章）として、標準の拡張機能 modal-editor-ext-line への textObject/resolve の往復時間を測る。
// 手順と結果は docs/measurements/stage1-rpc-latency.md にある。
//
// 計測は時間がかかり、go のツールチェーンで拡張機能をビルドするので、環境変数 latencyEnv が 1 のときだけ動く。
// make test（-race つき）では読み飛ばす。計測は -race を付けずに動かす。
//
//	MODAL_EDITOR_LATENCY=1 go test -run TestStage1Latency -count=1 -v ./internal/exthost
const latencyEnv = "MODAL_EDITOR_LATENCY"

// 計測の回数。
const (
	latencyStartups = 100   // 起動から最初の応答までを測る回数
	latencyWarmup   = 1000  // 起動済みの往復で、測る前に捨てる回数
	latencyQueries  = 10000 // 起動済みの往復を測る回数
	latencyEdits    = 5000  // 大きなバッファで、編集と往復の組を測る回数
)

// latencyQueryTimeout は、計測の問い合わせの期限である。計測では期限を超えないことを前提にし、超えたら失敗にする。
const latencyQueryTimeout = 10 * time.Second

// latencyLine は、大きなバッファの 1 行である（40 バイト）。
const latencyLine = "\tvalue := compute(left, right) // note"

func TestStage1Latency(t *testing.T) {
	if os.Getenv(latencyEnv) != "1" {
		t.Skipf("set %s=1 to measure the extension round trip", latencyEnv)
	}
	dir := buildLineExtension(t)
	spec := lineSpec(t, dir)

	t.Logf("GOMAXPROCS=%d GOOS=%s GOARCH=%s %s", runtime.GOMAXPROCS(0), runtime.GOOS, runtime.GOARCH, runtime.Version())
	if load, err := os.ReadFile("/proc/loadavg"); err == nil {
		t.Logf("loadavg before: %s", strings.TrimSpace(string(load)))
	}

	t.Run("startup", func(t *testing.T) {
		d := make([]time.Duration, 0, latencyStartups)
		for range latencyStartups {
			d = append(d, measureStartup(t, spec))
		}
		logStats(t, "起動から最初の応答まで", d)
	})
	t.Run("warm", func(t *testing.T) {
		h := newLatencyHost(t, spec)
		openText(t, h, 1, strings.Repeat(latencyLine+"\n", 99)+latencyLine)
		p := ext.TextObjectResolveParams{Buffer: 1, Version: 1, ID: "line.inner", Cursor: ext.Position{Line: 50}}
		for range latencyWarmup {
			resolveOK(t, h, p)
		}
		d := make([]time.Duration, 0, latencyQueries)
		for range latencyQueries {
			start := time.Now()
			resolveOK(t, h, p)
			d = append(d, time.Since(start))
		}
		logStats(t, "起動済みの往復（100 行）", d)
	})
	for _, lines := range []int{10_000, 100_000} {
		t.Run(fmt.Sprintf("edit-%d", lines), func(t *testing.T) {
			measureEdits(t, spec, lines)
		})
	}
	if load, err := os.ReadFile("/proc/loadavg"); err == nil {
		t.Logf("loadavg after: %s", strings.TrimSpace(string(load)))
	}
}

// buildLineExtension は、cmd/modal-editor-ext-line をビルドし、宣言ファイルと一緒に一時ディレクトリへ置く。
func buildLineExtension(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join("..", "..", "cmd", "modal-editor-ext-line")
	//nolint:gosec // 引数は定数と、テストが作った一時ディレクトリのパスだけである
	cmd := exec.Command("go", "build", "-trimpath", "-o", filepath.Join(dir, "modal-editor-ext-line"), "./"+filepath.ToSlash(src))
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build extension: %v\n%s", err, out)
	}
	manifest, err := os.ReadFile(filepath.Join(src, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // dir はテストが作った一時ディレクトリである
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// lineSpec は、dir の宣言ファイルを読んで検査し、Spec にする。
func lineSpec(t *testing.T, dir string) Spec {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m ext.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	return Spec{Manifest: m, Dir: dir}
}

func newLatencyHost(t *testing.T, spec Spec) *Host {
	t.Helper()
	h, err := New([]Spec{spec}, Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h
}

func openText(t *testing.T, h *Host, version uint64, text string) {
	t.Helper()
	if err := h.OpenBuffer(BufferSnapshot{ID: 1, Version: version, Text: text}); err != nil {
		t.Fatal(err)
	}
}

func resolveOK(t *testing.T, h *Host, p ext.TextObjectResolveParams) {
	t.Helper()
	res, err := h.ResolveTextObject(context.Background(), "line", p, latencyQueryTimeout)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Version != p.Version {
		t.Fatalf("result = %+v, want OK at version %d", res, p.Version)
	}
}

// measureStartup は、未起動の拡張機能へ問い合わせ、子プロセスの起動、initialize、buffer/open を経て応答が届くまでの時間を返す。
func measureStartup(t *testing.T, spec Spec) time.Duration {
	t.Helper()
	h, err := New([]Spec{spec}, Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = h.Close() }()
	openText(t, h, 1, latencyLine)
	start := time.Now()
	resolveOK(t, h, ext.TextObjectResolveParams{Buffer: 1, Version: 1, ID: "line.inner"})
	return time.Since(start)
}

// measureEdits は、lines 行のバッファで、1 回の編集の差分の送信（ChangeBuffer）と、その直後の往復を合わせた時間を測る。
// 編集は 2 種類を測る。行の中の 1 文字の挿入と削除を交互に行うもの（行の数が変わらない）と、
// 1 行の挿入と削除を交互に行うもの（行の数が変わり、本体の写しと拡張機能の複製で行の配列の後ろ半分を動かす）である。
func measureEdits(t *testing.T, spec Spec, lines int) {
	t.Helper()
	h := newLatencyHost(t, spec)
	text := strings.Repeat(latencyLine+"\n", lines-1) + latencyLine
	mid := lines / 2

	start := time.Now()
	openText(t, h, 1, text)
	resolveOK(t, h, ext.TextObjectResolveParams{Buffer: 1, Version: 1, ID: "line.inner", Cursor: ext.Position{Line: mid}})
	t.Logf("%d 行: 起動、全文の送信、最初の応答まで %s（1 回だけの値）", lines, time.Since(start))

	version := uint64(1)
	at := func(line, b int) buffer.Position { return buffer.Position{Line: line, Byte: b} }
	kinds := []struct {
		name   string
		insert buffer.Edit
		remove buffer.Edit
	}{
		{"行の中の編集", buffer.Edit{Range: buffer.Range{Start: at(mid, 1), End: at(mid, 1)}, Text: "x"}, buffer.Edit{Range: buffer.Range{Start: at(mid, 1), End: at(mid, 2)}}},
		{"行を足す編集", buffer.Edit{Range: buffer.Range{Start: at(mid, 0), End: at(mid, 0)}, Text: latencyLine + "\n"}, buffer.Edit{Range: buffer.Range{Start: at(mid, 0), End: at(mid+1, 0)}}},
	}
	for _, k := range kinds {
		d := make([]time.Duration, 0, latencyEdits)
		for i := range latencyEdits + latencyWarmup/10 {
			e := k.insert
			if i%2 == 1 {
				e = k.remove
			}
			start := time.Now()
			if err := h.ChangeBuffer(1, buffer.Change{Before: version, After: version + 1, Edits: []buffer.Edit{e}}); err != nil {
				t.Fatal(err)
			}
			version++
			resolveOK(t, h, ext.TextObjectResolveParams{Buffer: 1, Version: ext.Version(version), ID: "line.inner", Cursor: ext.Position{Line: mid}})
			if i >= latencyWarmup/10 {
				d = append(d, time.Since(start))
			}
		}
		logStats(t, fmt.Sprintf("%d 行、%s、差分の送信と往復", lines, k.name), d)
	}
}

// logStats は、d の p50、p99、最大を記録する。p は最も近い順位の方式（nearest-rank）で求める。
func logStats(t *testing.T, name string, d []time.Duration) {
	t.Helper()
	slices.Sort(d)
	rank := func(p int) time.Duration {
		i := (len(d)*p + 99) / 100
		return d[max(i, 1)-1]
	}
	t.Logf("RESULT | %s | n=%d | p50 %s | p99 %s | max %s |", name, len(d), rank(50), rank(99), d[len(d)-1])
}
