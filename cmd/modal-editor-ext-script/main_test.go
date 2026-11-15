package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/traefik/yaegi/extract"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/internal/exthost"
)

// envRunAsMain が設定されていれば、テストの実行ファイルは main として動く。
// exthost が子プロセスとして起動する実行ファイルに、テストの実行ファイル自身を使うためである。
const envRunAsMain = "MODAL_EDITOR_EXT_SCRIPT_TEST_MAIN"

func TestMain(m *testing.M) {
	if os.Getenv(envRunAsMain) == "1" {
		main()
		return
	}
	// 子プロセスはこのプロセスの環境変数を引き継ぐ。
	if err := os.Setenv(envRunAsMain, "1"); err != nil {
		panic(err)
	}
	code := m.Run()
	os.Exit(code)
}

const (
	callTimeout = 10 * time.Second
	waitTimeout = 20 * time.Second
)

// syncBuffer は、複数の goroutine から書けるバッファである。exthost のログを集める。
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// host は、スクリプトの拡張機能を 1 つだけ持つ exthost.Host と、その状態の変化とログである。
type host struct {
	t   *testing.T
	h   *exthost.Host
	log *syncBuffer

	mu     sync.Mutex
	states []exthost.StateChange
}

// loadManifest は、testdata/<dir>/manifest.json を読む。
func loadManifest(t *testing.T, dir string) ext.Manifest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m ext.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("manifest.json: %v", err)
	}
	return m
}

// startHost は、宣言ファイル m の拡張機能を、本体の監視（exthost）の下で動かす。
// 宣言ファイルのディレクトリは testdata/<dir> とし、command はテストの実行ファイル自身に置き換える。
// args は宣言ファイルのまま渡し、相対パスは exthost が渡す環境変数 ext.EnvExtensionDir から解決される。
func startHost(t *testing.T, m ext.Manifest, dir string, cfg func(*exthost.Config)) *host {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(filepath.Join("testdata", dir))
	if err != nil {
		t.Fatal(err)
	}
	m.Command = exe

	logw := &syncBuffer{}
	c := exthost.Config{
		InitTimeout:  callTimeout,
		PingInterval: 100 * time.Millisecond,
		PingTimeout:  time.Second,
		RestartDelay: time.Hour, // 再起動待ちの状態を観察するため、テストの間は再起動しない。
		Logger:       slog.New(slog.NewTextHandler(logw, nil)),
	}
	if cfg != nil {
		cfg(&c)
	}
	h, err := exthost.New([]exthost.Spec{{Manifest: m, Dir: abs}}, c)
	if err != nil {
		t.Fatal(err)
	}
	hs := &host{t: t, h: h, log: logw}
	t.Cleanup(func() {
		_ = h.Close()
		if t.Failed() {
			t.Logf("host log:\n%s", logw.String())
		}
	})
	go func() {
		for ev := range h.Events() {
			if sc, ok := ev.(*exthost.StateChange); ok {
				hs.mu.Lock()
				hs.states = append(hs.states, *sc)
				hs.mu.Unlock()
			}
		}
	}()
	if err := h.OpenBuffer(exthost.BufferSnapshot{ID: 1, Version: 1, Text: "  \tfoo bar \t\n\n   \n  あい　\nx"}); err != nil {
		t.Fatal(err)
	}
	return hs
}

