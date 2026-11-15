package exthost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/ext/server"
	"github.com/piplupflipper/modal-editor/internal/buffer"
	"github.com/piplupflipper/modal-editor/internal/rpc"
)

// ext/server で書いたテスト用の拡張機能（modeServer）と、Host を実際につないで、
// 標準の拡張機能のためのメッセージ（基本設計 5.3 の 2 つ目の表）の往復を確かめる。
//
// 拡張機能は、コマンドを受け付けると別の goroutine から本体へリクエストを送り、
// 受け取った結果を output/append で欄 outChannel へ書き出す。テストはその通知で結果を確かめる。

const outChannel = "test"

// サーバーの拡張機能のコマンド。Args は、コマンドごとに送るリクエストの params である。
const (
	cmdState  = "srv.state"  // editor/state を送る
	cmdOpen   = "srv.open"   // editor/open を送り、WaitBuffer で複製を待って、その全文を書き出す
	cmdSave   = "srv.save"   // buffer/save を送る
	cmdDiag   = "srv.diag"   // diagnostic/list を送る
	cmdChoice = "srv.choice" // choice/ask を送る
	cmdNotify = "srv.notify" // diagnostic/set、sign/set、popup/show を送る
	cmdSync   = "srv.sync"   // params の Sync が true なら、応答の Open でカーソルの移動を返す
	cmdArgs   = "srv.args"   // 起動したときの引数を書き出す
)

var serverCommands = []string{cmdState, cmdOpen, cmdSave, cmdDiag, cmdChoice, cmdNotify, cmdSync, cmdArgs}

