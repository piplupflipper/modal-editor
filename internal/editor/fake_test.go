package editor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/ext/server"
	"github.com/piplupflipper/modal-editor/internal/editcmd"
	"github.com/piplupflipper/modal-editor/internal/exthost"
	"github.com/piplupflipper/modal-editor/internal/ui"
)

// テスト用の拡張機能は、テストの実行ファイル自身を、引数 fakeExtFlag と動き方の名前を付けて起動したものである。
// TestMain はこの引数を見たら、テストを実行せずに ext/server で書いた拡張機能として動く。
const fakeExtFlag = "-editor-fake-ext"

// 拡張機能の動き方。
const (
	// fakeMain は、テキストオブジェクトとコマンドを持つ拡張機能（ID は "fake"）である。
	fakeMain = "main"
	// fakeFileType は、ファイルの種類 "fakeft" の契機だけで起動する拡張機能（ID は "ftx"）である。
	fakeFileType = "ft"
	// fakeSlowInit は、initialize の応答を slowInitDelay だけ遅らせる拡張機能（ID は "slow"）である。
	fakeSlowInit = "slowinit"
)

const (
	slowDelay     = 300 * time.Millisecond
	slowInitDelay = 1500 * time.Millisecond
)

func TestMain(m *testing.M) {
	for i, a := range os.Args {
		if a == fakeExtFlag && i+1 < len(os.Args) {
			os.Exit(runFakeExt(os.Args[i+1]))
		}
	}
	os.Exit(m.Run())
}

// lineRange は、カーソルの行の全体の範囲を返す。
func lineRange(b *server.Buffer, line int) ext.Range {
	return ext.Range{Start: ext.Position{Line: line}, End: ext.Position{Line: line, Byte: len(b.Line(line))}}
}