// waitState は、拡張機能 id が st になるまで待ち、その変化を返す。
func (hs *host) waitState(id string, st exthost.State) exthost.StateChange {
	hs.t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for time.Now().Before(deadline) {
		hs.mu.Lock()
		for _, sc := range hs.states {
			if sc.Extension == id && sc.State == st {
				hs.mu.Unlock()
				return sc
			}
		}
		hs.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	hs.t.Fatalf("timed out waiting for %s to be %v", id, st)
	return exthost.StateChange{}
}

// waitLog は、ログに s が現れるまで待つ。
func (hs *host) waitLog(s string) {
	hs.t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for time.Now().Before(deadline) {
		if strings.Contains(hs.log.String(), s) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	hs.t.Fatalf("timed out waiting for log %q", s)
}

func (hs *host) resolve(id, obj string, cursor ext.Position, count int) (ext.TextObjectResolveResult, error) {
	hs.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	return hs.h.ResolveTextObject(ctx, id, ext.TextObjectResolveParams{Buffer: 1, Version: 1, ID: obj, Cursor: cursor, Count: count}, callTimeout)
}

func pos(line, b int) ext.Position { return ext.Position{Line: line, Byte: b} }

// TestLineScript は、例のスクリプト（testdata/line）を、宣言ファイルの command と args で本体の監視の下に起動し、
// textObject/resolve に答えることを確かめる。期待する範囲は modal-editor-ext-line の line.inner と同じである。
//
// req: FR-10, FR-36
func TestLineScript(t *testing.T) {
	m := loadManifest(t, "line")
	hs := startHost(t, m, "line", nil)
	if got := hs.h.Activate(ext.ActivationTextObject, "script.inner"); len(got) != 1 || got[0] != m.ID {
		t.Fatalf("Activate = %v, want [%s]", got, m.ID)
	}
	hs.waitState(m.ID, exthost.StateRunning)

	tests := []struct {
		name   string
		cursor ext.Position
		count  int
		ok     bool
		want   ext.Range
	}{
		{"trims both ends", pos(0, 5), 0, true, ext.Range{Start: pos(0, 3), End: pos(0, 10)}},
		{"empty line fails", pos(1, 0), 0, false, ext.Range{}},
		{"blank line fails", pos(2, 1), 0, false, ext.Range{}},
		{"keeps ideographic space", pos(3, 2), 0, true, ext.Range{Start: pos(3, 2), End: pos(3, 11)}},
		{"count spans lines", pos(0, 0), 4, true, ext.Range{Start: pos(0, 3), End: pos(3, 11)}},
		{"last line", pos(4, 0), 0, true, ext.Range{Start: pos(4, 0), End: pos(4, 1)}},
		{"count beyond last line fails", pos(3, 0), 3, false, ext.Range{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := hs.resolve(m.ID, "script.inner", tt.cursor, tt.count)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if got.OK != tt.ok {
				t.Fatalf("OK = %v, want %v (result %+v)", got.OK, tt.ok, got)
			}
			if got.Version != 1 {
				t.Errorf("Version = %d, want 1", got.Version)
			}
			if tt.ok && got.Range != tt.want {
				t.Errorf("Range = %+v, want %+v", got.Range, tt.want)
			}
		})
	}
}

// TestManifest は、例の宣言ファイルが検査を通り、command と args でこの実行ファイルとスクリプトを指すことを確かめる。
func TestManifest(t *testing.T) {
	m := loadManifest(t, "line")
	if warnings, err := m.Validate(); err != nil || len(warnings) > 0 {
		t.Fatalf("Validate = %v, %v; want no warnings and no error", warnings, err)
	}
	if m.Command != "./"+name {
		t.Errorf("command = %q, want ./%s", m.Command, name)
	}
	if len(m.Args) != 1 || m.Args[0] != "line.go" {
		t.Errorf("args = %q, want [line.go]", m.Args)
	}
	if _, err := os.Stat(filepath.Join("testdata", "line", m.Args[0])); err != nil {
		t.Errorf("script in args: %v", err)
	}
}

// TestLoadError は、読み込めないスクリプトで、理由を標準エラー出力に書いて終了コード 1 で終わることを確かめる。
//
// req: FR-36
func TestLoadError(t *testing.T) {
	tests := []struct {
		file string
		want []string
	}{
		// 構文の誤りは、パス:行:列 の形で場所を示す。
		{"syntax.go", []string{"syntax error", "testdata/broken/syntax.go:5:1:"}},
		{"nomain.go", []string{"func main is not declared"}},
		{"notmain.go", []string{"package is script, want main"}},
		// 外部のモジュールは import できない（基本設計 6.6）。
		{"import.go", []string{"testdata/broken/import.go", "example.com/notexist/pkg"}},
		// 型の誤り（無いメソッドの呼び出し）は、yaegi が実行の前に見つける。
		{"typeerror.go", []string{"testdata/broken/typeerror.go:7", "NoSuchMethod"}},
		// main の panic は、yaegi が回復して誤りにする。
		{"panic.go", []string{"panic: panic at load"}},
		{"missing.go", []string{"no such file or directory"}},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			var stderr bytes.Buffer
			path := filepath.Join("testdata", "broken", tt.file)
			if code := run([]string{path}, &stderr); code != 1 {
				t.Errorf("exit code = %d, want 1", code)
			}
			for _, w := range tt.want {
				if !strings.Contains(stderr.String(), w) {
					t.Errorf("stderr does not contain %q:\n%s", w, stderr.String())
				}
			}
		})
	}
}

