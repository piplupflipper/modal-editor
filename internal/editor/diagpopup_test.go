package editor

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/piplupflipper/vi-imitation/ext"
	"github.com/piplupflipper/vi-imitation/internal/command"
	"github.com/piplupflipper/vi-imitation/internal/option"
	"github.com/piplupflipper/vi-imitation/internal/ui"
)

// diagRange は、行 l のバイト b0 から b1 までの範囲である。
func diagRange(l, b0, b1 int) ext.Range {
	return ext.Range{Start: ext.Position{Line: l, Byte: b0}, End: ext.Position{Line: l, Byte: b1}}
}

// diagPopupOpts は、診断のポップアップを出し、カーソルが止まった契機の時間を 100 ミリ秒にした設定である（同梱の設定の例の値）。
var diagPopupOpts = map[string]option.Value{
	option.DiagnosticPopup:   option.BoolValue(true),
	option.CursorHoldDelayMs: option.IntValue(100),
}

// startDiagPopup は、files を書いた dir で、大きさ 100×24 の偽の端末でエディタを起動し、タイマーを偽物に差し替える。
// 最初のファイルを開く。opts は設定の項目の初めの値である。
func startDiagPopup(t *testing.T, opts map[string]option.Value, cfg Config, files map[string]string, open ...string) (*harness, *fakeTimers) {
	t.Helper()
	term := newFakeTerm()
	term.w, term.h = 100, 24
	if cfg.WorkDir == "" {
		cfg.WorkDir = writeFiles(t, files)
	}
	cfg.Options = opts
	h := startTerm(t, term, cfg, open...)
	ft := &fakeTimers{}
	h.e.do(func() { h.e.afterFunc = ft.after })
	h.settle()
	return h, ft
}

// setDiags は、カーソルのあるタブのバッファに、拡張機能 id の診断 ds を、今のバージョン番号で置く。
func setDiags(h *harness, id string, ds []ext.Diagnostic) {
	h.e.do(func() {
		bs := h.e.cur
		h.e.setDiagnostics(id, ext.DiagnosticSetParams{Buffer: bs.id, Version: ext.Version(bs.buf.Version()), Diagnostics: ds})
	})
}

// holdFire は、キーを処理し終えるのを待ってから、最後に掛けたカーソルが止まった契機のタイマーの時間が来たことにし、
// イベントループがその知らせを処理し終えるまで待つ。掛けたタイマーが無ければ失敗にする。
func holdFire(h *harness, ft *fakeTimers) {
	h.t.Helper()
	h.settle()
	n := ft.count()
	if n == 0 {
		h.t.Fatal("カーソルが止まった契機のタイマーを掛けていない")
	}
	ft.fire(n - 1)
	h.e.do(func() {}) // fire が送った関数を、イベントループが実行し終えるまで待つ
}

// diagFloat は、読み込んでいる client が出している診断のポップアップの中身を返す。出していなければ ok が false である。
func diagFloat(h *harness) (string, bool) {
	fi, ok := h.float()
	if !ok {
		return "", false
	}
	var diag bool
	h.e.do(func() { diag = h.e.float != nil && h.e.float.diagnostic && h.e.float.title == diagnosticPopupTitle })
	if !diag {
		return "", false
	}
	return fi.text, true
}

// viewDiagFloat は、client c が出している診断のポップアップの中身を返す。出していなければ空である。イベントループの goroutine で呼ぶ。
func viewDiagFloat(h *harness, c *clientView) string {
	if f := h.e.viewOf(c).float; f != nil && f.diagnostic {
		return f.bs.buf.Text()
	}
	return ""
}