func runServerExt() int {
	s := server.New()
	out := func(format string, args ...any) {
		_ = s.AppendOutput(outChannel, fmt.Sprintf(format, args...))
	}
	outJSON := func(name string, v any, err error) {
		if err != nil {
			out("%s error %v", name, err)
			return
		}
		data, _ := json.Marshal(v)
		out("%s %s", name, data)
	}
	// background は、コマンドを受け付けた後に、ループの外で本体へのリクエストを送る。
	background := func(id string, f func(ctx context.Context, args json.RawMessage)) {
		s.Command(id, func(_ context.Context, _ *server.Buffer, p *ext.CommandExecuteParams) (ext.CommandExecuteResult, error) {
			args := p.Args
			// ハンドラーの ctx はループの上の印を持ち（ハンドラーが戻る前に使うと ErrEditInHandler になる）、応答で取り消されるので使わない。
			go f(context.Background(), args) //nolint:gosec // 上の理由で、ハンドラーの ctx を引き継がない。
			return ext.CommandExecuteResult{OK: true}, nil
		})
	}
	background(cmdState, func(ctx context.Context, _ json.RawMessage) {
		res, err := s.EditorState(ctx)
		outJSON(cmdState, res, err)
	})
	background(cmdOpen, func(ctx context.Context, args json.RawMessage) {
		var p ext.EditorOpenParams
		_ = json.Unmarshal(args, &p)
		res, err := s.OpenEditor(ctx, p)
		if err != nil {
			out("%s error %v", cmdOpen, err)
			return
		}
		b, err := s.WaitBuffer(ctx, res.Buffer, res.Version)
		if err != nil {
			out("%s error %v", cmdOpen, err)
			return
		}
		out("%s %d %d %v %s %q", cmdOpen, res.Buffer, res.Version, res.Moved, b.Path(), b.Text())
	})
	background(cmdSave, func(ctx context.Context, args json.RawMessage) {
		var p ext.BufferSaveParams
		_ = json.Unmarshal(args, &p)
		res, err := s.SaveBuffer(ctx, p)
		outJSON(cmdSave, res, err)
	})
	background(cmdDiag, func(ctx context.Context, args json.RawMessage) {
		var p ext.DiagnosticListParams
		_ = json.Unmarshal(args, &p)
		res, err := s.ListDiagnostics(ctx, p)
		outJSON(cmdDiag, res, err)
	})
	background(cmdChoice, func(ctx context.Context, args json.RawMessage) {
		var p ext.ChoiceAskParams
		_ = json.Unmarshal(args, &p)
		res, err := s.AskChoice(ctx, p)
		outJSON(cmdChoice, res, err)
	})
	s.Command(cmdNotify, func(_ context.Context, b *server.Buffer, p *ext.CommandExecuteParams) (ext.CommandExecuteResult, error) {
		_ = s.SetDiagnostics(ext.DiagnosticSetParams{Buffer: b.ID(), Version: p.Version, Diagnostics: []ext.Diagnostic{
			{Range: ext.Range{End: ext.Position{Byte: 1}}, Severity: ext.SeverityError, Message: "bad", Source: "srv"},
		}})
		_ = s.SetSigns(ext.SignSetParams{Buffer: b.ID(), Version: p.Version, Signs: []ext.Sign{{Line: 0, Kind: ext.SignBreakpoint}}})
		_ = s.ShowPopup(ext.PopupShowParams{Buffer: b.ID(), Version: p.Version, Position: p.Cursor, Text: "*doc*", Format: ext.PopupMarkdown})
		return ext.CommandExecuteResult{OK: true}, nil
	})
	s.Command(cmdSync, func(_ context.Context, b *server.Buffer, p *ext.CommandExecuteParams) (ext.CommandExecuteResult, error) {
		if !p.Sync {
			return ext.CommandExecuteResult{OK: true}, nil
		}
		id := b.ID()
		end := ext.Position{Line: b.LineCount() - 1}
		return ext.CommandExecuteResult{OK: true, Open: &ext.EditorOpenParams{Buffer: &id, Position: &end, Focus: true}}, nil
	})
	s.Command(cmdArgs, func(context.Context, *server.Buffer, *ext.CommandExecuteParams) (ext.CommandExecuteResult, error) {
		out("%s %s", cmdArgs, strings.Join(os.Args[1:], "|"))
		return ext.CommandExecuteResult{OK: true}, nil
	})
	// 保存の前に、先頭へ "// " を足す。
	s.BufferWillSave(func(_ context.Context, b *server.Buffer, _ *ext.BufferWillSaveParams) (ext.BufferWillSaveResult, error) {
		if strings.HasPrefix(b.Line(0), "// ") {
			return ext.BufferWillSaveResult{}, nil
		}
		return ext.BufferWillSaveResult{Transaction: &ext.Transaction{Edits: []ext.Edit{{Text: "// "}}}}, nil
	})
	s.OnBufferDidSave(func(_ context.Context, e *server.BufferDidSaveEvent) {
		out("didSave %d %d %s %q", e.Params.Buffer, e.Params.Version, e.Params.Path, e.Buffer.Text())
	})
	if err := s.Run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// serverSpec は、サーバーの拡張機能の定義を作る。
func serverSpec(t *testing.T, id string, args []string) Spec {
	t.Helper()
	m := ext.Manifest{
		ID:         id,
		Command:    fakeCommand(t, modeServer),
		Args:       args,
		SaveHook:   true,
		Activation: []ext.Activation{{On: ext.ActivationCommand, ID: cmdState}},
	}
	for _, c := range serverCommands {
		m.Contributes.Commands = append(m.Contributes.Commands, ext.Contribution{ID: c})
	}
	if _, err := ext.ValidateManifests([]ext.Manifest{m}); err != nil {
		t.Fatal(err)
	}
	return Spec{Manifest: m}
}

// waitOutput は、拡張機能 srv から欄 outChannel へ、prefix で始まるテキストが書き足されるのを待って返す。
func (hs *harness) waitOutput(prefix string) string {
	hs.t.Helper()
	ev := hs.waitEvent("output "+prefix, func(ev Event) bool {
		o, ok := ev.(*OutputAppend)
		return ok && o.Extension == "srv" && o.Params.Channel == outChannel && strings.HasPrefix(o.Params.Text, prefix+" ")
	})
	return strings.TrimPrefix(ev.(*OutputAppend).Params.Text, prefix+" ")
}

// execute は、拡張機能 srv のバッファ 1（バージョン番号 1）でコマンド id を実行する。
func (hs *harness) execute(id string, args any, sync bool) ext.CommandExecuteResult {
	hs.t.Helper()
	var raw json.RawMessage
	if args != nil {
		raw, _ = json.Marshal(args)
	}
	res, err := hs.h.ExecuteCommand(ctx, "srv", ext.CommandExecuteParams{Buffer: 1, Version: 1, ID: id, Args: raw, Sync: sync}, callTimeout)
	if err != nil || !res.OK {
		hs.t.Fatalf("command %s = %+v, %v", id, res, err)
	}
	return res
}

func decodeOutput[T any](t *testing.T, text string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("output %q: %v", text, err)
	}
	return v
}

