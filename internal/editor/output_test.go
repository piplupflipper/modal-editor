package editor

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/internal/ui"
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
	h.keys("<C-w><PageUp>")
	h.settle()
	h.e.do(func() {
		if got := h.e.outView.bottom; got != 20-half {
			t.Errorf("<C-w><PageUp> の後の bottom = %d, want %d", got, 20-half)
		}
		h.e.appendOutput("acp", ext.OutputAppendParams{Channel: "acp", Text: "line20\n"})
		if got := h.e.outView.bottom; got != 20-half {
			t.Errorf("書き足した後の bottom = %d, want %d（動かない）", got, 20-half)
		}
		// 名前を省いて開いた欄は、上へスクロールした時点の欄に固定し、別の欄へ書き足されても替えない。
		h.e.appendOutput("dap", ext.OutputAppendParams{Channel: "dap", Text: "debuggee\n"})
		if got := h.e.shownOutput(); got != (outputKey{ext: "acp", channel: "acp"}) {
			t.Errorf("別の欄へ書き足した後の欄 = %v, want acp", got)
		}
	})
	for range 30 {
		h.keys("<C-w><PageUp>")
	}
	h.settle()
	h.screenHas("先頭", "line00")
	for range 30 {
		h.keys("<C-w><PageDown>")
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
	h.keys("<C-w>z<C-w><PageUp>")
	h.settle()
	if msg := h.message(); msg != "" {
		t.Errorf("閉じた欄のスクロール: message = %q", msg)
	}
}

// 折り返す長い行（opencode の返答の形: プロンプトの行、各段落が 1 行で 2〜3 行に折り返す 6 行、[end_turn] の行）だけの欄でも、
// 上へのスクロールは画面の行で数えるので、押すたびに動き、先頭の行（プロンプト）まで戻れる。下へ戻すと末尾に追従する。
//
// req: FR-34
func TestOutputScrollWrapped(t *testing.T) {
	h := start(t, Config{})
	h.e.do(func() {
		e := h.e
		text := "> main.go の main 関数を説明して\n"
		for i := range 6 {
			// 120 列で 2〜3 行に折り返す。どの画面の行も違う文字列になるように、語に番号を付ける。
			for j := range 18 + i%2*8 {
				text += fmt.Sprintf("段落%d-%02d本文 ", i, j)
			}
			text += "\n"
		}
		text += "[end_turn]\n"
		e.appendOutput("acp", ext.OutputAppendParams{Channel: "acp", Text: text})
		e.outView = &outputView{bottom: -1}
		// do の関数はイベントループの goroutine で動くので、t.Fatalf ではなく t.Errorf と return で止める。
		if w := e.renderer.Width; w != 120 {
			t.Errorf("renderer.Width = %d", w)
			return
		}
		lines := e.shownLines()
		total := 0
		for _, l := range lines {
			total += e.renderer.OutputLineRows(l)
		}
		if total <= len(lines)+5 {
			t.Errorf("折り返しが足りない: %d 行が %d 画面行", len(lines), total)
			return
		}
		r := &ui.Renderer{Width: 120, Height: 30}
		paneTop := func() string {
			scr := r.Render(&ui.Frame{Output: e.uiOutput()})
			return strings.TrimRight(scr.Grid.RowText(r.TextRows()+1), " ")
		}
		const rows = 10
		if r.Render(&ui.Frame{Output: e.uiOutput()}); r.OutputRows() != rows {
			t.Errorf("OutputRows = %d", r.OutputRows())
			return
		}
		prev := paneTop()
		reached := false
		for i := range total {
			e.scrollOutput(rows/2, rows)
			top := paneTop()
			if top == prev && !reached {
				t.Errorf("%d 回目の上へのスクロールで欄が動かない（上の端 %q）", i+1, top)
				return
			}
			prev = top
			if strings.HasPrefix(top, "> main.go") {
				reached = true
			}
		}
		if !reached {
			t.Errorf("先頭の行まで戻れない（上の端 %q）", prev)
			return
		}
		for range total {
			e.scrollOutput(-rows/2, rows)
		}
		if e.outView.bottom != -1 {
			t.Errorf("下の端まで戻した後の bottom = %d, want -1", e.outView.bottom)
		}
	})
}

// 改行の無い長い出力は、1 行の上限で文字の境目で次の行へ送る（NFR-03）。
func TestOutputLongLine(t *testing.T) {
	h := start(t, Config{})
	h.e.do(func() {
		e := h.e
		for range 3 {
			e.appendOutput("dap", ext.OutputAppendParams{Channel: "dap", Text: strings.Repeat("あ", maxOutputLineBytes/3+1)})
		}
		lines := e.outputs[outputKey{ext: "dap", channel: "dap"}]
		got := 0
		for _, l := range lines {
			if len(l) > maxOutputLineBytes || !utf8.ValidString(l) {
				t.Errorf("行の長さ %d、UTF-8 として正しい %v", len(l), utf8.ValidString(l))
			}
			got += len(l)
		}
		if want := 3 * 3 * (maxOutputLineBytes/3 + 1); got != want || len(lines) < 3 {
			t.Errorf("合計 %d バイト（want %d）、%d 行", got, want, len(lines))
		}
	})
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
	// 6 行の偽の端末の行の位置を確かめるので、タブが 1 つのときはタブの行を出さない。
	h := start(t, Config{TabLine: ui.TabLineMultiple})
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
