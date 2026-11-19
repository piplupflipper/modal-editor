package exthost

import (
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/piplupflipper/vi-imitation/ext"
	"github.com/piplupflipper/vi-imitation/internal/buffer"
)

// testMemoryLimitMB は、上限を掛けるテストで使う RLIMIT_AS（MiB）である。
// testdata/memhog（Go）が起動して initialize に応答でき、test/alloc の確保（最大 4 GiB）の途中で当たる大きさにする。
// Go の実行時が起動できるかは上限について単調でなく、起動できない帯がある（Config.MemoryLimitMB の doc）。
// 2048 MiB は、2026-11-16 の計測（32 CPU の Linux、Go 1.27.4）で、小さな Go の拡張機能（ext-line）が 20/20 で起動した値である。
const testMemoryLimitMB = 2048

// memhogSpec は、testdata/memhog を -race を付けずにビルドし、それを拡張機能 id とする定義を返す。
// テストの実行ファイル自身を拡張機能にしないのは、-race の検出器が起動時に大きな仮想アドレス空間を予約し、
// 上限の下では起動できないためである。
func memhogSpec(t *testing.T, id string) Spec {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "memhog")
	build := exec.Command("go", "build", "-o", exe, "./testdata/memhog") //nolint:gosec // テスト用の拡張機能を一時ディレクトリへビルドする
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build memhog: %v\n%s", err, out)
	}
	m := ext.Manifest{
		ID:          id,
		Command:     exe,
		Activation:  []ext.Activation{{On: ext.ActivationTextObject, ID: id + ".obj"}},
		Contributes: ext.Contributes{TextObjects: []ext.Contribution{{ID: id + ".obj"}}},
	}
	if _, err := ext.ValidateManifests([]ext.Manifest{m}); err != nil {
		t.Fatal(err)
	}
	return Spec{Manifest: m}
}

// addressSpaceLimit は、/proc/<pid>/limits の "Max address space" の行のソフトとハードの値を返す。
// 上限なし（unlimited）は math.MaxUint64 にする。
func addressSpaceLimit(t *testing.T, pid int) (soft, hard uint64) {
	t.Helper()
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/limits", pid))
	if err != nil {
		t.Fatal(err)
	}
	parse := func(s string) uint64 {
		if s == "unlimited" {
			return math.MaxUint64
		}
		var n uint64
		if _, err := fmt.Sscan(s, &n); err != nil {
			t.Fatalf("limits: %q: %v", s, err)
		}
		return n
	}
	for line := range strings.Lines(string(b)) {
		if rest, ok := strings.CutPrefix(line, "Max address space"); ok {
			f := strings.Fields(rest)
			if len(f) < 2 {
				t.Fatalf("limits: %q", line)
			}
			return parse(f[0]), parse(f[1])
		}
	}
	t.Fatalf("no address space limit in /proc/%d/limits:\n%s", pid, b)
	return 0, 0
}

func TestMemoryLimitBytes(t *testing.T) {
	for _, tc := range []struct {
		mb   int64
		want uint64
	}{
		{0, 0},
		{-1, 0},
		{1, 1 << 20},
		{256, 256 << 20},
		{math.MaxInt64 >> 20, uint64(math.MaxInt64>>20) << 20},
		{math.MaxInt64>>20 + 1, 0},
		{math.MaxInt64, 0},
	} {
		if got := memoryLimitBytes(tc.mb); got != tc.want {
			t.Errorf("memoryLimitBytes(%d) = %d, want %d", tc.mb, got, tc.want)
		}
	}
}

// 上限を掛けた拡張機能は、自分とその子プロセスの RLIMIT_AS がソフトとハードの両方とも上限になる。
// 上限を掛けない（0）ときは、本体の上限をそのまま継ぐ。
//
// req: FR-20
func TestMemoryLimitApplied(t *testing.T) {
	selfSoft, selfHard := addressSpaceLimit(t, os.Getpid())
	spec := memhogSpec(t, "x")
	for _, tc := range []struct {
		name     string
		mb       int64
		wantSoft uint64
		wantHard uint64
	}{
		{"limited", testMemoryLimitMB, testMemoryLimitMB << 20, testMemoryLimitMB << 20},
		{"unlimited", 0, selfSoft, selfHard},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hs := newHarness(t, []Spec{spec}, func(c *Config) { c.MemoryLimitMB = tc.mb }, true)
			var pid int
			if err := hs.h.Call(ctx, "x", "test/pid", nil, callTimeout, &pid); err != nil {
				t.Fatal(err)
			}
			if soft, hard := addressSpaceLimit(t, pid); soft != tc.wantSoft || hard != tc.wantHard {
				t.Errorf("extension RLIMIT_AS = %d/%d, want %d/%d", soft, hard, tc.wantSoft, tc.wantHard)
			}
			var child int
			if err := hs.h.Call(ctx, "x", "test/grandchild", nil, callTimeout, &child); err != nil {
				t.Fatal(err)
			}
			if soft, hard := addressSpaceLimit(t, child); soft != tc.wantSoft || hard != tc.wantHard {
				t.Errorf("child of extension RLIMIT_AS = %d/%d, want %d/%d", soft, hard, tc.wantSoft, tc.wantHard)
			}
		})
	}
}

