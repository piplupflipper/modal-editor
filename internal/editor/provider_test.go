package editor

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/ext/server"
	"github.com/piplupflipper/modal-editor/internal/exthost"
)

// hoverText は、fakeProvider の hover が popup/show で出す本文である。ポップアップの中で j を打てるように 2 行にする。
const hoverText = "hover-popup\nsecond"

// stepDelay は、fakeProvider の debug.stepOver が止まるまでの時間である。
const stepDelay = 150 * time.Millisecond

// registerFakeProvider は、本体の操作を引き受ける拡張機能（fakeProvider）のハンドラーを登録する。
// どの操作も、受け取った params を message/show で表示する。format は範囲（無ければ 1 行目）を大文字にする編集を返し、
// definition は peek でなければ次の行へ移る open を返す。
func registerFakeProvider(s *server.Server) {
	show := func(p *ext.ProviderInvokeParams) {
		rg := "-"
		if p.Range != nil {
			rg = fmt.Sprintf("%d:%d-%d:%d", p.Range.Start.Line, p.Range.Start.Byte, p.Range.End.Line, p.Range.End.Byte)
		}
		args := ""
		if len(p.Args) > 0 && string(p.Args) != "null" {
			args = string(p.Args)
		}
		_ = s.ShowMessage(ext.MessageInfo, fmt.Sprintf("%s %s cursor=%d:%d range=%s args=%s withheld=%v",
			os.Getenv("FAKE_PROVIDER_ID"), p.Operation, p.Cursor.Line, p.Cursor.Byte, rg, args, p.Withheld))
	}
	echo := func(_ context.Context, _ *server.Buffer, p *ext.ProviderInvokeParams) (ext.ProviderInvokeResult, error) {
		show(p)
		return ext.ProviderInvokeResult{OK: true}, nil
	}
	for _, op := range []ext.Operation{ext.OpRename, ext.OpDebugContinue, ext.OpBreakpointToggle} {
		s.Provider(op, echo)
	}
	// hover は、LSP の拡張機能と同じく、本文を問い合わせた位置の popup/show で出す。
	s.Provider(ext.OpHover, func(_ context.Context, _ *server.Buffer, p *ext.ProviderInvokeParams) (ext.ProviderInvokeResult, error) {
		show(p)
		_ = s.ShowPopup(ext.PopupShowParams{Buffer: p.Buffer, Version: p.Version, Position: p.Cursor, Text: hoverText, Format: ext.PopupPlainText})
		return ext.ProviderInvokeResult{OK: true}, nil
	})
	// debug.stepOver は、デバッガーのステップ実行の代役である。stepDelay の後に、カーソルの次の行で止まったことにする。
	// 再生中でなければ受け付けたことだけを応答し、止まったら editor/open で移す。再生中（sync）なら、止まってから位置を応答の open で返す。
	// stepDelay の間に次の stepOver が届いたら、動いている間なので失敗する（delve と同じ。#210）。
	var running atomic.Bool
	s.ProviderAsync(ext.OpDebugStepOver, func(_ context.Context, b *server.Buffer, p *ext.ProviderInvokeParams, reply func(ext.ProviderInvokeResult, error)) {
		if !running.CompareAndSwap(false, true) {
			_ = s.ShowMessage(ext.MessageError, "debuggee is running")
			reply(ext.ProviderInvokeResult{OK: false}, nil)
			return
		}
		id := b.ID()
		to := ext.Position{Line: min(p.Cursor.Line+1, b.LineCount()-1)}
		open := ext.EditorOpenParams{Buffer: &id, Position: &to, Focus: true}
		if !p.Sync {
			reply(ext.ProviderInvokeResult{OK: true}, nil)
		}
		go func() { //nolint:gosec // G118: 再生中でなければ応答した後に editor/open を送るので、ハンドラーの ctx は使わない
			time.Sleep(stepDelay)
			running.Store(false)
			if p.Sync {
				reply(ext.ProviderInvokeResult{OK: true, Open: &open}, nil)
				return
			}
			_, _ = s.OpenEditor(context.Background(), open)
		}()
	})
	s.Provider(ext.OpFormat, func(_ context.Context, b *server.Buffer, p *ext.ProviderInvokeParams) (ext.ProviderInvokeResult, error) {
		show(p)
		rg := lineRange(b, 0)
		if p.Range != nil {
			rg = *p.Range
		}
		text, err := b.Slice(rg)
		if err != nil {
			return ext.ProviderInvokeResult{}, err
		}
		return ext.ProviderInvokeResult{OK: true, Transaction: &ext.Transaction{Edits: []ext.Edit{{Range: rg, Text: strings.ToUpper(text)}}}}, nil
	})
	s.Provider(ext.OpDefinition, func(_ context.Context, b *server.Buffer, p *ext.ProviderInvokeParams) (ext.ProviderInvokeResult, error) {
		show(p)
		id := b.ID()
		to := ext.Position{Line: min(p.Cursor.Line+1, b.LineCount()-1)}
		// peek なら、移らずに移動先の写しをポップアップで見る（editor/open の peek。FR-110）。
		peek := strings.Contains(string(p.Args), `"peek":true`)
		return ext.ProviderInvokeResult{OK: true, Open: &ext.EditorOpenParams{Buffer: &id, Position: &to, Peek: peek}}, nil
	})
}