// TestUsage は、スクリプトのパスを 1 つ渡さなければ、使い方を書いて終了コード 2 で終わることを確かめる。
func TestUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"a.go", "b.go"}} {
		var stderr bytes.Buffer
		if code := run(args, &stderr); code != 2 {
			t.Errorf("run(%q) = %d, want 2", args, code)
		}
		if !strings.Contains(stderr.String(), "usage:") {
			t.Errorf("run(%q) stderr = %q, want usage", args, stderr.String())
		}
	}
}

// TestLoadErrorUnderHost は、構文の誤りのあるスクリプトの拡張機能を、本体の監視が子プロセスの終了として扱い、
// 理由を標準エラー出力のログに残すことを確かめる。
//
// req: NFR-03
func TestLoadErrorUnderHost(t *testing.T) {
	m := ext.Manifest{
		ID:          "broken",
		Args:        []string{"syntax.go"},
		Activation:  []ext.Activation{{On: ext.ActivationTextObject, ID: "broken.obj"}},
		Contributes: ext.Contributes{TextObjects: []ext.Contribution{{ID: "broken.obj", Keys: "ib"}}},
	}
	hs := startHost(t, m, "broken", nil)
	hs.h.Activate(ext.ActivationTextObject, "broken.obj")
	hs.waitState(m.ID, exthost.StateRestartWait)
	hs.waitLog("syntax.go:5:1:")
}

// faultyManifest は、testdata/faulty のスクリプトの宣言ファイルである。
func faultyManifest() ext.Manifest {
	ids := []string{"faulty.print", "faulty.panic", "faulty.gopanic", "faulty.hang", "faulty.stdin", "faulty.exit"}
	m := ext.Manifest{ID: "faulty", Args: []string{"faulty.go"}}
	for i, id := range ids {
		m.Activation = append(m.Activation, ext.Activation{On: ext.ActivationTextObject, ID: id})
		m.Contributes.TextObjects = append(m.Contributes.TextObjects, ext.Contribution{ID: id, Keys: "i" + string(rune('a'+i))})
	}
	return m
}

// startFaulty は、testdata/faulty のスクリプトを本体の監視の下で稼働中にする。
func startFaulty(t *testing.T, cfg func(*exthost.Config)) *host {
	t.Helper()
	hs := startHost(t, faultyManifest(), "faulty", cfg)
	hs.h.Activate(ext.ActivationTextObject, "faulty.print")
	hs.waitState("faulty", exthost.StateRunning)
	return hs
}

// TestStdoutGoesToLog は、スクリプトが標準出力へ書いても本体とのメッセージが壊れず、書いた内容がログに残ることを確かめる。
func TestStdoutGoesToLog(t *testing.T) {
	hs := startFaulty(t, nil)
	got, err := hs.resolve("faulty", "faulty.print", pos(0, 3), 0)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !got.OK {
		t.Fatalf("result = %+v, want OK", got)
	}
	hs.waitLog("printed to stdout")
	if st, _ := hs.h.State("faulty"); st != exthost.StateRunning {
		t.Errorf("state = %v, want running", st)
	}
}

// TestStdinIsEmpty は、スクリプトが os.Stdin を読んでもすぐ EOF になり、本体とのメッセージを奪わないことを確かめる。
// 生存確認の期限の数倍の時間をおいても稼働中のまま答え続ける。
func TestStdinIsEmpty(t *testing.T) {
	hs := startFaulty(t, func(c *exthost.Config) { c.PingTimeout = 300 * time.Millisecond })
	got, err := hs.resolve("faulty", "faulty.stdin", pos(0, 0), 0)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !got.OK {
		t.Fatalf("result = %+v, want OK (os.Stdin returned EOF)", got)
	}
	time.Sleep(time.Second)
	if _, err := hs.resolve("faulty", "faulty.print", pos(0, 0), 0); err != nil {
		t.Fatalf("resolve after reading stdin: %v", err)
	}
	if st, _ := hs.h.State("faulty"); st != exthost.StateRunning {
		t.Errorf("state = %v, want running", st)
	}
}

// TestExit は、スクリプトの os.Exit でプロセスがその終了コードで終わり、本体の監視が異常として扱うことを確かめる。
// yaegi の制限なしのモードで動かすので、os.Exit は panic に置き換わらない。
//
// req: NFR-03
func TestExit(t *testing.T) {
	hs := startFaulty(t, nil)
	if _, err := hs.resolve("faulty", "faulty.exit", pos(0, 0), 0); !errors.Is(err, exthost.ErrUnavailable) {
		t.Fatalf("resolve = %v, want ErrUnavailable", err)
	}
	hs.waitState("faulty", exthost.StateRestartWait)
	hs.waitLog("exit status 5")
}