// カーソルが止まると、カーソルの行の診断をすべて、重要度の順に、出どころとコードを付けてカーソルの近くのポップアップに出す
// （オーナーの設定での Neovim 0.12.5 の vim.diagnostic.open_float と同じ並びと形。2026-11-21 に tmux の中の nvim で確かめた）。
// 番号は診断が 1 つでも付け、改行を含む本文の続きの行は番号の幅だけ字下げする。複数の行にまたがる診断は、行の先頭で終わるなら
// 終わりの行に出さない。設定が真なら、メッセージの行には診断の本文を出さない。
//
// req: FR-171
func TestDiagnosticPopupContent(t *testing.T) {
	h, ft := startDiagPopup(t, diagPopupOpts, Config{}, map[string]string{"a.txt": "abc def\nghi\nf()\n"}, "a.txt")
	if n := ft.count(); n != 0 {
		t.Fatalf("起動した直後にタイマーを掛けた（%d 個）。キーを打つまでは待たない", n)
	}
	setDiags(h, "lsp", []ext.Diagnostic{
		{Range: diagRange(0, 0, 3), Severity: ext.SeverityHint, Message: "unused abc"},
		{Range: ext.Range{Start: ext.Position{Line: 0, Byte: 5}, End: ext.Position{Line: 1, Byte: 0}}, Severity: ext.SeverityError, Message: "span"},
		{Range: diagRange(0, 4, 7), Severity: ext.SeverityError, Message: "undefined: def", Source: "gopls", Code: "UndeclaredName"},
		{Range: diagRange(0, 0, 1), Severity: ext.SeverityWarning, Message: "w1", Code: "W1"},
		// gopls v0.24.0 の型検査の診断の形（PR #134 のレビューで実物から写したもの）。本文に改行とタブを含む。
		{Range: diagRange(2, 0, 3), Severity: ext.SeverityError, Message: "not enough arguments in call to f\n\thave ()\n\twant (int)", Source: "compiler", Code: "WrongArgCount"},
	})
	setDiags(h, "lint", []ext.Diagnostic{{Range: diagRange(0, 0, 3), Severity: ext.SeverityInfo, Message: "info msg", Source: "lint"}})

	h.keys("0")
	h.settle()
	if s := h.trimmedScreen(); strings.Contains(s, "undefined") {
		t.Errorf("止まる前に診断の本文が出た:\n%s", s)
	}
	ft.mu.Lock()
	if d := ft.ds[len(ft.ds)-1]; d != 100*time.Millisecond {
		t.Errorf("タイマーの時間 = %v, want cursorHold.delayMs の 100ms", d)
	}
	ft.mu.Unlock()
	holdFire(h, ft)
	want := "1. gopls: undefined: def (UndeclaredName)\n2. span\n3. w1 (W1)\n4. lint: info msg\n5. unused abc"
	if got, ok := diagFloat(h); !ok || got != want {
		t.Fatalf("1 行目のポップアップ = %q, %v\nwant %q", got, ok, want)
	}
	if fi, _ := h.float(); fi.focused || fi.center {
		t.Errorf("ポップアップ = %+v, want カーソルの近くでフォーカスなし", fi)
	}
	// 窓はカーソルの文字の列（印の列の右）から、カーソルの下の行に、見出しを最初の行にして置く（ホバーと同じ部品。枠線は既定で無い）。
	h.screenHas("見出しと 1 行目の下", "E abc def\n   診断\nE  1. gopls: undefined: def (UndeclaredName)\n~  2. span")
	if msg := h.message(); msg != "" {
		t.Errorf("メッセージの行 = %q, want 空（ポップアップに出すときはメッセージの行に出さない）", msg)
	}
	if s := h.trimmedScreen(); strings.Contains(s, "E gopls") {
		t.Errorf("メッセージの行に診断の本文が出た:\n%s", s)
	}

	// 2 行目は、1 行目から 2 行目の先頭までの診断の終わりの行なので出さない。
	h.keys("j")
	h.settle()
	if _, ok := h.float(); ok {
		t.Error("カーソルが動いてもポップアップが残る")
	}
	holdFire(h, ft)
	if got, ok := h.float(); ok {
		t.Errorf("行の先頭で終わる診断の終わりの行にポップアップを出した: %q", got.text)
	}

	h.keys("j")
	holdFire(h, ft)
	want = "1. compiler: not enough arguments in call to f\n   \thave ()\n   \twant (int) (WrongArgCount)"
	if got, ok := diagFloat(h); !ok || got != want {
		t.Errorf("改行を含む診断 = %q, %v\nwant %q", got, ok, want)
	}
}