// providerSpecs は、ファイルの種類 provft の提供者 lspx と、ファイルの種類を問わない提供者 dapx の定義を返す。
// どちらも activation を持たず、provides だけで起動する。
func providerSpecs(t *testing.T) []exthost.Spec {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ms := []ext.Manifest{
		{
			ID: "lspx", Command: "/bin/sh", Args: []string{"-c", `FAKE_PROVIDER_ID=lspx exec "$0" "$@"`, exe, fakeExtFlag, fakeProvider},
			Activation: []ext.Activation{{On: ext.ActivationFileType, Pattern: "provft"}},
			Provides:   []ext.Operation{ext.OpHover, ext.OpFormat, ext.OpDefinition, ext.OpRename},
		},
		{
			ID: "dapx", Command: "/bin/sh", Args: []string{"-c", `FAKE_PROVIDER_ID=dapx exec "$0" "$@"`, exe, fakeExtFlag, fakeProvider},
			Provides: []ext.Operation{ext.OpDebugContinue, ext.OpHover, ext.OpBreakpointToggle, ext.OpDebugStepOver},
		},
	}
	if _, err := ext.ValidateManifests(ms); err != nil {
		t.Fatal(err)
	}
	specs := make([]exthost.Spec, len(ms))
	for i, m := range ms {
		specs[i] = exthost.Spec{Manifest: m, Dir: t.TempDir()}
	}
	return specs
}

// 既定のキーで呼んだ LSP と DAP の操作は、バッファのファイルの種類で選んだ提供者へ provider/invoke で問い合わせる。
// 結果の編集とカーソルの移動は command/execute と同じに扱う。提供者が無ければ、そのことをメッセージの行に出す。
//
// req: FR-06, FR-94, FR-95, FR-96
func TestProviderKeys(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.provft": "abc\ndef\nghi\n", "b.txt": "xyz\n"})
	h := start(t, Config{WorkDir: dir, Extensions: providerSpecs(t), FileTypes: map[string]string{".provft": "provft"}}, "a.provft")
	waitMsg := func(what, want string) {
		t.Helper()
		h.eventually(what, func() bool { return h.e.message.Text == want })
	}

	h.keys("l<Space>lh")
	waitMsg("<Space>lh", "lspx hover cursor=0:1 range=- args= withheld=false")
	h.keys("K")
	waitMsg("K", "lspx hover cursor=0:1 range=- args= withheld=false")
	// 対象を取らない提供者の操作は、テキストを変えないのでドットリピートが記録しない（FR-06）。
	// xK. と x<Space>db. の . は、直前の変更の x を繰り返す（#203 のレビュー）。
	for _, keys := range []string{"K", "<Space>db"} {
		h.keys("x" + keys)
		h.settle()
		h.e.do(func() { h.e.message.Text = "" })
		h.keys(".")
		h.settle()
		if got := h.text(); got != "a\ndef\nghi" {
			t.Errorf("x%s. の後: text = %q", keys, got)
		}
		h.keys("uu0l")
		h.settle()
		if got := h.text(); got != "abc\ndef\nghi" {
			t.Fatalf("x%s.uu の後: text = %q", keys, got)
		}
	}

	// <C-]> と <Space>lD は定義へ移り、<Space>ld は peek を付けて問い合わせ、移らない。
	h.keys("<C-]>")
	h.eventually("<C-]> の移動", func() bool { return h.e.cur.state.Cursor().Line == 1 })
	h.keys("<Space>ld")
	waitMsg("<Space>ld", `lspx definition cursor=1:0 range=- args={"peek":true} withheld=false`)
	if l := h.e.cur.state.Cursor().Line; l != 1 {
		t.Errorf("<Space>ld の後の行 = %d, want 1", l)
	}

	// gq はオペレーターで、範囲を渡す。返った編集を適用し、u で戻せる。
	h.keys("gqj")
	h.eventually("gqj の整形", func() bool { return h.e.cur.buf.Text() == "abc\nDEF\nGHI" })
	if msg := h.message(); !strings.HasPrefix(msg, "lspx format cursor=1:0 range=1:0-2:3") {
		t.Errorf("gqj: message = %q", msg)
	}
	h.keys("u")
	h.settle()
	if got := h.text(); got != "abc\ndef\nghi" {
		t.Errorf("u: text = %q", got)
	}
	// gq はオペレーターなので、Vim の gq と同じくドットリピートで繰り返し、format を問い合わせ直す。
	h.e.do(func() { h.e.message.Text = "" })
	h.keys(".")
	h.eventually("gqj の後の .", func() bool { return h.e.cur.buf.Text() == "abc\nDEF\nGHI" })
	if msg := h.message(); !strings.HasPrefix(msg, "lspx format cursor=1:0 range=1:0-2:3") {
		t.Errorf("gqj の後の .: message = %q", msg)
	}
	h.keys("u")
	h.settle()

	// 提供者の操作は、コマンドラインから操作の名前で呼べ、引数を args の text で渡す。
	h.keys(":rename newName<CR>")
	waitMsg(":rename", `lspx rename cursor=1:0 range=- args={"text":"newName"} withheld=false`)

	// ファイルの種類の契機を持たない dapx は、どのバッファでも提供者になる。
	h.keys("<Space>dc")
	waitMsg("<Space>dc", "dapx debug.continue cursor=1:0 range=- args= withheld=false")

	// b.txt のファイルの種類では lspx は候補にならず、hover は dapx が引き受ける。format の提供者は無い。
	h.keys(":e b.txt<CR>")
	h.settle()
	h.keys("<Space>lh")
	waitMsg("b.txt の hover", "dapx hover cursor=0:0 range=- args= withheld=false")
	h.keys("gqq")
	h.settle()
	if msg := h.message(); !strings.Contains(msg, "format の提供者がありません") {
		t.Errorf("提供者の無い gqq: message = %q", msg)
	}
	if got := h.text(); got != "xyz" {
		t.Errorf("提供者の無い gqq: text = %q", got)
	}
}

