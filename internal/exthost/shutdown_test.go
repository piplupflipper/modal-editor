package exthost

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/piplupflipper/modal-editor/ext"
)

// 正常な終了（基本設計 5.2）の手順を確かめる。
// shutdown を受け取った拡張機能は自分で片付けて終わり、応答しない拡張機能は期限後に SIGTERM、さらに期限後に SIGKILL で終わる。

// 終了の手順の記録（process.go の terminate）。
const (
	logSIGTERM = "sending SIGTERM"
	logSIGKILL = "sending SIGKILL"
)

// spawnChild は、拡張機能 srv に別のプロセスグループの子プロセスを起動させ、その pid と拡張機能の pid を返す。
// 子プロセスは、本体がプロセスグループへ送るシグナルでは終わらない（DAP のデバッグ対象と同じ）。
func spawnChild(t *testing.T, hs *harness) (child, extPID int) {
	t.Helper()
	if err := hs.h.OpenBuffer(BufferSnapshot{ID: 1, Version: 1, Text: "x"}); err != nil {
		t.Fatal(err)
	}
	hs.execute(cmdSpawn, nil, false)
	if _, err := fmt.Sscan(hs.waitOutput(cmdSpawn), &child, &extPID); err != nil {
		t.Fatal(err)
	}
	// 片付けに失敗したときに、テストの後へ残さない。
	t.Cleanup(func() { _ = syscall.Kill(child, syscall.SIGKILL) })
	return child, extPID
}

// Close は shutdown を送り、拡張機能が子プロセスを片付けて自分で終わるのを待つ。SIGTERM は送らない。
func TestCloseShutsDownGracefully(t *testing.T) {
	const shutdownTimeout = 5 * time.Second
	hs := newHarness(t, []Spec{serverSpec(t, "srv", nil)}, func(c *Config) { c.ShutdownTimeout = shutdownTimeout }, true)
	child, extPID := spawnChild(t, hs)

	start := time.Now()
	if err := hs.h.Close(); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d >= shutdownTimeout {
		t.Errorf("Close took %s; the extension did not exit on shutdown", d)
	}
	if !processGone(child) {
		t.Errorf("child %d in another process group is still alive; the shutdown hook did not run", child)
	}
	if !processGone(extPID) {
		t.Errorf("extension %d is still alive after Close", extPID)
	}
	if log := hs.log.String(); strings.Contains(log, logSIGTERM) || strings.Contains(log, logSIGKILL) {
		t.Errorf("signals were sent to an extension that exits on shutdown:\n%s", log)
	}
}

// shutdown を無視する拡張機能は、期限後の SIGTERM で終わる。
func TestCloseSendsSIGTERMAfterTimeout(t *testing.T) {
	const shutdownTimeout = 300 * time.Millisecond
	const terminateTimeout = 5 * time.Second
	hs := newHarness(t, []Spec{fakeSpec(t, "x", modeIgnoreShutdown, "")}, func(c *Config) {
		c.ShutdownTimeout = shutdownTimeout
		c.TerminateTimeout = terminateTimeout
	}, false)
	pid := hs.fakeState().PID

	start := time.Now()
	if err := hs.h.Close(); err != nil {
		t.Fatal(err)
	}
	d := time.Since(start)
	if d < shutdownTimeout || d >= shutdownTimeout+terminateTimeout {
		t.Errorf("Close took %s, want between %s and %s", d, shutdownTimeout, shutdownTimeout+terminateTimeout)
	}
	if !processGone(pid) {
		t.Errorf("process %d is still alive after Close", pid)
	}
	log := hs.log.String()
	if !strings.Contains(log, logSIGTERM) || strings.Contains(log, logSIGKILL) {
		t.Errorf("want SIGTERM and no SIGKILL:\n%s", log)
	}
}

// shutdown も SIGTERM も無視する拡張機能は、さらに期限後の SIGKILL で終わる。
func TestCloseKillsUnresponsiveExtension(t *testing.T) {
	const shutdownTimeout = 200 * time.Millisecond
	const terminateTimeout = 300 * time.Millisecond
	hs := newHarness(t, []Spec{fakeSpec(t, "x", modeStubborn, "")}, func(c *Config) {
		c.ShutdownTimeout = shutdownTimeout
		c.TerminateTimeout = terminateTimeout
	}, false)
	s := hs.fakeState()

	start := time.Now()
	if err := hs.h.Close(); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < shutdownTimeout+terminateTimeout {
		t.Errorf("Close took %s, want at least %s", d, shutdownTimeout+terminateTimeout)
	}
	if !processGone(s.PID) {
		t.Errorf("process %d is still alive after Close", s.PID)
	}
	// 拡張機能は SIGTERM を受け取ったが終わらず、SIGKILL で終わった。
	hs.waitLog(stubbornSIGTERM)
	hs.waitLog(logSIGKILL)
}