// カーソルが止まった契機は、Neovim 0.12.5 の CursorHold と同じく、キーを打つたびに待ち直し、ノーマルモードでキーの列の途中でなく、
// マクロを記録していないときだけ来る。時間が来る前に次のキーを打ったら、前のタイマーは止め、その知らせが後から来ても出さない。
// インサートモードの間は出さず、出したポップアップはインサートモードに入るキーで閉じる。
//
// req: FR-171
func TestDiagnosticPopupHold(t *testing.T) {
	h, ft := startDiagPopup(t, diagPopupOpts, Config{}, map[string]string{"a.txt": "abc def\nghi\n"}, "a.txt")
	setDiags(h, "lsp", []ext.Diagnostic{{Range: diagRange(0, 0, 3), Severity: ext.SeverityError, Message: "bad", Source: "gopls"}})
	const want = "1. gopls: bad"

	// 時間が来る前に動いた: 前のタイマーは止め、止める前の知らせが来ても出さない。
	h.keys("l")
	h.settle()
	first := ft.count() - 1
	h.keys("h")
	h.settle()
	ft.mu.Lock()
	stopped := ft.stopped[first]
	ft.mu.Unlock()
	if !stopped {
		t.Error("次のキーで前のタイマーを止めていない")
	}
	ft.fire(first)
	h.e.do(func() {})
	if _, ok := h.float(); ok {
		t.Error("止めたタイマーの知らせでポップアップを出した")
	}
	holdFire(h, ft)
	if got, ok := diagFloat(h); !ok || got != want {
		t.Fatalf("止まった後 = %q, %v", got, ok)
	}

	for _, tt := range []struct {
		name, keys, back string
	}{
		{"インサートモード", "i", "<Esc>"},
		{"マクロの記録", "qa", "q"},
		{"オペレーターの後", "d", "<Esc>"},
		{"ビジュアルモード", "v", "<Esc>"},
		{"コマンドラインモード", ":", "<Esc>"},
	} {
		h.keys(tt.keys)
		h.settle()
		if _, ok := h.float(); ok {
			t.Errorf("%s: %s で前のポップアップが閉じない", tt.name, tt.keys)
		}
		holdFire(h, ft)
		if _, ok := h.float(); ok {
			t.Errorf("%s の間にポップアップを出した", tt.name)
		}
		h.keys(tt.back)
		holdFire(h, ft)
		if got, ok := diagFloat(h); !ok || got != want {
			t.Errorf("%s を抜けた後 = %q, %v", tt.name, got, ok)
		}
	}

	// 出した後は、次のキーを打つまで知らせ直さない（掛けたタイマーは 1 つだけで、もう掛け直していない）。
	n := ft.count()
	h.e.do(func() {})
	if got := ft.count(); got != n {
		t.Errorf("キーを打たずにタイマーを掛け直した（%d → %d）", n, got)
	}

	// 診断が変わっただけでは閉じない（Neovim 0.12.5 の open_float と同じ）。
	setDiags(h, "lsp", nil)
	if got, ok := diagFloat(h); !ok || got != want {
		t.Errorf("診断が変わった後 = %q, %v（閉じない）", got, ok)
	}
	// 診断の無い行では出さない。
	h.keys("<Esc>")
	holdFire(h, ft)
	if _, ok := h.float(); ok {
		t.Error("診断の無い行でポップアップを出した")
	}
}

// ホバーなど、ほかのポップアップが出ている間は、カーソルが止まっても診断のポップアップを出さず、ほかのポップアップを残す。
// タブを移ると閉じる。
//
// req: FR-171
func TestDiagnosticPopupOtherFloat(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.provft": "abc\ndef\n", "b.provft": "xyz\n"})
	h, ft := startDiagPopup(t, diagPopupOpts, Config{WorkDir: dir, Extensions: providerSpecs(t), FileTypes: map[string]string{".provft": "provft"}}, nil,
		filepath.Join(dir, "b.provft"), filepath.Join(dir, "a.provft"))
	setDiags(h, "lsp", []ext.Diagnostic{{Range: diagRange(0, 0, 3), Severity: ext.SeverityWarning, Message: "warn"}})
	h.keys("K")
	h.eventually("ホバー", func() bool { return popupText(h.e) == hoverText })
	holdFire(h, ft)
	var shown string
	h.e.do(func() { shown = popupText(h.e) })
	if shown != hoverText {
		t.Errorf("ホバーの間に止まった後のポップアップ = %q, want ホバーのまま", shown)
	}

	// ホバーをフォーカスに入れた中で止まっても、出さない。
	h.keys("<C-w>w")
	holdFire(h, ft)
	if fi, ok := h.float(); !ok || !fi.focused || fi.text != hoverText {
		t.Errorf("フォーカスを入れたホバーの中で止まった後 = %+v, %v", fi, ok)
	}
	h.keys("<Esc>")
	holdFire(h, ft)
	if got, ok := diagFloat(h); !ok || got != "1. warn" {
		t.Fatalf("ホバーを閉じた後 = %q, %v", got, ok)
	}
	// 別のタブへ移ると閉じる。
	h.keys("<Tab>")
	h.settle()
	if _, ok := h.float(); ok {
		t.Error("タブを移ってもポップアップが残る")
	}
}