// 上限を掛けた拡張機能がメモリを使い切ると、確保に失敗して異常終了する。
// 本体は子プロセスの終了として扱って再起動し、本体（テストのプロセス）とバッファは残る（NFR-03）。
// 上限を掛けなければ、同じ確保は成功する（確保の量が上限を超えていることの確認）。
//
// req: FR-20, NFR-03
func TestMemoryLimitExceeded(t *testing.T) {
	spec := memhogSpec(t, "x")

	t.Run("unlimited", func(t *testing.T) {
		hs := newHarness(t, []Spec{spec}, nil, true)
		var got int
		if err := hs.h.Call(ctx, "x", "test/alloc", nil, callTimeout, &got); err != nil {
			t.Fatalf("test/alloc without limit: %v", err)
		}
		if got <= testMemoryLimitMB<<20 {
			t.Fatalf("allocated %d bytes, want more than the limit %d", got, testMemoryLimitMB<<20)
		}
	})

	t.Run("limited", func(t *testing.T) {
		hs := newHarness(t, []Spec{spec}, func(c *Config) { c.MemoryLimitMB = testMemoryLimitMB }, true)
		b := buffer.New("abc")
		if err := hs.h.OpenBuffer(snapshot(1, "", b)); err != nil {
			t.Fatal(err)
		}
		hs.h.Activate(ext.ActivationTextObject, "x.obj")
		hs.waitState("x", StateRunning)
		var before int
		if err := hs.h.Call(ctx, "x", "test/pid", nil, callTimeout, &before); err != nil {
			t.Fatal(err)
		}

		var got int
		err := hs.h.Call(ctx, "x", "test/alloc", nil, callTimeout, &got)
		if err == nil {
			t.Fatalf("allocated %d bytes under RLIMIT_AS %d MiB", got, testMemoryLimitMB)
		}
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("call test/alloc: %v, want ErrUnavailable", err)
		}
		sc := hs.waitState("x", StateRestartWait)
		if !errors.Is(sc.Err, errExited) && !errors.Is(sc.Err, errConnClosed) {
			t.Fatalf("restart reason = %v", sc.Err)
		}
		// Go の実行時は、確保に失敗すると "fatal error: out of memory" で終わる。
		hs.waitLog("out of memory")
		if !processGone(before) {
			t.Fatalf("old process %d is still alive", before)
		}

		// 本体は動き続け、バッファを変更でき、再起動した拡張機能にも同じ上限が掛かる。
		c, err := b.Apply(buffer.Transaction{BaseVersion: 0, Edits: []buffer.Edit{{Text: "new "}}})
		if err != nil {
			t.Fatal(err)
		}
		if err := hs.h.ChangeBuffer(1, c); err != nil {
			t.Fatal(err)
		}
		hs.waitState("x", StateRunning)
		var after int
		if err := hs.h.Call(ctx, "x", "test/pid", nil, callTimeout, &after); err != nil {
			t.Fatal(err)
		}
		if after == before {
			t.Fatal("extension was not restarted")
		}
		if soft, _ := addressSpaceLimit(t, after); soft != testMemoryLimitMB<<20 {
			t.Fatalf("restarted extension RLIMIT_AS = %d, want %d", soft, testMemoryLimitMB<<20)
		}
	})
}

// 本体のハードの上限が求めた上限より小さければ、ハードの上限を超えない値にする（権限の要らない範囲で掛ける）。
func TestLimitAddressSpaceKeepsHardLimit(t *testing.T) {
	cmd := exec.Command("sleep", "1000")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	pid := cmd.Process.Pid
	const hard = 1 << 30
	if err := prlimit(pid, syscall.RLIMIT_AS, &rlimit64{Cur: hard, Max: hard}, nil); err != nil {
		t.Fatal(err)
	}
	if err := limitAddressSpace(pid, 4<<30); err != nil {
		t.Fatalf("limitAddressSpace above hard limit: %v", err)
	}
	if soft, h := addressSpaceLimit(t, pid); soft != hard || h != hard {
		t.Fatalf("RLIMIT_AS = %d/%d, want %d/%d", soft, h, hard, hard)
	}
	if err := limitAddressSpace(pid, 256<<20); err != nil {
		t.Fatal(err)
	}
	if soft, h := addressSpaceLimit(t, pid); soft != 256<<20 || h != 256<<20 {
		t.Fatalf("RLIMIT_AS = %d/%d, want %d/%d", soft, h, 256<<20, 256<<20)
	}
}