// 異常（生存確認の期限切れなど）で終わらせるときは、shutdown を送らずにすぐ SIGKILL を送る。
func TestAbnormalExitSkipsShutdown(t *testing.T) {
	// modeNormal は shutdown で終わるが、読み取りの goroutine が止まっているので shutdown を処理できない。
	// shutdown を送って待てば ShutdownTimeout だけかかる。
	hs := newHarness(t, []Spec{fakeSpec(t, "x", modeNormal, "")}, func(c *Config) {
		c.PingTimeout = 300 * time.Millisecond
		c.ShutdownTimeout = 10 * time.Second
	}, true)
	hs.h.Activate(ext.ActivationTextObject, "x.obj")
	hs.waitState("x", StateRunning)
	before := hs.fakeState().PID
	if err := hs.h.Call(ctx, "x", methodHang, nil, 100*time.Millisecond, nil); !errors.Is(err, ErrTimeout) {
		t.Fatalf("call test/hang: %v, want ErrTimeout", err)
	}
	start := time.Now()
	sc := hs.waitState("x", StateRestartWait)
	if !errors.Is(sc.Err, errPingTimeout) {
		t.Fatalf("restart reason = %v, want ping timeout", sc.Err)
	}
	if !processGone(before) {
		t.Fatalf("hung process %d is still alive", before)
	}
	if d := time.Since(start); d >= 5*time.Second {
		t.Errorf("abnormal exit took %s; it waited for shutdown", d)
	}
	hs.waitState("x", StateRunning)
	for _, l := range hs.fakeState().Log {
		if l == "notification:"+ext.MethodShutdown {
			t.Errorf("the restarted extension received shutdown")
		}
	}
	if strings.Contains(hs.log.String(), logSIGTERM) {
		t.Errorf("SIGTERM was sent on an abnormal exit")
	}
}

// Disable は、すぐに停止中へ移し、Close と同じ手順で拡張機能を止めて無効にする。
// 停止中は問い合わせと willSave を送らずに失敗させ、shutdown の後に届いたリクエストには失敗を応答する。
// 止めたことは連続の失敗に数えず、無効へ移った後に Restart で起動し直せる。
func TestDisableShutsDown(t *testing.T) {
	hs := newHarness(t, []Spec{serverSpec(t, "srv", []string{argSlowShutdown})}, func(c *Config) { c.ShutdownTimeout = 5 * time.Second }, true)
	child, extPID := spawnChild(t, hs)

	if err := hs.h.Disable("srv"); err != nil {
		t.Fatal(err)
	}
	if st := hs.state("srv"); st != StateStopping {
		t.Fatalf("state right after Disable = %s, want stopping", st)
	}
	hs.waitState("srv", StateStopping)

	// 停止中: 問い合わせは送らずにすぐ失敗し、willSave の対象にも入らない。Restart と Disable は何もしない。
	start := time.Now()
	if _, err := hs.h.ExecuteCommand(ctx, "srv", ext.CommandExecuteParams{Buffer: 1, Version: 1, ID: cmdState}, callTimeout); !errors.Is(err, ErrUnavailable) {
		t.Errorf("command while stopping: %v, want ErrUnavailable", err)
	}
	if d := time.Since(start); d > 200*time.Millisecond {
		t.Errorf("command while stopping took %s; it waited for the extension", d)
	}
	if ids := hs.h.SaveHookExtensions(); len(ids) != 0 {
		t.Errorf("SaveHookExtensions while stopping = %v, want none", ids)
	}
	if err := hs.h.Restart("srv"); err != nil {
		t.Errorf("Restart while stopping: %v", err)
	}
	if err := hs.h.Disable("srv"); err != nil {
		t.Errorf("Disable while stopping: %v", err)
	}
	if st := hs.state("srv"); st != StateStopping {
		t.Errorf("state after Restart while stopping = %s, want stopping", st)
	}

	// shutdown の後に拡張機能が送った buffer/edit は、イベントループへ渡らずに失敗する。
	if out := hs.waitOutput(shutdownEdit); !strings.HasPrefix(out, "error ") || !strings.Contains(out, "shutting down") {
		t.Errorf("buffer/edit after shutdown = %q, want an error", out)
	}

	sc := hs.waitState("srv", StateDisabled)
	if sc.Err != nil {
		t.Errorf("disable reason = %v, want nil", sc.Err)
	}
	hs.mu.Lock()
	for _, ev := range hs.events {
		if _, ok := ev.(*EditRequest); ok {
			t.Errorf("buffer/edit after shutdown reached the event loop")
		}
	}
	hs.mu.Unlock()
	if !processGone(child) {
		t.Errorf("child %d is still alive; the shutdown hook did not run", child)
	}
	if !processGone(extPID) {
		t.Errorf("extension %d is still alive after Disable", extPID)
	}
	if strings.Contains(hs.log.String(), logSIGTERM) {
		t.Errorf("SIGTERM was sent to an extension that exits on shutdown")
	}
	if _, err := hs.h.ExecuteCommand(ctx, "srv", ext.CommandExecuteParams{Buffer: 1, Version: 1, ID: cmdState}, callTimeout); !errors.Is(err, ErrUnavailable) {
		t.Errorf("command to disabled extension: %v, want ErrUnavailable", err)
	}

	if err := hs.h.Restart("srv"); err != nil {
		t.Fatal(err)
	}
	hs.waitState("srv", StateRunning)
}