// 設定が偽（既定）なら、タイマーを掛けず、今までどおりカーソルの行の診断の本文をメッセージの行に出す。
// 実行中に :option.set diagnostic.popup true で変えると、次のキーから契機を待ち、メッセージの行には出さない。
//
// req: FR-171
func TestDiagnosticPopupOff(t *testing.T) {
	h, ft := startDiagPopup(t, nil, Config{}, map[string]string{"a.txt": "abc def\n"}, "a.txt")
	setDiags(h, "lsp", []ext.Diagnostic{{Range: diagRange(0, 4, 7), Severity: ext.SeverityError, Message: "undefined: def", Source: "gopls"}})
	h.keys("w")
	h.screenHas("メッセージの行の診断", "E gopls: undefined: def")
	if n := ft.count(); n != 0 {
		t.Errorf("設定が偽でタイマーを %d 個掛けた", n)
	}
	h.keys(":option.set diagnostic.popup true<CR>")
	h.settle()
	h.keys("0")
	h.settle()
	if s := h.trimmedScreen(); strings.Contains(s, "E gopls") {
		t.Errorf("設定が真でメッセージの行に診断の本文を出した:\n%s", s)
	}
	holdFire(h, ft)
	if got, ok := diagFloat(h); !ok || got != "1. gopls: undefined: def" {
		t.Errorf("設定を真にした後 = %q, %v", got, ok)
	}
}

// 時間の待ちは本物のタイマー（time.AfterFunc）でイベントループの外で行い、時間が来たらイベントループに戻して出す（基本設計 2.1）。
// カーソルが止まった契機は client ごとに持ち、キーを打った client のカーソルの行の診断を、その client の画面にだけ出す。
// 時間を 0 にして、出るまで待つ（時間の長さには頼らない）。
//
// req: FR-171
func TestDiagnosticPopupRealTimer(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "alpha\n", "b.txt": "beta\n"})
	term := newFakeTerm()
	term.w, term.h = 100, 24
	h := startTerm(t, term, Config{WorkDir: dir, Options: map[string]option.Value{
		option.DiagnosticPopup: option.BoolValue(true), option.CursorHoldDelayMs: option.IntValue(0),
	}}, "a.txt")
	b := h.attach(ClientInfo{ID: 2, Files: []string{filepath.Join(dir, "b.txt")}})
	h.e.do(func() {
		for _, bs := range h.e.bufs {
			if filepath.Base(bs.path) == "b.txt" {
				h.e.setDiagnostics("lsp", ext.DiagnosticSetParams{Buffer: bs.id, Version: ext.Version(bs.buf.Version()), Diagnostics: []ext.Diagnostic{
					{Range: diagRange(0, 0, 1), Severity: ext.SeverityError, Message: "bad beta"},
				}})
			}
		}
	})
	b.keys("l")
	h.eventually("B の画面のポップアップ", func() bool { return strings.Contains(b.term.screen(), "1. bad beta") })
	if s := h.term.screen(); strings.Contains(s, "bad beta") {
		t.Errorf("A の画面に出した:\n%s", s)
	}
	b.keys("h")
	h.settle()
	h.eventually("キーで閉じてまた出る", func() bool {
		return h.e.viewOf(h.e.clients[1]).float != nil && strings.Contains(b.term.screen(), "1. bad beta")
	})
}

