package editor

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/internal/command"
	"github.com/piplupflipper/modal-editor/internal/exthost"
)

// 不正な UTF-8 を含むバッファは拡張機能へ送らない（基本設計 4.1、5.3。#62）。
// 本体は、送らなくなったバッファの色と診断と印を消し、送らない間に届いた一覧を捨てる。
// 拡張機能の操作は、送っていないことを伝える文言で失敗させ、バッファを使わないコマンドは実行する。

// setLinewiseRegister は、レジスター r に行単位の text を入れる。
func setLinewiseRegister(h *harness, r rune, text string) {
	h.e.do(func() { h.e.cur.state.Registers().Set(r, command.Register{Text: text, Linewise: true}) })
}

// 偽の拡張機能で、送らなくなったときに表示のデータを消し、送らない間の一覧を捨て、操作の失敗を日本語で知らせる。
func TestInvalidUTF8Withheld(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "abc\ndef\n"})
	h := start(t, Config{WorkDir: dir, Extensions: fakeSpecs(t)}, "a.txt")
	h.keys(`\d`)
	h.eventually("送っている間の複製", func() bool { return h.e.message.Text == `v0:"abc\ndef"` })

	// 送っている間に届いた表示のデータを持たせておく。
	rng := ext.Range{End: ext.Position{Byte: 3}}
	h.e.do(func() {
		v := ext.Version(h.e.cur.buf.Version())
		h.e.setDecorations("x", ext.DecorationSetParams{Buffer: h.e.cur.id, Version: v, Decorations: []ext.Decoration{{Range: rng, Style: "keyword"}}})
		h.e.setDiagnostics("x", ext.DiagnosticSetParams{Buffer: h.e.cur.id, Version: v, Diagnostics: []ext.Diagnostic{{Range: rng, Message: "m"}}})
		h.e.setSigns("x", ext.SignSetParams{Buffer: h.e.cur.id, Version: v, Signs: []ext.Sign{{Line: 0, Kind: ext.SignBreakpoint}}})
	})
	h.eventually("色の表", func() bool {
		return h.e.cur.decoView != nil && len(h.e.cur.diagnostics) == 1 && len(h.e.cur.signs) == 1
	})

	setLinewiseRegister(h, 'a', "\xff")
	h.keys(`gg"aP`)
	h.settle()
	if got := h.text(); got != "\xff\nabc\ndef" {
		t.Fatalf(`gg"aP: text = %q`, got)
	}
	h.e.do(func() {
		bs := h.e.cur
		if len(bs.decorations) != 0 || bs.decoView != nil || len(bs.diagnostics) != 0 || len(bs.signs) != 0 {
			t.Errorf("送らなくなった後も表示のデータが残る: decorations=%v decoView=%v diagnostics=%v signs=%v", bs.decorations, bs.decoView != nil, bs.diagnostics, bs.signs)
		}
		// 送らない間に届いた一覧は捨てる。
		v := ext.Version(bs.buf.Version())
		h.e.setDecorations("x", ext.DecorationSetParams{Buffer: bs.id, Version: v, Decorations: []ext.Decoration{{Range: rng, Style: "keyword"}}})
		if h.e.setDiagnostics("x", ext.DiagnosticSetParams{Buffer: bs.id, Version: v, Diagnostics: []ext.Diagnostic{{Range: rng, Message: "m"}}}) {
			t.Error("送っていないバッファの diagnostic/set を持った")
		}
		if h.e.setSigns("x", ext.SignSetParams{Buffer: bs.id, Version: v, Signs: []ext.Sign{{Line: 0, Kind: ext.SignBreakpoint}}}) {
			t.Error("送っていないバッファの sign/set を持った")
		}
		if len(bs.decorations) != 0 {
			t.Errorf("送っていないバッファの decoration/set を持った: %v", bs.decorations)
		}
	})

	const withheldMsg = "不正な UTF-8 を含むので、拡張機能へ送っていません"
	// バッファを使うコマンドと、テキストオブジェクトは、送っていないことを伝えて失敗する。テキストは変えない。
	for _, k := range []string{`\d`, `\u`, "dil"} {
		h.keys(k)
		h.settle()
		if msg := h.message(); !strings.Contains(msg, withheldMsg) {
			t.Errorf("%s: message = %q", k, msg)
		}
		if got := h.text(); got != "\xff\nabc\ndef" {
			t.Fatalf("%s: text = %q", k, got)
		}
	}
	// バッファを使わないコマンドは実行する。
	h.keys(`\s`)
	h.eventually(`\s の結果`, func() bool { return strings.HasPrefix(h.e.message.Text, "mode=normal") })

	// 不正なバイトを取り除くと、全文を送り直し、複製は本体と一致する。
	h.keys("u")
	h.settle()
	h.keys(`\d`)
	h.eventually("送り直した複製", func() bool { return h.e.message.Text == `v2:"abc\ndef"` })
}