// マクロの再生の中の提供者の操作は、前の操作の完了を待ってから次へ進む（#210）。
// <Space>dn を 2 回記録したマクロの再生は、2 回とも止まるのを待ってから進み、記録したときと同じく 2 行進む。
// 再生の途中のホバーのポップアップは、記録したときと同じく次の移動で消えたものとして扱い、再生の後に残さない。
// 最後の操作のホバーは、再生の後に出す。
//
// req: FR-19, FR-94
func TestReplayProviderWaits(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.provft": "l0\nl1\nl2\nl3\nl4\nl5\n"})
	h := start(t, Config{WorkDir: dir, Extensions: providerSpecs(t), FileTypes: map[string]string{".provft": "provft"}}, "a.provft")
	// lineOf と popupOf はイベントループの goroutine で呼ぶ（eventually の cond の中か do の中）。
	lineOf := func() int { return h.e.cur.state.Cursor().Line }
	popupOf := func() string {
		h.e.expireFloat() // 描く前と同じく、閉じる条件を確かめる
		return popupText(h.e)
	}
	line := func() int {
		var l int
		h.e.do(func() { l = lineOf() })
		return l
	}
	popup := func() string {
		var s string
		h.e.do(func() { s = popupOf() })
		return s
	}

	// 記録するときは、止まって移るのを待ってから次のキーを打つ。
	h.keys("qa<Space>dn")
	h.eventually("1 回目の停止", func() bool { return lineOf() == 1 })
	h.keys("<Space>dn")
	h.eventually("2 回目の停止", func() bool { return lineOf() == 2 })
	h.keys("q")
	h.settle()

	// 再生は、2 回目のステップ実行を 1 回目の停止の後に送る。再生の直後に打ったキー（x）は、再生が済んでから処理する。
	h.keys("gg@ax")
	h.eventually("再生の後の x", func() bool { return h.e.cur.buf.Text() == "l0\nl1\n2\nl3\nl4\nl5" })
	if l := line(); l != 2 {
		t.Errorf("再生の後の行 = %d, want 2", l)
	}
	if msg := h.message(); strings.Contains(msg, "running") {
		t.Errorf("再生の中のステップ実行が失敗した: %q", msg)
	}
	h.keys("u")
	h.settle()

	// 途中のホバーは再生の後に残らない。
	h.keys("ggqbK")
	h.eventually("記録のときのホバー", func() bool { return popupOf() == hoverText })
	h.keys("jq")
	h.settle()
	h.keys("gg@b")
	h.settle()
	if l, p := line(), popup(); l != 1 || p != "" {
		t.Errorf("@b の後: line = %d, popup = %q, want 1 and no popup", l, p)
	}

	// 最後のホバーは再生の後に出す。
	h.keys("qcKq")
	h.settle()
	h.keys("jk@c")
	h.eventually("@c のホバー", func() bool { return popupOf() == hoverText })

	// 移動しないキー（zz）や、動いて戻るキー（jk）が続いても、記録したときと同じく再生の後に残らない（#217 のレビュー）。
	// 拡張機能の操作（<Space>db）が続いても残らない（#224 のコメント）。
	for _, rec := range []string{"qeKzzq", "qfKjkq", "qgK<Space>dbq"} {
		reg := rec[1:2]
		h.keys("j" + rec)
		h.settle()
		h.keys("jk@" + reg)
		h.settle()
		if p := popup(); p != "" {
			t.Errorf("%s の後の @%s: popup = %q, want none", rec, reg, p)
		}
	}
}