// CloseContext は、ctx が終わったら期限を待たずに SIGKILL で終わらせ、ctx.Err() を返す。
func TestCloseContextCancels(t *testing.T) {
	hs := newHarness(t, []Spec{fakeSpec(t, "x", modeStubborn, "")}, func(c *Config) {
		c.ShutdownTimeout = 10 * time.Second
		c.TerminateTimeout = 10 * time.Second
	}, false)
	pid := hs.fakeState().PID

	cctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := hs.h.CloseContext(cctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("CloseContext = %v, want DeadlineExceeded", err)
	}
	if d := time.Since(start); d >= 5*time.Second {
		t.Errorf("CloseContext took %s; it waited for the shutdown timeout", d)
	}
	if !processGone(pid) {
		t.Errorf("process %d is still alive after CloseContext", pid)
	}
	if err := hs.h.Close(); err != nil {
		t.Errorf("second Close = %v", err)
	}
}

// 未起動と再起動待ちの拡張機能の Disable は、プロセスを待たずに無効にする。
// Reload は、稼働中なら正常に止めて未起動へ戻し、次の契機で起動し直す（:ext sync で実行ファイルを入れ替えた後。基本設計 5.6）。
// 再起動待ちと無効なら、すぐに未起動へ戻す。
func TestReload(t *testing.T) {
	hs := newHarness(t, []Spec{fakeSpec(t, "idle", modeNormal, ""), fakeSpec(t, "crash", modeExit, "")}, func(c *Config) {
		c.RestartDelay = time.Hour
		c.MaxRestartDelay = time.Hour
	}, true)

	hs.h.Activate(ext.ActivationTextObject, "idle.obj")
	hs.waitState("idle", StateRunning)
	if err := hs.h.Reload("idle"); err != nil {
		t.Fatal(err)
	}
	if st := hs.state("idle"); st != StateStopping {
		t.Fatalf("state right after Reload = %s, want stopping", st)
	}
	hs.waitState("idle", StateStopping)
	if sc := hs.waitState("idle", StateNotStarted); sc.Err != nil {
		t.Errorf("reload reason = %v, want nil", sc.Err)
	}
	if started := hs.h.Activate(ext.ActivationTextObject, "idle.obj"); len(started) != 1 {
		t.Errorf("activation after Reload started %v, want idle", started)
	}
	hs.waitState("idle", StateRunning)

	hs.h.Activate(ext.ActivationTextObject, "crash.obj")
	hs.waitState("crash", StateRestartWait)
	if err := hs.h.Reload("crash"); err != nil {
		t.Fatal(err)
	}
	hs.waitState("crash", StateNotStarted)

	if err := hs.h.Disable("crash"); err != nil {
		t.Fatal(err)
	}
	hs.waitState("crash", StateDisabled)
	if err := hs.h.Reload("crash"); err != nil {
		t.Fatal(err)
	}
	if st := hs.state("crash"); st != StateNotStarted {
		t.Errorf("state after Reload of disabled = %s, want not started", st)
	}
	if err := hs.h.Reload("nope"); !errors.Is(err, ErrUnknownExtension) {
		t.Errorf("Reload(unknown) = %v, want ErrUnknownExtension", err)
	}
}

func TestDisableNotRunning(t *testing.T) {
	hs := newHarness(t, []Spec{fakeSpec(t, "idle", modeNormal, ""), fakeSpec(t, "crash", modeExit, "")}, func(c *Config) {
		c.RestartDelay = time.Hour
		c.MaxRestartDelay = time.Hour
	}, true)

	if err := hs.h.Disable("idle"); err != nil {
		t.Fatal(err)
	}
	hs.waitState("idle", StateDisabled)
	if started := hs.h.Activate(ext.ActivationTextObject, "idle.obj"); started != nil {
		t.Errorf("activation started a disabled extension: %v", started)
	}

	hs.h.Activate(ext.ActivationTextObject, "crash.obj")
	hs.waitState("crash", StateRestartWait)
	if err := hs.h.Disable("crash"); err != nil {
		t.Fatal(err)
	}
	if sc := hs.waitState("crash", StateDisabled); sc.Err != nil {
		t.Errorf("disable reason = %v, want nil", sc.Err)
	}
	if err := hs.h.Disable("nope"); !errors.Is(err, ErrUnknownExtension) {
		t.Errorf("Disable(unknown) = %v, want ErrUnknownExtension", err)
	}
}
