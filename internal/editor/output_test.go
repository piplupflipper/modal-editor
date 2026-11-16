package editor

import (
	"fmt"
	"strings"
	"testing"

	"github.com/piplupflipper/modal-editor/ext"
)

// trimmedScreen は、描いた画面の各行の末尾の空白を除いて改行でつないだものを返す。
func (h *harness) trimmedScreen() string {
	lines := strings.Split(h.term.screen(), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return strings.Join(lines, "\n")
}

// screenHas は、描いた画面（各行の末尾の空白を除いたもの）に s が出るまで待つ。
func (h *harness) screenHas(what, s string) {
	h.t.Helper()
	h.eventually(what, func() bool { return strings.Contains(h.trimmedScreen(), s) })
}

// 出力の欄は :output で開き、最後に書き足された欄を出し、:output 名前 で切り替え、:outclose で閉じる。
// <C-w>P は閉じていれば開き、開いていれば次の欄へ切り替え、<C-w>z で閉じる。
//
// req: FR-31, FR-34
func TestOutputPane(t *testing.T) {
	h := start(t, Config{})
	h.keys(":output<CR>")
	h.settle()
	h.screenHas("空の欄", "[出力] (まだ出力はありません)")
	h.e.do(func() {
		h.e.appendOutput("acp", ext.OutputAppendParams{Channel: "acp", Text: "> こんにちは\nHello"})
		h.e.appendOutput("dap", ext.OutputAppendParams{Channel: "dap", Text: "debuggee\n"})
	})
	h.keys("<Esc>") // 描き直させる
	h.screenHas("最後に書き足された欄", "[出力] dap")
	if scr := h.term.screen(); !strings.Contains(scr, "debuggee") {
		t.Errorf("dap の欄の中身が無い:\n%s", scr)
	}
	h.keys(":output acp<CR>")
	h.screenHas(":output acp", "[出力] acp")
	if scr := h.term.screen(); !strings.Contains(scr, "Hello") {
		t.Errorf("acp の欄の最後の行が無い:\n%s", scr)
	}
	h.keys("<C-w>P")
	h.screenHas("<C-w>P で次の欄", "[出力] dap")
	h.keys("<C-w>P")
	h.screenHas("<C-w>P で最初の欄へ戻る", "[出力] acp")
	h.keys(":output nothing<CR>")
	h.settle()
	if msg := h.message(); msg != "出力の欄 nothing はありません" {
		t.Errorf("知らない欄: message = %q", msg)
	}
	h.keys("<C-w>z")
	h.eventually("<C-w>z で閉じる", func() bool { return !strings.Contains(h.term.screen(), "[出力]") })
	h.keys("<C-w>P")
	h.screenHas("<C-w>P で開く", "[出力] dap")
	h.keys(":outclose<CR>")
	h.eventually(":outclose で閉じる", func() bool { return !strings.Contains(h.term.screen(), "[出力]") })
}

// 出力の欄のスクロールは、欄の高さの半分ずつ動き、下の端まで戻ると末尾に追従する。
// 上へスクロールしている間に書き足しても、見ている位置は動かない。古い行を捨てたら、同じ行を指すように番号をずらす。
func TestOutputScroll(t *testing.T) {
	h := start(t, Config{})
	h.e.do(func() {
		for i := range 20 {
			h.e.appendOutput("acp", ext.OutputAppendParams{Channel: "acp", Text: fmt.Sprintf("line%02d\n", i)})
		}
	})
	h.keys(":output<CR>")
	h.screenHas("末尾", "line19")
	var rows int
	h.e.do(func() { rows = h.e.renderer.OutputRows() })
	if rows < 1 {
		t.Fatalf("OutputRows = %d", rows)
	}
	half := max(rows/2, 1)
	h.keys("<C-w>u")
	h.settle()
	h.e.do(func() {
		if got := h.e.outView.bottom; got != 20-half {
			t.Errorf("<C-w>u の後の bottom = %d, want %d", got, 20-half)
		}
		h.e.appendOutput("acp", ext.OutputAppendParams{Channel: "acp", Text: "line20\n"})
		if got := h.e.outView.bottom; got != 20-half {
			t.Errorf("書き足した後の bottom = %d, want %d（動かない）", got, 20-half)
		}
	})
	for range 30 {
		h.keys("<C-w>u")
	}
	h.settle()
	h.screenHas("先頭", "line00")
	for range 30 {
		h.keys("<C-w>d")
	}
	h.settle()
	h.e.do(func() {
		if got := h.e.outView.bottom; got != -1 {
			t.Errorf("下の端まで戻した後の bottom = %d, want -1（追従）", got)
		}
		// 上へ戻して見ている間に、上限を超えて古い行を捨てる。
		h.e.outView.bottom = 10
		for range maxOutputLines {
			h.e.appendOutput("acp", ext.OutputAppendParams{Channel: "acp", Text: "x\n"})
		}
		if got := h.e.outView.bottom; got != 0 {
			t.Errorf("捨てた後の bottom = %d, want 0", got)
		}
	})
	// 欄を開いていなければ、スクロールのキーは何もしない。
	h.keys("<C-w>z<C-w>u")
	h.settle()
	if msg := h.message(); msg != "" {
		t.Errorf("閉じた欄のスクロール: message = %q", msg)
	}
}

// 拡張機能のコマンドの前に :output を置ける（:output|acp.prompt ...）。
func TestOutputBeforeExtCommand(t *testing.T) {
	h := start(t, Config{Extensions: fakeSpecs(t)})
	h.keys(":output|fake.echo x<CR>")
	h.eventually("拡張機能のコマンドの結果", func() bool { return h.e.message.Text == `args={"text":"x"}` })
	h.e.do(func() {
		if h.e.outView == nil {
			t.Error("欄が開いていない")
		}
	})
}

// sign/set の印は印の列に出し、1 つの行の印は優先順位の最も高いものを出す。印は編集に合わせてずれる。
// 印と診断が無くなれば、印の列を出さない。
//
// req: FR-31
func TestSignColumn(t *testing.T) {
	h := start(t, Config{})
	h.keys("ia<CR>b<CR>c<Esc>")
	h.settle()
	h.e.do(func() {
		bs := h.e.cur
		v := ext.Version(bs.buf.Version())
		h.e.setSigns("dap", ext.SignSetParams{Buffer: bs.id, Version: v, Signs: []ext.Sign{
			{Line: 0, Kind: ext.SignBreakpoint}, {Line: 2, Kind: ext.SignBreakpoint}, {Line: 2, Kind: ext.SignExecutionPoint},
		}})
		h.e.setDiagnostics("lsp", ext.DiagnosticSetParams{Buffer: bs.id, Version: v, Diagnostics: []ext.Diagnostic{
			{Range: ext.Range{Start: ext.Position{Line: 0}, End: ext.Position{Line: 0, Byte: 1}}, Severity: ext.SeverityError},
			{Range: ext.Range{Start: ext.Position{Line: 1}, End: ext.Position{Line: 1, Byte: 1}}, Severity: ext.SeverityWarning},
		}})
	})
	h.keys("gg")
	h.screenHas("印の列", "B a\nW b\n=>c")
	h.keys("Oz<Esc>")
	h.screenHas("行を足した後の印", "  z\nB a\nW b\n=>c")
	h.e.do(func() {
		bs := h.e.cur
		v := ext.Version(bs.buf.Version())
		h.e.setSigns("dap", ext.SignSetParams{Buffer: bs.id, Version: v})
		h.e.setDiagnostics("lsp", ext.DiagnosticSetParams{Buffer: bs.id, Version: v})
	})
	h.keys("<Esc>")
	h.screenHas("印の列を消す", "z\na\nb\nc")
}