// 行の診断の並べ方と、ポップアップの行と色を付ける範囲の作り方。長い行は窓の幅で空白の位置で折り返し、
// 色は各診断の最初の行の番号を除いた全体に、重要度の印の色の名前で付ける。
//
// req: FR-171
func TestDiagnosticPopupLines(t *testing.T) {
	bs := &bufferState{diagnostics: map[string][]ext.Diagnostic{
		"b": {{Range: diagRange(1, 2, 3), Severity: ext.SeverityError, Message: "e2"}},
		"a": {
			{Range: diagRange(1, 2, 3), Severity: ext.SeverityError, Message: "e1"},
			{Range: diagRange(1, 0, 1), Severity: ext.SeverityError, Message: "e0"},
			{Range: diagRange(1, 0, 9), Severity: ext.SeverityWarning, Message: "w"},
			// 逆向きの範囲も、始まりと終わりを入れ替えて数える。
			{Range: ext.Range{Start: ext.Position{Line: 2, Byte: 0}, End: ext.Position{Line: 0, Byte: 4}}, Severity: ext.SeverityHint, Message: "rev"},
			{Range: diagRange(0, 0, 1), Severity: ext.SeverityError, Message: "other line"},
		},
	}}
	var msgs []string
	for _, d := range lineDiagnostics(bs, 1) {
		msgs = append(msgs, d.Message)
	}
	if got := strings.Join(msgs, ","); got != "e0,e1,e2,w,rev" {
		t.Errorf("並び = %s", got)
	}

	ds := []ext.Diagnostic{
		{Severity: ext.SeverityError, Message: "aaa bbb ccc ddd", Source: "s", Code: "C"},
		{Severity: ext.SeverityHint, Message: "x\ny"},
	}
	lines, decs := diagnosticPopupLines(ds, 12, 8)
	if got := strings.Join(lines, "|"); got != "1. s: aaa |bbb ccc ddd |(C)|2. x|   y" {
		t.Errorf("行 = %q", got)
	}
	var got []string
	for _, d := range decs {
		got = append(got, d.Style+":"+lines[d.Range.Start.Line][d.Range.Start.Byte:d.Range.End.Byte])
	}
	want := []string{
		ui.StyleSignDiagnosticError + ":s: aaa ", ui.StyleSignDiagnosticError + ":bbb ccc ddd ", ui.StyleSignDiagnosticError + ":(C)",
		ui.StyleSignDiagnosticHint + ":x", ui.StyleSignDiagnosticHint + ":   y",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("色の範囲 = %q\nwant %q", got, want)
	}
}

// 診断の範囲の向きをそろえ、掛かる行を決めるのは diagnosticSpanOf の 1 か所で、行の先頭で終わる複数の行の範囲は終わりの行に掛からない。
// メッセージの行の 1 行の表示（FR-30）と次の診断へ移る操作も、同じ関数を使う。
//
// req: FR-171
func TestDiagnosticSpanOf(t *testing.T) {
	pos := func(l, b int) ext.Position { return ext.Position{Line: l, Byte: b} }
	for _, tt := range []struct {
		r           ext.Range
		start       ext.Position
		first, last int
	}{
		{ext.Range{Start: pos(1, 2), End: pos(1, 5)}, pos(1, 2), 1, 1},
		{ext.Range{Start: pos(1, 5), End: pos(1, 2)}, pos(1, 2), 1, 1},
		{ext.Range{Start: pos(1, 3), End: pos(3, 0)}, pos(1, 3), 1, 2},
		{ext.Range{Start: pos(3, 0), End: pos(1, 3)}, pos(1, 3), 1, 2},
		{ext.Range{Start: pos(1, 3), End: pos(3, 1)}, pos(1, 3), 1, 3},
		{ext.Range{Start: pos(2, 0), End: pos(2, 0)}, pos(2, 0), 2, 2}, // 1 行の中の空の範囲は、その行に掛かる
	} {
		s := diagnosticSpanOf(tt.r)
		if s.r.Start != tt.start || s.first != tt.first || s.last != tt.last {
			t.Errorf("diagnosticSpanOf(%+v) = %+v, want start %+v, 行 %d〜%d", tt.r, s, tt.start, tt.first, tt.last)
		}
	}
	bs := &bufferState{diagnostics: map[string][]ext.Diagnostic{
		"x": {{Range: ext.Range{Start: pos(0, 2), End: pos(1, 0)}, Severity: ext.SeverityError, Message: "span"}},
	}}
	if _, ok := bs.cursorDiagnostic(pos(1, 0)); ok {
		t.Error("メッセージの行の表示が、行の先頭で終わる範囲の終わりの行に出た")
	}
	if m, ok := bs.cursorDiagnostic(pos(0, 0)); !ok || m.Text != "E span" {
		t.Errorf("始まりの行のメッセージの行の表示 = %q, %v", m.Text, ok)
	}
}