// キーマップの右辺の段階 2 の LSP と DAP のコマンドは、起動の時点で設定の誤りとして知らせる（#203 のレビュー）。
// 拡張機能が同じ ID のコマンドをまだ宣言していれば知らせない。
func TestRemovedCommandWarnings(t *testing.T) {
	km := map[string]map[string]string{"normal": {
		"<Space>d": ":lsp.definition<CR>", "<Space>b": ":dap.toggleBreakpoint<CR>", "<Space>w": ":w<CR>",
	}}
	errs := removedCommandWarnings(km, map[string]string{})
	if len(errs) != 2 || !strings.Contains(errs[0].Error(), "keymap.normal の <Space>b の右辺の :dap.toggleBreakpoint") ||
		!strings.Contains(errs[1].Error(), ":definition か <C-]>") {
		t.Errorf("errs = %v", errs)
	}
	if errs := removedCommandWarnings(km, map[string]string{"lsp.definition": "old", "dap.toggleBreakpoint": "old"}); len(errs) != 0 {
		t.Errorf("宣言された古いコマンド: errs = %v", errs)
	}
	h := start(t, Config{Keymap: km})
	if msg := h.message(); !strings.Contains(msg, ":dap.toggleBreakpoint は無くなったコマンド") || !strings.HasPrefix(msg, "設定の誤り 2 件（:messages）: ") {
		t.Errorf("起動時のメッセージ = %q", msg)
	}
}

// 診断の一覧と次と前の診断への移動は、本体が受け取った診断から作り、提供者へ問い合わせない。
//
// req: FR-94, FR-95
func TestDiagnosticKeys(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "one\ntwo\nthree\nfour\n"})
	h := start(t, Config{WorkDir: dir}, "a.txt")
	h.keys("<Space>lj")
	h.settle()
	if msg := h.message(); !strings.Contains(msg, "診断がありません") {
		t.Errorf("診断の無い <Space>lj: message = %q", msg)
	}
	h.keys("<Space>le")
	h.settle()
	if msg := h.message(); msg != "診断はありません" {
		t.Errorf("診断の無い <Space>le: message = %q", msg)
	}

	h.e.do(func() {
		bs := h.e.cur
		h.e.setDiagnostics("x", ext.DiagnosticSetParams{Buffer: bs.id, Version: ext.Version(bs.buf.Version()), Diagnostics: []ext.Diagnostic{
			{Range: ext.Range{Start: ext.Position{Line: 2, Byte: 1}, End: ext.Position{Line: 2, Byte: 3}}, Severity: ext.SeverityError, Message: "bad\n\tthree", Source: "src"},
			{Range: ext.Range{Start: ext.Position{Line: 1}, End: ext.Position{Line: 1, Byte: 3}}, Severity: ext.SeverityWarning, Message: "two"},
		}})
	})
	cursor := func() string {
		var s string
		h.e.do(func() { c := h.e.cur.state.Cursor(); s = fmt.Sprintf("%d:%d", c.Line, c.Byte) })
		return s
	}
	for _, step := range []struct{ keys, want string }{
		{"<Space>lj", "1:0"}, {"<Space>lj", "2:1"}, {"<Space>lj", "1:0"}, // 末尾で先頭へ折り返す
		{"<Space>lk", "2:1"}, {"<Space>lk", "1:0"},
	} {
		h.keys(step.keys)
		h.settle()
		if got := cursor(); got != step.want {
			t.Errorf("%s: cursor = %s, want %s", step.keys, got, step.want)
		}
	}
	h.keys("<Space>le")
	h.settle()
	var popup string
	h.e.do(func() { popup = popupText(h.e) })
	path := h.e.cur.path
	want := fmt.Sprintf("%s:2:1: warning two\n%s:3:2: error src: bad three", path, path)
	if popup != want {
		t.Errorf("<Space>le: popup = %q, want %q", popup, want)
	}
}