// treesitterExtension は、標準の拡張機能 modal-editor-ext-treesitter を一時ディレクトリへビルドし、宣言ファイルを Spec にする。
// 文法を cgo でコンパイルするので、CGO_ENABLED は環境のまま（本体は cgo を使わない。NFR-02）。
func treesitterExtension(t *testing.T) exthost.Spec {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join("..", "..", "cmd", "modal-editor-ext-treesitter")
	//nolint:gosec // 引数は定数と、テストが作った一時ディレクトリのパスだけである
	cmd := exec.Command("go", "build", "-trimpath", "-o", filepath.Join(dir, "modal-editor-ext-treesitter"), "./"+filepath.ToSlash(src))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build extension: %v\n%s", err, out)
	}
	data, err := os.ReadFile(filepath.Join(src, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m ext.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if _, err := ext.ValidateManifests([]ext.Manifest{m}); err != nil {
		t.Fatal(err)
	}
	return exthost.Spec{Manifest: m, Dir: dir}
}

// 実物の tree-sitter の拡張機能で、色の付いた Go のファイルの先頭に不正なバイトの行を入れる（gg"aP）と、
// 古い色（1 行ずれて描かれる）を消す。u で取り除くと、拡張機能が解析し直した色が付く（PR #81 のレビューの再現）。
func TestInvalidUTF8ClearsTreesitterDecorations(t *testing.T) {
	if testing.Short() {
		t.Skip("拡張機能のビルドが要る")
	}
	dir := writeFiles(t, map[string]string{"a.go": "package a\n\nfunc f() {}\n"})
	h := start(t, Config{WorkDir: dir, Extensions: []exthost.Spec{treesitterExtension(t)}}, "a.go")
	h.eventually("tree-sitter の色", func() bool { return len(h.e.cur.decorations["treesitter"].decorations) > 0 })

	setLinewiseRegister(h, 'a', "\xff")
	h.keys(`gg"aP`)
	h.settle()
	if got := h.text(); got != "\xff\npackage a\n\nfunc f() {}" {
		t.Fatalf(`gg"aP: text = %q`, got)
	}
	h.eventually("古い色を消す", func() bool { return len(h.e.cur.decorations) == 0 && h.e.cur.decoView == nil })
	// 送っていない間に、行き違いの一覧が届いても持たない。
	time.Sleep(300 * time.Millisecond)
	h.e.do(func() {
		if len(h.e.cur.decorations) != 0 {
			t.Errorf("送っていない間の色 = %v", h.e.cur.decorations)
		}
	})

	h.keys("u")
	h.settle()
	h.eventually("解析し直した色", func() bool {
		ds, ok := h.e.cur.decorations["treesitter"]
		return ok && uint64(ds.version) == h.e.cur.buf.Version() && len(ds.decorations) > 0 && ds.decorations[0].Range.Start.Line == 0
	})
}