func runFakeExt(mode string) int {
	s := server.New()
	// hostCtx は、ハンドラーから起こした goroutine が本体へリクエストを送るときの ctx である。
	// ハンドラーの ctx で送ると ErrEditInHandler になる（ext/server の Edit の doc）。
	hostCtx := context.Background()
	var mu sync.Mutex
	opened := map[ext.BufferID]bool{}
	var bufLog []string
	switch mode {
	case fakeMain:
		s.TextObject("fake.line", func(_ context.Context, b *server.Buffer, p *ext.TextObjectResolveParams) (ext.TextObjectResolveResult, error) {
			return ext.TextObjectResolveResult{OK: true, Range: lineRange(b, p.Cursor.Line)}, nil
		})
		s.TextObjectAsync("fake.slow", func(_ context.Context, b *server.Buffer, p *ext.TextObjectResolveParams, reply func(ext.TextObjectResolveResult, error)) {
			r := ext.TextObjectResolveResult{OK: true, Version: p.Version, Range: lineRange(b, p.Cursor.Line)}
			go func() {
				time.Sleep(slowDelay)
				reply(r, nil)
			}()
		})
		s.TextObjectAsync("fake.hang", func(ctx context.Context, _ *server.Buffer, _ *ext.TextObjectResolveParams, reply func(ext.TextObjectResolveResult, error)) {
			go func() {
				<-ctx.Done()
				reply(ext.TextObjectResolveResult{}, ctx.Err())
			}()
		})
		s.Command("fake.upper", func(_ context.Context, b *server.Buffer, p *ext.CommandExecuteParams) (ext.CommandExecuteResult, error) {
			l := p.Cursor.Line
			tx := ext.Transaction{Edits: []ext.Edit{{Range: lineRange(b, l), Text: strings.ToUpper(b.Line(l))}}}
			return ext.CommandExecuteResult{OK: true, Transaction: &tx}, nil
		})
		s.Command("fake.jump", func(_ context.Context, b *server.Buffer, p *ext.CommandExecuteParams) (ext.CommandExecuteResult, error) {
			id := b.ID()
			to := ext.Position{Line: min(p.Cursor.Line+1, b.LineCount()-1)}
			tx := ext.Transaction{Edits: []ext.Edit{{Range: ext.Range{Start: p.Cursor, End: p.Cursor}, Text: "*"}}}
			return ext.CommandExecuteResult{OK: true, Transaction: &tx, Open: &ext.EditorOpenParams{Buffer: &id, Position: &to}}, nil
		})
		s.Command("fake.later", func(_ context.Context, b *server.Buffer, p *ext.CommandExecuteParams) (ext.CommandExecuteResult, error) {
			id, v, end := b.ID(), p.Version, ext.Position{Line: 0, Byte: len(b.Line(0))}
			go func() {
				_, _ = s.Edit(hostCtx, ext.BufferEditParams{Buffer: id, Transaction: ext.Transaction{
					BaseVersion: v, Edits: []ext.Edit{{Range: ext.Range{Start: end, End: end}, Text: "!"}},
				}})
			}()
			return ext.CommandExecuteResult{OK: true}, nil
		})
		// fake.empty、fake.lines、fake.endofline は、buffer/edit の noLines と endOfLine を試す。
		// fake.empty は全文を消して行の無いバッファにし、残りの 2 つは編集を持たずに状態だけを変える。
		lineState := func(id string, noLines, endOfLine *bool, erase bool) {
			s.Command(id, func(_ context.Context, b *server.Buffer, p *ext.CommandExecuteParams) (ext.CommandExecuteResult, error) {
				tx := ext.Transaction{BaseVersion: p.Version}
				if erase {
					last := b.LineCount() - 1
					tx.Edits = []ext.Edit{{Range: ext.Range{End: ext.Position{Line: last, Byte: len(b.Line(last))}}}}
				}
				go func() {
					r, err := s.Edit(hostCtx, ext.BufferEditParams{Buffer: b.ID(), Transaction: tx, NoLines: noLines, EndOfLine: endOfLine})
					_ = s.ShowMessage(ext.MessageInfo, fmt.Sprintf("%s applied=%v v%d err=%v", id, r.Applied, r.Version, err))
				}()
				return ext.CommandExecuteResult{OK: true}, nil
			})
		}
		yes, no := true, false
		lineState("fake.empty", &yes, nil, true)
		lineState("fake.lines", &no, nil, false)
		lineState("fake.endofline", nil, &yes, false)
		s.Command("fake.dump", func(_ context.Context, b *server.Buffer, _ *ext.CommandExecuteParams) (ext.CommandExecuteResult, error) {
			if b == nil {
				// 本体がバッファを送っていない（p.Withheld。基本設計 5.3）。
				return ext.CommandExecuteResult{OK: false}, nil
			}
			_ = s.ShowMessage(ext.MessageInfo, fmt.Sprintf("v%d:%q", b.Version(), b.Text()))
			return ext.CommandExecuteResult{OK: true}, nil
		})
		// fake.save は、カーソルのあるバッファを buffer/save（skipHooks）で保存させ、結果を表示する。
		s.Command("fake.save", func(_ context.Context, b *server.Buffer, _ *ext.CommandExecuteParams) (ext.CommandExecuteResult, error) {
			id := b.ID()
			go func() {
				r, err := s.SaveBuffer(hostCtx, ext.BufferSaveParams{Buffer: id, SkipHooks: true})
				_ = s.ShowMessage(ext.MessageInfo, fmt.Sprintf("saved=%v err=%v", r.Saved, err))
			}()
			return ext.CommandExecuteResult{OK: true}, nil
		})
		// fake.echo は、受け取った command/execute の args をそのまま表示する。
		s.Command("fake.echo", func(_ context.Context, _ *server.Buffer, p *ext.CommandExecuteParams) (ext.CommandExecuteResult, error) {
			_ = s.ShowMessage(ext.MessageInfo, fmt.Sprintf("args=%s", p.Args))
			return ext.CommandExecuteResult{OK: true}, nil
		})
		// fake.eol は、複製の FinalNewline（buffer/open の finalNewline）を表示する。
		s.Command("fake.eol", func(_ context.Context, b *server.Buffer, _ *ext.CommandExecuteParams) (ext.CommandExecuteResult, error) {
			_ = s.ShowMessage(ext.MessageInfo, fmt.Sprintf("b%d finalNewline=%v", b.ID(), b.FinalNewline()))
			return ext.CommandExecuteResult{OK: true}, nil
		})
		// fake.env は、本体から受け取った環境変数 ext.EnvMCPSocket の値を表示する。
		s.Command("fake.env", func(context.Context, *server.Buffer, *ext.CommandExecuteParams) (ext.CommandExecuteResult, error) {
			v, ok := os.LookupEnv(ext.EnvMCPSocket)
			_ = s.ShowMessage(ext.MessageInfo, fmt.Sprintf("mcp=%q set=%v", v, ok))
			return ext.CommandExecuteResult{OK: true}, nil
		})
		s.Command("fake.ask", func(_ context.Context, _ *server.Buffer, _ *ext.CommandExecuteParams) (ext.CommandExecuteResult, error) {
			go func() {
				r, err := s.AskChoice(hostCtx, ext.ChoiceAskParams{Title: "どれ?", Choices: []ext.Choice{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}}})
				_ = s.ShowMessage(ext.MessageInfo, fmt.Sprintf("selected=%s cancelled=%v err=%v", r.Selected, r.Cancelled, err))
			}()
			return ext.CommandExecuteResult{OK: true}, nil
		})
		// fake.ask3 は、choice/ask の問いを 3 つ続けて送り、それぞれの結果を出力の欄 ask に書き足す（#151）。
		// 3 つ目は 1 つ目と 2 つ目より後に届くように、少し待ってから送る。
		s.Command("fake.ask3", func(context.Context, *server.Buffer, *ext.CommandExecuteParams) (ext.CommandExecuteResult, error) {
			for i := range 3 {
				go func() {
					time.Sleep(time.Duration(i) * 30 * time.Millisecond)
					r, err := s.AskChoice(hostCtx, ext.ChoiceAskParams{Title: "どれ?", Choices: []ext.Choice{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}}})
					_ = s.AppendOutput("ask", fmt.Sprintf("selected=%s cancelled=%v err=%v\n", r.Selected, r.Cancelled, err))
				}()
			}
			return ext.CommandExecuteResult{OK: true}, nil
		})
		s.Command("fake.state", func(_ context.Context, _ *server.Buffer, _ *ext.CommandExecuteParams) (ext.CommandExecuteResult, error) {
			go func() {
				st, err := s.EditorState(hostCtx)
				_ = s.ShowMessage(ext.MessageInfo, fmt.Sprintf("mode=%s cursor=%d:%d buffers=%d err=%v", st.Mode, st.Cursor.Line, st.Cursor.Byte, len(st.Buffers), err))
			}()
			return ext.CommandExecuteResult{OK: true}, nil
		})
		// fake.focus は、少し待ってから、カーソルのない方のバッファを editor/open（focus）で開かせる。
		s.Command("fake.focus", func(context.Context, *server.Buffer, *ext.CommandExecuteParams) (ext.CommandExecuteResult, error) {
			go func() {
				time.Sleep(100 * time.Millisecond)
				st, err := s.EditorState(hostCtx)
				if err != nil {
					return
				}
				for _, b := range st.Buffers {
					if b.Buffer != st.Buffer {
						id := b.Buffer
						_, _ = s.OpenEditor(hostCtx, ext.EditorOpenParams{Buffer: &id, Focus: true})
						return
					}
				}
			}()
			return ext.CommandExecuteResult{OK: true}, nil
		})
		// fake.goto は、別のバッファ（複製を持つもののうち番号の最も小さいもの）の 2 行目へ移る open を、結果で返す。
		s.Command("fake.goto", func(_ context.Context, b *server.Buffer, _ *ext.CommandExecuteParams) (ext.CommandExecuteResult, error) {
			mu.Lock()
			defer mu.Unlock()
			var other ext.BufferID
			for id := range opened {
				if id != b.ID() && (other == 0 || id < other) {
					other = id
				}
			}
			if other == 0 {
				return ext.CommandExecuteResult{OK: false}, nil
			}
			return ext.CommandExecuteResult{OK: true, Open: &ext.EditorOpenParams{Buffer: &other, Position: &ext.Position{Line: 1}, Focus: true}}, nil
		})
		// fake.bufs は、届いた buffer/open と buffer/close の記録を、届いた順に表示する。
		s.Command("fake.bufs", func(context.Context, *server.Buffer, *ext.CommandExecuteParams) (ext.CommandExecuteResult, error) {
			mu.Lock()
			defer mu.Unlock()
			_ = s.ShowMessage(ext.MessageInfo, "bufs: "+strings.Join(bufLog, ", "))
			return ext.CommandExecuteResult{OK: true}, nil
		})
		s.OnBufferOpen(func(_ context.Context, ev *server.BufferOpenEvent) {
			mu.Lock()
			defer mu.Unlock()
			opened[ev.Buffer.ID()] = true
			bufLog = append(bufLog, fmt.Sprintf("open %d %s", ev.Buffer.ID(), filepath.Base(ev.Buffer.Path())))
		})
		s.OnBufferClose(func(_ context.Context, ev *server.BufferCloseEvent) {
			mu.Lock()
			defer mu.Unlock()
			delete(opened, ev.Buffer)
			bufLog = append(bufLog, fmt.Sprintf("close %d", ev.Buffer))
		})
		s.BufferWillSave(func(_ context.Context, b *server.Buffer, _ *ext.BufferWillSaveParams) (ext.BufferWillSaveResult, error) {
			for i := range b.LineCount() {
				if j := strings.Index(b.Line(i), "fmt"); j >= 0 {
					p := ext.Position{Line: i, Byte: j}
					return ext.BufferWillSaveResult{Transaction: &ext.Transaction{Edits: []ext.Edit{{
						Range: ext.Range{Start: p, End: ext.Position{Line: i, Byte: j + 3}}, Text: "FMT",
					}}}}, nil
				}
			}
			return ext.BufferWillSaveResult{}, nil
		})
	case fakeSlowInit:
		s.OnInitialize(func(context.Context, *server.InitializeEvent) error {
			time.Sleep(slowInitDelay)
			return nil
		})
	}
	if err := s.Run(hostCtx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// fakeSpecs は、テスト用の拡張機能の定義を返す。
func fakeSpecs(t *testing.T) []exthost.Spec {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	contrib := func(id, keys string) ext.Contribution { return ext.Contribution{ID: id, Keys: keys} }
	act := func(kind ext.ActivationKind, id string) ext.Activation { return ext.Activation{On: kind, ID: id} }
	ms := []ext.Manifest{
		{
			ID: "fake", Command: exe, Args: []string{fakeExtFlag, fakeMain}, SaveHook: true,
			Activation: []ext.Activation{
				act(ext.ActivationTextObject, "fake.line"), act(ext.ActivationTextObject, "fake.slow"),
				act(ext.ActivationTextObject, "fake.hang"), act(ext.ActivationCommand, "fake.upper"),
				act(ext.ActivationCommand, "fake.echo"),
			},
			Contributes: ext.Contributes{
				TextObjects: []ext.Contribution{contrib("fake.line", "il"), contrib("fake.slow", "is"), contrib("fake.hang", "ih")},
				Commands: []ext.Contribution{
					contrib("fake.upper", `\u`), contrib("fake.jump", `\j`), contrib("fake.later", `\l`),
					contrib("fake.dump", `\d`), contrib("fake.ask", `\a`), contrib("fake.state", `\s`),
					contrib("fake.focus", `\f`), contrib("fake.goto", `\g`), contrib("fake.eol", `\e`),
					contrib("fake.env", `\v`), contrib("fake.empty", `\z`), contrib("fake.lines", `\n`),
					contrib("fake.endofline", `\o`), contrib("fake.echo", ""), contrib("fake.save", `\w`),
					contrib("fake.bufs", `\b`), contrib("fake.ask3", `\3`),
				},
			},
		},
		{
			ID: "ftx", Command: exe, Args: []string{fakeExtFlag, fakeFileType},
			Activation: []ext.Activation{{On: ext.ActivationFileType, Pattern: "fakeft"}},
		},
		{
			ID: "slow", Command: exe, Args: []string{fakeExtFlag, fakeSlowInit},
			Activation: []ext.Activation{{On: ext.ActivationFileType, Pattern: "slowft"}},
		},
	}
	if _, err := ext.ValidateManifests(ms); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	specs := make([]exthost.Spec, len(ms))
	for i, m := range ms {
		specs[i] = exthost.Spec{Manifest: m, Dir: dir}
	}
	return specs
}

// fakeTerm は、テスト用の偽の端末である。キーを送り、最後に描いた画面を持つ。
type fakeTerm struct {
	in chan TermEvent

	mu   sync.Mutex
	last ui.Screen
}

func newFakeTerm() *fakeTerm { return &fakeTerm{in: make(chan TermEvent, 1024)} }

func (f *fakeTerm) Input() <-chan TermEvent { return f.in }

func (f *fakeTerm) Size() (int, int) { return 120, 6 }

func (f *fakeTerm) Draw(s ui.Screen) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.last = s
	return nil
}