// 拡張機能から本体へのリクエストは、応答の口つきのイベントになり、イベントループの応答が拡張機能へ届く。
func TestStdExtRequestsRoundTrip(t *testing.T) {
	hs := newHarness(t, []Spec{serverSpec(t, "srv", nil)}, nil, true)
	if err := hs.h.OpenBuffer(BufferSnapshot{ID: 1, Path: "/w/a.go", FileType: "go", Version: 1, Text: "x\ny"}); err != nil {
		t.Fatal(err)
	}

	t.Run("editor/state", func(t *testing.T) {
		hs.execute(cmdState, nil, false)
		req := hs.waitEvent("editor/state", func(ev Event) bool { _, ok := ev.(*EditorStateRequest); return ok }).(*EditorStateRequest)
		want := ext.EditorStateResult{
			Buffer: 1, Cursor: ext.Position{Line: 1}, Mode: ext.ModeVisualLine,
			Selections: []ext.Range{{End: ext.Position{Line: 1, Byte: 1}}},
			Buffers:    []ext.BufferInfo{{Buffer: 1, Path: "/w/a.go", FileType: "go", Version: 1, Modified: true}},
		}
		if req.Extension != "srv" {
			t.Errorf("Extension = %q", req.Extension)
		}
		if err := req.Reply(want); err != nil {
			t.Fatal(err)
		}
		if got := decodeOutput[ext.EditorStateResult](t, hs.waitOutput(cmdState)); !reflect.DeepEqual(got, want) {
			t.Errorf("extension received %+v, want %+v", got, want)
		}
	})

	// editor/open の応答より先に OpenBuffer の buffer/open が届き、拡張機能は WaitBuffer で複製を読める（基本設計 6.7）。
	t.Run("editor/open", func(t *testing.T) {
		pos := ext.Position{Line: 0, Byte: 2}
		hs.execute(cmdOpen, ext.EditorOpenParams{Path: "/w/b.go", Position: &pos, Focus: true}, false)
		req := hs.waitEvent("editor/open", func(ev Event) bool { _, ok := ev.(*EditorOpenRequest); return ok }).(*EditorOpenRequest)
		if req.Params.Path != "/w/b.go" || req.Params.Position == nil || *req.Params.Position != pos || !req.Params.Focus {
			t.Errorf("Params = %+v", req.Params)
		}
		if err := hs.h.OpenBuffer(BufferSnapshot{ID: 2, Path: "/w/b.go", Version: 5, Text: "package b"}); err != nil {
			t.Fatal(err)
		}
		if err := req.Reply(ext.EditorOpenResult{Buffer: 2, Version: 5, Moved: true}); err != nil {
			t.Fatal(err)
		}
		if got, want := hs.waitOutput(cmdOpen), `2 5 true /w/b.go "package b"`; got != want {
			t.Errorf("extension output %q, want %q", got, want)
		}
	})

	t.Run("editor/open error", func(t *testing.T) {
		buf := ext.BufferID(9)
		hs.execute(cmdOpen, ext.EditorOpenParams{Buffer: &buf}, false)
		req := hs.waitEvent("editor/open", func(ev Event) bool { _, ok := ev.(*EditorOpenRequest); return ok }).(*EditorOpenRequest)
		if err := req.ReplyError(errors.New("no such buffer")); err != nil {
			t.Fatal(err)
		}
		if got := hs.waitOutput(cmdOpen); !strings.Contains(got, "no such buffer") {
			t.Errorf("extension output %q", got)
		}
	})

	t.Run("buffer/save", func(t *testing.T) {
		v := ext.Version(1)
		hs.execute(cmdSave, ext.BufferSaveParams{Buffer: 1, Version: &v, SkipHooks: true}, false)
		req := hs.waitEvent("buffer/save", func(ev Event) bool { _, ok := ev.(*BufferSaveRequest); return ok }).(*BufferSaveRequest)
		if req.Params.Buffer != 1 || req.Params.Version == nil || *req.Params.Version != 1 || !req.Params.SkipHooks {
			t.Errorf("Params = %+v", req.Params)
		}
		if err := req.Reply(ext.BufferSaveResult{Saved: true, Version: 1}); err != nil {
			t.Fatal(err)
		}
		if got := decodeOutput[ext.BufferSaveResult](t, hs.waitOutput(cmdSave)); got != (ext.BufferSaveResult{Saved: true, Version: 1}) {
			t.Errorf("extension received %+v", got)
		}
	})

	t.Run("diagnostic/list", func(t *testing.T) {
		buf := ext.BufferID(1)
		hs.execute(cmdDiag, ext.DiagnosticListParams{Buffer: &buf}, false)
		req := hs.waitEvent("diagnostic/list", func(ev Event) bool { _, ok := ev.(*DiagnosticListRequest); return ok }).(*DiagnosticListRequest)
		if req.Params.Buffer == nil || *req.Params.Buffer != 1 {
			t.Errorf("Params = %+v", req.Params)
		}
		want := ext.DiagnosticListResult{Entries: []ext.DiagnosticEntry{{
			Buffer: 1, Extension: "lsp", Version: 1,
			Diagnostics: []ext.Diagnostic{{Severity: ext.SeverityWarning, Message: "w"}},
		}}}
		if err := req.Reply(want); err != nil {
			t.Fatal(err)
		}
		if got := decodeOutput[ext.DiagnosticListResult](t, hs.waitOutput(cmdDiag)); !reflect.DeepEqual(got, want) {
			t.Errorf("extension received %+v", got)
		}
	})

	t.Run("choice/ask", func(t *testing.T) {
		hs.execute(cmdChoice, ext.ChoiceAskParams{Title: "書き込むか", Choices: []ext.Choice{{ID: "y", Label: "はい"}, {ID: "n", Label: "いいえ"}}}, false)
		req := hs.waitEvent("choice/ask", func(ev Event) bool { _, ok := ev.(*ChoiceAskRequest); return ok }).(*ChoiceAskRequest)
		if req.Params.Title != "書き込むか" || len(req.Params.Choices) != 2 {
			t.Errorf("Params = %+v", req.Params)
		}
		if err := req.Reply(ext.ChoiceAskResult{Cancelled: true}); err != nil {
			t.Fatal(err)
		}
		if got := decodeOutput[ext.ChoiceAskResult](t, hs.waitOutput(cmdChoice)); got != (ext.ChoiceAskResult{Cancelled: true}) {
			t.Errorf("extension received %+v", got)
		}
	})
}