// カーソルが止まった契機は、どの契機を待つものにも共通の条件（問い、待ち、先打ちのキー）と、契機を待つものごとの条件（ready）で知らせる。
// インサートモードで受けるもの（Vim の CursorHoldI に当たる）を差し込むと、インサートモードの間にも呼ばれ、
// ノーマルモードでだけ受ける診断のポップアップは出ない。ノーマルモードに戻ると、差し込んだものは呼ばれず、診断のポップアップが出る。
// 時間は、契機を待つものすべてに共通の cursorHold.delayMs の 1 つである。
//
// req: FR-171
func TestCursorHoldInsertListener(t *testing.T) {
	h, ft := startDiagPopup(t, diagPopupOpts, Config{}, map[string]string{"a.txt": "abc\ndef\n"}, "a.txt")
	setDiags(h, "lsp", []ext.Diagnostic{{Range: diagRange(0, 0, 3), Severity: ext.SeverityError, Message: "bad"}})
	var calls []string
	h.e.do(func() {
		h.e.holdListeners = append(h.e.holdListeners, holdListener{
			wants: func(*Editor) bool { return true },
			ready: func(e *Editor) bool { return e.cur.state.Mode() == command.ModeInsert },
			held:  func(e *Editor) { calls = append(calls, e.cur.buf.Text()) },
		})
	})
	h.keys("ix")
	holdFire(h, ft)
	var got []string
	h.e.do(func() { got = slices.Clone(calls) })
	if len(got) != 1 || got[0] != "xabc\ndef" {
		t.Errorf("インサートモードで受けるものの呼ばれ方 = %q, want [xabc]", got)
	}
	if _, ok := h.float(); ok {
		t.Error("インサートモードの間に診断のポップアップを出した")
	}
	// 問いの間（共通の条件）は、どれも呼ばない。
	h.keys("<Esc>:2,1d<CR>")
	h.settle()
	holdFire(h, ft)
	h.e.do(func() { got = slices.Clone(calls) })
	if _, ok := h.float(); ok || len(got) != 1 {
		t.Errorf("問いの間に知らせた: ポップアップ %v, 呼ばれ方 %q", ok, got)
	}
	h.keys("n")
	holdFire(h, ft)
	h.e.do(func() { got = slices.Clone(calls) })
	if len(got) != 1 {
		t.Errorf("ノーマルモードで、インサートモードで受けるものを呼んだ: %q", got)
	}
	if text, ok := diagFloat(h); !ok || text != "1. bad" {
		t.Errorf("ノーマルモードに戻った後 = %q, %v", text, ok)
	}
}