func (f *fakeTerm) screen() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.last.Grid == nil {
		return ""
	}
	return f.last.Grid.String()
}

// harness は、偽の端末で動かしたエディタである。
type harness struct {
	t      *testing.T
	e      *Editor
	term   *fakeTerm
	done   chan error
	sent   int64
	cancel context.CancelFunc
}

// start は、cfg でエディタを作り、files を開いて、別の goroutine で Run する。テストの終わりに止めて Close する。
func start(t *testing.T, cfg Config, files ...string) *harness {
	t.Helper()
	if cfg.WorkDir == "" {
		cfg.WorkDir = t.TempDir()
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	term := newFakeTerm()
	e, err := New(term, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if err := e.Open(f); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	h := &harness{t: t, e: e, term: term, done: make(chan error, 1), cancel: cancel}
	go func() { h.done <- e.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-h.done:
			if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, ErrTerminalLost) {
				t.Errorf("Run = %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Run が終わらない")
		}
		closeCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := e.Close(closeCtx); err != nil {
			t.Errorf("Close = %v", err)
		}
	})
	return h
}

// keys は、Vim の表記のキーの列を端末から送る。
func (h *harness) keys(s string) {
	h.t.Helper()
	for _, k := range editcmd.MustParseKeys(s) {
		h.term.in <- TermEvent{Key: k}
		h.sent++
	}
}

// eventually は、cond がイベントループの goroutine で true を返すまで待つ。
func (h *harness) eventually(what string, cond func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var ok bool
		h.e.do(func() { ok = cond() })
		if ok {
			return
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("待ちきれない: %s（text=%q, message=%q）", what, h.text(), h.message())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// settle は、端末から送ったキーを、イベントループがすべて処理し終えるまで待つ。
func (h *harness) settle() {
	h.t.Helper()
	h.eventually("キーの処理", func() bool { return h.e.forwarded.Load() == h.sent && h.e.keys.empty() })
}

func (h *harness) text() string {
	var s string
	h.e.do(func() { s = h.e.cur.buf.Text() })
	return s
}

func (h *harness) message() string {
	var s string
	h.e.do(func() { s = h.e.message.Text })
	return s
}

func (h *harness) state(id string) exthost.State {
	st, _ := h.e.host.State(id)
	return st
}

func durationPtr(d time.Duration) *time.Duration { return &d }