// 拡張機能からの表示の通知は、それぞれのイベントになる。
func TestStdExtNotificationsRoundTrip(t *testing.T) {
	hs := newHarness(t, []Spec{serverSpec(t, "srv", nil)}, nil, true)
	if err := hs.h.OpenBuffer(BufferSnapshot{ID: 1, Version: 3, Text: "abc"}); err != nil {
		t.Fatal(err)
	}
	cursor := ext.Position{Byte: 2}
	if _, err := hs.h.ExecuteCommand(ctx, "srv", ext.CommandExecuteParams{Buffer: 1, Version: 3, ID: cmdNotify, Cursor: cursor}, callTimeout); err != nil {
		t.Fatal(err)
	}
	ds := hs.waitEvent("diagnostic/set", func(ev Event) bool { _, ok := ev.(*DiagnosticSet); return ok }).(*DiagnosticSet)
	if ds.Extension != "srv" || ds.Params.Version != 3 || len(ds.Params.Diagnostics) != 1 || ds.Params.Diagnostics[0].Message != "bad" {
		t.Errorf("DiagnosticSet = %+v", ds)
	}
	ss := hs.waitEvent("sign/set", func(ev Event) bool { _, ok := ev.(*SignSet); return ok }).(*SignSet)
	if !reflect.DeepEqual(ss.Params, ext.SignSetParams{Buffer: 1, Version: 3, Signs: []ext.Sign{{Line: 0, Kind: ext.SignBreakpoint}}}) {
		t.Errorf("SignSet = %+v", ss)
	}
	ps := hs.waitEvent("popup/show", func(ev Event) bool { _, ok := ev.(*PopupShow); return ok }).(*PopupShow)
	if ps.Params != (ext.PopupShowParams{Buffer: 1, Version: 3, Position: cursor, Text: "*doc*", Format: ext.PopupMarkdown}) {
		t.Errorf("PopupShow = %+v", ps)
	}
}