// TestScriptPath は、args の相対パスを環境変数 ext.EnvExtensionDir のディレクトリから解決することを確かめる。
func TestScriptPath(t *testing.T) {
	tests := []struct{ arg, dir, want string }{
		{"line.go", "/ext/line", "/ext/line/line.go"},
		{"sub/line.go", "/ext", "/ext/sub/line.go"},
		{"/abs/line.go", "/ext", "/abs/line.go"},
		{"line.go", "", "line.go"},
	}
	for _, tt := range tests {
		if got := scriptPath(tt.arg, tt.dir); got != tt.want {
			t.Errorf("scriptPath(%q, %q) = %q, want %q", tt.arg, tt.dir, got, tt.want)
		}
	}
	// run も同じ規則で探す。
	abs, err := filepath.Abs(filepath.Join("testdata", "broken"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(ext.EnvExtensionDir, abs)
	var stderr bytes.Buffer
	if code := run([]string{"syntax.go"}, &stderr); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if want := filepath.Join(abs, "syntax.go") + ":5:1:"; !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr does not contain %q:\n%s", want, stderr.String())
	}
}

// TestHandlerPanic は、ハンドラーの panic が、そのリクエストの誤りの応答になり、拡張機能は動き続けることを確かめる。
// コンパイルする拡張機能と同じく、ext/server が回復する。
func TestHandlerPanic(t *testing.T) {
	hs := startFaulty(t, nil)
	if _, err := hs.resolve("faulty", "faulty.panic", pos(0, 0), 0); err == nil || !strings.Contains(err.Error(), "panic in handler") {
		t.Fatalf("resolve = %v, want the panic as an error", err)
	}
	if _, err := hs.resolve("faulty", "faulty.print", pos(0, 0), 0); err != nil {
		t.Fatalf("resolve after panic: %v", err)
	}
	if st, _ := hs.h.State("faulty"); st != exthost.StateRunning {
		t.Errorf("state = %v, want running", st)
	}
}

// TestGoroutinePanic は、スクリプトが起動した goroutine の panic でプロセスが終わり、本体の監視が異常として扱うことを確かめる。
//
// req: NFR-03
func TestGoroutinePanic(t *testing.T) {
	hs := startFaulty(t, nil)
	_, err := hs.resolve("faulty", "faulty.gopanic", pos(0, 0), 0)
	if !errors.Is(err, exthost.ErrUnavailable) {
		t.Fatalf("resolve = %v, want ErrUnavailable", err)
	}
	hs.waitState("faulty", exthost.StateRestartWait)
	hs.waitLog("panic in goroutine")
}

// TestHang は、ハンドラーの無限ループで生存確認が期限を超え、本体の監視が強制終了することを確かめる。
//
// req: NFR-03
func TestHang(t *testing.T) {
	hs := startFaulty(t, func(c *exthost.Config) { c.PingTimeout = 300 * time.Millisecond })
	ctx := context.Background()
	_, err := hs.h.ResolveTextObject(ctx, "faulty", ext.TextObjectResolveParams{Buffer: 1, Version: 1, ID: "faulty.hang"}, 100*time.Millisecond)
	if !errors.Is(err, exthost.ErrTimeout) {
		t.Fatalf("resolve = %v, want ErrTimeout", err)
	}
	sc := hs.waitState("faulty", exthost.StateRestartWait)
	if sc.Err == nil || !strings.Contains(sc.Err.Error(), "ping") {
		t.Errorf("restart reason = %v, want ping timeout", sc.Err)
	}
}

// TestSymbolsUpToDate は、生成した記号の表（github_com-*.go）が、いまの ext と ext/server から生成し直したものと一致することを確かめる。
// ext に公開の型や関数を足したのに go generate を打ち忘れると、スクリプトから使えないまま気づかないので、ここで落とす。
func TestSymbolsUpToDate(t *testing.T) {
	for _, pkg := range []string{
		"github.com/piplupflipper/modal-editor/ext",
		"github.com/piplupflipper/modal-editor/ext/server",
	} {
		e := extract.Extractor{Dest: "main"}
		var buf bytes.Buffer
		if _, err := e.Extract(pkg, "main", &buf); err != nil {
			t.Fatalf("extract %s: %v", pkg, err)
		}
		file := strings.NewReplacer("/", "-", ".", "_").Replace(pkg) + ".go"
		got, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, buf.Bytes()) {
			t.Errorf("%s is out of date; run go generate ./cmd/modal-editor-ext-script", file)
		}
	}
}