// ペインの部品の client（ファイラなど）でキーを打つと、元の client の画面の診断のポップアップを閉じ、元の client のカーソルが止まった契機を
// 待ち直さない（Neovim も、別の窓へ移ると open_float の窓を閉じる。PR #333 のレビューの推奨 1）。
// 元の client でキーを打てば、また待つ。
//
// req: FR-171
func TestDiagnosticPopupPaneKeys(t *testing.T) {
	var calls muxCalls
	cfg := Config{Extensions: fakeSpecs(t), Options: diagPopupOpts}
	calls.config(&cfg)
	h := start(t, cfg)
	ft := &fakeTimers{}
	h.e.do(func() { h.e.afterFunc = ft.after })
	peer := h.attach(ClientInfo{ID: 2, Pane: "%9"})
	peer.keys(":fake.pane tree<CR>")
	h.eventually("OpenPane", func() bool { _, ok := calls.last(true); return ok })
	call, _ := calls.last(true)
	h.waitPaneOpened(call.view)
	comp := h.attachComponent(call.view, true)
	comp.waitScreen("▸ dir")
	var pc *clientView
	h.e.do(func() {
		pc = h.clientOf(peer.term)
		bs := h.e.viewOf(pc).cur
		h.e.setDiagnostics("lsp", ext.DiagnosticSetParams{Buffer: bs.id, Version: ext.Version(bs.buf.Version()), Diagnostics: []ext.Diagnostic{
			{Range: diagRange(0, 0, 0), Severity: ext.SeverityError, Message: "bad"},
		}})
	})
	peer.keys("0")
	holdFire(h, ft)
	var shown string
	h.e.do(func() { shown = viewDiagFloat(h, pc) })
	if shown != "1. bad" {
		t.Fatalf("元の client の診断のポップアップ = %q", shown)
	}
	n := ft.count()
	comp.keys("j")
	h.settle()
	h.e.do(func() { shown = viewDiagFloat(h, pc) })
	if shown != "" {
		t.Errorf("ペインのキーの後も元の client の診断のポップアップが残る: %q", shown)
	}
	if got := ft.count(); got != n {
		t.Errorf("ペインのキーでタイマーを掛け直した（%d → %d）", n, got)
	}
	// 元の client のキーで掛けたタイマーが時間の来る前なら、ペインのキーで止め、止めたタイマーの知らせは捨てる。
	peer.keys("0")
	h.settle()
	m := ft.count() - 1
	comp.keys("k")
	h.settle()
	ft.mu.Lock()
	stopped := ft.stopped[m]
	ft.mu.Unlock()
	if !stopped || ft.count() != m+1 {
		t.Errorf("ペインのキーで元の client のタイマーを止めて掛け直さない: 止めた %v, タイマー %d 個", stopped, ft.count())
	}
	holdFire(h, ft)
	h.e.do(func() { shown = viewDiagFloat(h, pc) })
	if shown != "" {
		t.Errorf("ペインのキーの後に止まって、元の client に出した: %q", shown)
	}
	peer.keys("0")
	holdFire(h, ft)
	h.e.do(func() { shown = viewDiagFloat(h, pc) })
	if shown != "1. bad" {
		t.Errorf("元の client のキーの後 = %q", shown)
	}
}

// 2 つの client のキーが交互に来ても、client ごとのタイマーは互いを止めない。A のキー、B のキー、A の時間が来る、の順なら
// A の画面にだけ出し、続けて B の時間が来れば B の画面にも出す（PR #333 のレビューの推奨 4）。
//
// req: FR-171
func TestDiagnosticPopupTwoClients(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "alpha\n", "b.txt": "beta\n"})
	h, ft := startDiagPopup(t, diagPopupOpts, Config{WorkDir: dir}, nil, filepath.Join(dir, "a.txt"))
	b := h.attach(ClientInfo{ID: 2, Files: []string{filepath.Join(dir, "b.txt")}})
	var ac, bc *clientView
	h.e.do(func() {
		ac, bc = h.clientOf(h.term), h.clientOf(b.term)
		for _, bs := range h.e.bufs {
			h.e.setDiagnostics("lsp", ext.DiagnosticSetParams{Buffer: bs.id, Version: ext.Version(bs.buf.Version()), Diagnostics: []ext.Diagnostic{
				{Range: diagRange(0, 0, 1), Severity: ext.SeverityError, Message: "bad " + filepath.Base(bs.path)},
			}})
		}
	})
	h.keys("l")
	h.settle()
	ai := ft.count() - 1
	b.keys("l")
	h.settle()
	bi := ft.count() - 1
	ft.mu.Lock()
	aStopped := ft.stopped[ai]
	ft.mu.Unlock()
	if ai == bi || aStopped {
		t.Fatalf("B のキーで A のタイマーを止めた（A %d、B %d、止めた %v）", ai, bi, aStopped)
	}
	ft.fire(ai)
	var a, bs string
	h.e.do(func() { a, bs = viewDiagFloat(h, ac), viewDiagFloat(h, bc) })
	if a != "1. bad a.txt" || bs != "" {
		t.Errorf("A の時間が来た後: A = %q, B = %q", a, bs)
	}
	ft.fire(bi)
	h.e.do(func() { a, bs = viewDiagFloat(h, ac), viewDiagFloat(h, bc) })
	if a != "1. bad a.txt" || bs != "1. bad b.txt" {
		t.Errorf("B の時間も来た後: A = %q, B = %q", a, bs)
	}
	// B のキーは A のポップアップを閉じない（ポップアップも client ごと）。
	b.keys("h")
	h.settle()
	h.e.do(func() { a, bs = viewDiagFloat(h, ac), viewDiagFloat(h, bc) })
	if a != "1. bad a.txt" || bs != "" {
		t.Errorf("B のキーの後: A = %q, B = %q", a, bs)
	}
}