// 保存の流れ（基本設計 6.7）: saveHook の拡張機能へ buffer/willSave を問い合わせ、編集を適用した後に buffer/didSave を送る。
// 未起動の拡張機能へは送らず、起動もしない。
func TestStdExtSaveFlow(t *testing.T) {
	idle := serverSpec(t, "idle", nil)
	hs := newHarness(t, []Spec{serverSpec(t, "srv", nil), idle}, nil, true)
	if err := hs.h.OpenBuffer(BufferSnapshot{ID: 1, Path: "/w/a.go", Version: 1, Text: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := hs.h.Start("srv"); err != nil {
		t.Fatal(err)
	}
	hs.waitState("srv", StateRunning)

	if got := hs.h.SaveHookExtensions(); !slices.Equal(got, []string{"srv"}) {
		t.Errorf("SaveHookExtensions = %q, want [srv]", got)
	}
	if _, err := hs.h.SendWillSave("idle", ext.BufferWillSaveParams{Buffer: 1, Version: 1}, SendOptions{}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("SendWillSave to a not started extension = %v, want ErrUnavailable", err)
	}
	if _, err := hs.h.WillSave(ctx, "idle", ext.BufferWillSaveParams{Buffer: 1, Version: 1}, 0); !errors.Is(err, ErrUnavailable) {
		t.Errorf("WillSave to a not started extension = %v, want ErrUnavailable", err)
	}
	if st := hs.state("idle"); st != StateNotStarted {
		t.Errorf("idle state = %s, want not-started", st)
	}
	if _, err := hs.h.SendWillSave("srv", ext.BufferWillSaveParams{Buffer: 7, Version: 1}, SendOptions{}); !errors.Is(err, ErrBufferState) {
		t.Errorf("SendWillSave for a closed buffer = %v, want ErrBufferState", err)
	}

	// 1. buffer/willSave の結果をイベントで受け取る。
	if _, err := hs.h.SendWillSave("srv", ext.BufferWillSaveParams{Buffer: 1, Version: 1}, SendOptions{Tag: "save"}); err != nil {
		t.Fatal(err)
	}
	qr := hs.waitEvent("willSave result", func(ev Event) bool {
		q, ok := ev.(*QueryResult)
		return ok && q.Tag == "save"
	}).(*QueryResult)
	var res ext.BufferWillSaveResult
	if err := qr.Decode(&res); err != nil {
		t.Fatal(err)
	}
	want := ext.Transaction{BaseVersion: 1, Edits: []ext.Edit{{Text: "// "}}}
	if qr.Method != ext.MethodBufferWillSave || res.Transaction == nil || !reflect.DeepEqual(*res.Transaction, want) {
		t.Fatalf("willSave = %s %+v", qr.Method, res)
	}

	// 2. 編集を適用したことを知らせ、次の問い合わせは適用した後のバージョン番号で行う。
	if err := hs.h.ChangeBuffer(1, changeOf(1, 2, res.Transaction.Edits)); err != nil {
		t.Fatal(err)
	}
	res, err := hs.h.WillSave(ctx, "srv", ext.BufferWillSaveParams{Buffer: 1, Version: 2}, callTimeout)
	if err != nil || res.Transaction != nil {
		t.Errorf("WillSave after formatting = %+v, %v", res, err)
	}

	// 4. 書き出した後の buffer/didSave は、書き出した時点の複製と一緒にフックへ届く。
	if err := hs.h.DidSave(ext.BufferDidSaveParams{Buffer: 1, Version: 1, Path: "/w/a.go"}); !errors.Is(err, ErrBufferState) {
		t.Errorf("DidSave with an old version = %v, want ErrBufferState", err)
	}
	if err := hs.h.DidSave(ext.BufferDidSaveParams{Buffer: 1, Version: 2, Path: "/w/a.go"}); err != nil {
		t.Fatal(err)
	}
	if got, want := hs.waitOutput("didSave"), `1 2 /w/a.go "// x"`; got != want {
		t.Errorf("didSave output %q, want %q", got, want)
	}
}

// マクロとドットリピートの再生中の command/execute（sync が true）は、カーソルの移動を応答の open で返せる。
func TestStdExtCommandSync(t *testing.T) {
	hs := newHarness(t, []Spec{serverSpec(t, "srv", nil)}, nil, true)
	if err := hs.h.OpenBuffer(BufferSnapshot{ID: 1, Version: 1, Text: "a\nb\nc"}); err != nil {
		t.Fatal(err)
	}
	if res := hs.execute(cmdSync, nil, false); res.Open != nil {
		t.Errorf("Open without sync = %+v", res.Open)
	}
	res := hs.execute(cmdSync, nil, true)
	if res.Open == nil || res.Open.Buffer == nil || *res.Open.Buffer != 1 || res.Open.Position == nil || *res.Open.Position != (ext.Position{Line: 2}) {
		t.Errorf("Open with sync = %+v", res.Open)
	}
}

// 宣言ファイルの args は、拡張機能の実行ファイルの引数になる。
func TestStdExtArgs(t *testing.T) {
	hs := newHarness(t, []Spec{serverSpec(t, "srv", []string{"--stdio", "a b"})}, nil, true)
	if err := hs.h.OpenBuffer(BufferSnapshot{ID: 1, Version: 1}); err != nil {
		t.Fatal(err)
	}
	hs.execute(cmdArgs, nil, false)
	if got := hs.waitOutput(cmdArgs); got != "--stdio|a b" {
		t.Errorf("args = %q", got)
	}
}

// 誤りのある params のリクエストは、イベントにせずに誤りを応答する。
func TestRequestEventInvalidParams(t *testing.T) {
	tests := []struct {
		method, params string
		code           int64
	}{
		{ext.MethodEditorOpen, `{"path":"relative"}`, rpc.CodeInvalidParams},
		{ext.MethodEditorOpen, `{}`, rpc.CodeInvalidParams},
		{ext.MethodBufferSave, `{"buffer":"x"}`, rpc.CodeInvalidParams},
		{"editor/unknown", `{}`, rpc.CodeMethodNotFound},
	}
	for _, tt := range tests {
		ev, err := requestEvent("x", &rpc.Request{Method: tt.method, Params: json.RawMessage(tt.params)}, nil)
		if ev != nil || err == nil || err.Code != tt.code {
			t.Errorf("%s %s = %v, %v, want code %d", tt.method, tt.params, ev, err, tt.code)
		}
	}
	// editor/state は params が無くてもよい。
	if ev, err := requestEvent("x", &rpc.Request{Method: ext.MethodEditorState}, nil); err != nil || ev == nil {
		t.Errorf("editor/state without params = %v, %v", ev, err)
	}
}

// changeOf は、拡張機能の編集の列を、バージョン番号 before から after への buffer.Change にする。
func changeOf(before, after uint64, edits []ext.Edit) buffer.Change {
	c := buffer.Change{Before: before, After: after}
	for _, e := range edits {
		c.Edits = append(c.Edits, buffer.Edit{
			Range: buffer.Range{
				Start: buffer.Position{Line: e.Range.Start.Line, Byte: e.Range.Start.Byte},
				End:   buffer.Position{Line: e.Range.End.Line, Byte: e.Range.End.Byte},
			},
			Text: e.Text,
		})
	}
	return c
}
