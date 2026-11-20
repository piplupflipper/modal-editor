package editor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/piplupflipper/vi-imitation/ext"
	"github.com/piplupflipper/vi-imitation/internal/buffer"
	"github.com/piplupflipper/vi-imitation/internal/command"
	"github.com/piplupflipper/vi-imitation/internal/exthost"
)

// finderOf は、読み込んでいる client のファインダーの一覧のバッファの中身と、一覧のカーソルの行と、見出しを返す。
// ファインダーを出していなければ ok が false である。イベントループの goroutine で呼ぶ。
func finderOf(e *Editor) (text string, row int, title string, ok bool) {
	if f := e.float; f != nil && f.kind == floatFinder {
		return f.list.buf.Text(), f.list.state.Cursor().Line, f.title, true
	}
	return "", 0, "", false
}

// finderList は、finderOf をイベントループの goroutine で呼ぶ。
func (h *harness) finderList() (text string, row int, title string) {
	h.e.do(func() { text, row, title, _ = finderOf(h.e) })
	return text, row, title
}

// finderText は、ファインダーの一覧のバッファの中身を返す。イベントループの goroutine で呼ぶ（eventually の中で使う）。
func finderText(e *Editor) string {
	text, _, _, _ := finderOf(e)
	return text
}

// ファインダー（入力の行と一覧の組）は、中央に出してフォーカスを入れ、入力の行をインサートモードで始める。
// 入力の行が変わるたびに番号付きの input/change を送り、拡張機能が同じ番号で返した一覧を出す。古い番号の一覧は出さない。
// 一覧のカーソルは <C-n> と <C-p> で動き、<CR> で項目の ID と入力の中身を view/select で知らせる。<Esc> はインサートモードでも閉じる。
//
// req: FR-106
func TestFinderPopup(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "hello\n"})
	h := start(t, Config{WorkDir: dir, Extensions: fakeSpecs(t)}, "a.txt")
	h.keys(":fake.popup finder<CR>")
	h.eventually("ファインダー", func() bool { _, _, _, ok := finderOf(h.e); return ok })
	var view uint64
	h.e.do(func() { view = h.e.float.id })
	h.waitPopupOpened(view)
	fi, _ := h.float()
	if fi.text != "f:" || !fi.focused || !fi.center || fi.kind != floatFinder {
		t.Fatalf("finder: %+v", fi)
	}
	var mode command.Mode
	h.e.do(func() { mode = h.e.cur.state.Mode() })
	if mode != command.ModeInsert {
		t.Errorf("finder mode = %v, want insert", mode)
	}
	if text, row, _ := h.finderList(); text != "apple\nbanana\ncherry" || row != 0 {
		t.Errorf("finder list = %q row %d", text, row)
	}
	if scr := h.term.screen(); !strings.Contains(scr, "> f:") || !strings.Contains(scr, "apple") {
		t.Errorf("finder screen:\n%s", scr)
	}

	// 打った文字は入力の行に入り、1 文字ごとに番号を進めて知らせる。拡張機能は同じ番号で絞った一覧を返す。
	h.keys("an")
	h.waitMessage("kind=2 item=f:an key= seq=2")
	h.eventually("絞った一覧", func() bool { return finderText(h.e) == "banana" })
	if _, _, title := h.finderList(); title != "finder 1" {
		t.Errorf("finder title = %q", title)
	}
	var decorated bool
	h.e.do(func() { _, decorated = h.e.float.list.decorations[popupDecorationOwner] })
	if !decorated {
		t.Errorf("finder: match highlights are not set")
	}

	// 古い番号の一覧は捨てる（基本設計 6.9）。
	h.e.do(func() {
		f := h.e.float
		h.e.viewUpdate(&exthost.ViewUpdate{Extension: "fake", Params: ext.ViewUpdateParams{
			View: ext.ViewID(f.id), Seq: f.inputSeq - 1,
			Component: ext.ViewComponent{Kind: ext.ViewFinder, Items: []ext.ViewItem{{ID: "old", Label: "stale"}}},
		}})
	})
	if text, _, _ := h.finderList(); text != "banana" {
		t.Errorf("finder list after a stale view/update = %q", text)
	}

	// 入力を戻すと一覧が戻り、カーソルは先頭の項目に置く。<C-n> と <C-p> で一覧のカーソルが動き、端で止まる。
	h.keys("<BS><BS>")
	h.eventually("戻した一覧", func() bool { return finderText(h.e) == "apple\nbanana\ncherry" })
	h.keys("<C-n><C-n><C-n><C-p>")
	h.settle()
	if _, row, _ := h.finderList(); row != 1 {
		t.Errorf("finder cursor after <C-n>x3 <C-p> = %d, want 1", row)
	}
	if fi, _ := h.float(); fi.text != "f:" {
		t.Errorf("input after cursor keys = %q", fi.text)
	}
	h.keys("<CR>")
	h.waitMessage("kind=1 item=b key= seq=0 input=f:")

	// <Esc> は、インサートモードのままでもファインダーを閉じ、元のタブへ戻る。
	h.keys("<Esc>")
	h.waitMessage("kind=0 item= key=")
	if _, ok := h.float(); ok {
		t.Errorf("<Esc> did not close the finder")
	}
	var name string
	h.e.do(func() { name, mode = h.e.bufferName(h.e.cur), h.e.cur.state.Mode() })
	if name != "a.txt" || mode != command.ModeNormal {
		t.Errorf("after closing the finder: buffer %q mode %v", name, mode)
	}
}

// buildFinder は、ファジーファインダーの拡張機能（cmd/vi-imitation-ext-finder）を一時ディレクトリへビルドし、拡張機能の定義を返す。
func buildFinder(t *testing.T) exthost.Spec {
	t.Helper()
	return buildStdExt(t, "vi-imitation-ext-finder")
}

// 標準のファジーファインダーの拡張機能で、<Space>ff のファイル、<Space>fg の検索（rg があれば）、<Space>fb のタブを開き、
// 打った入力で絞った一覧から選んだ先を開く。ファイルと検索の場所は、拡張機能のプロセスの作業ディレクトリでなく本体の作業ディレクトリである。
// ファイル、検索の結果、タブの項目には、ファイルの種類のアイコンが付き、キーとコマンドの項目には付かない。
//
// req: FR-106, FR-107, FR-152
func TestStandardFinder(t *testing.T) {
	spec := buildFinder(t)
	dir := writeFiles(t, map[string]string{
		"a.txt": "alpha\n", "sub/beta.go": "package sub\n\nfunc Needle() {}\n", "sub/gamma.md": "# gamma\n",
	})
	h := start(t, Config{WorkDir: dir, Extensions: []exthost.Spec{spec}}, "a.txt")
	// ファイル、検索の結果、タブの項目には、既定の表のファイルの種類のアイコン（FR-152）が付く。キーとコマンドには付かない。
	txt, gop, md := iconOf(t, "file"), iconOf(t, "fileTypes.go"), iconOf(t, "fileTypes.markdown")
	h.keys("<Space>ff")
	h.eventually("ファイルの一覧", func() bool { return finderText(h.e) != "" && !strings.Contains(h.e.float.title, "読み込み中") })
	if text, _, title := h.finderList(); text != txt+"a.txt\n"+gop+"sub/beta.go\n"+md+"sub/gamma.md" || !strings.Contains(title, "3/3") {
		t.Errorf("files: %q %q", text, title)
	}
	h.keys("sbgo")
	h.eventually("絞ったファイル", func() bool { return finderText(h.e) == gop+"sub/beta.go" })
	h.keys("<CR>")
	h.eventually("開いたファイル", func() bool { return h.e.float == nil && h.e.bufferName(h.e.cur) == filepath.Join("sub", "beta.go") })

	if _, err := exec.LookPath("rg"); err == nil {
		h.keys("<Space>fg")
		h.eventually("検索のファインダー", func() bool { _, _, _, ok := finderOf(h.e); return ok })
		h.keys("needle")
		h.eventually("検索の結果", func() bool { return finderText(h.e) == gop+"sub/beta.go:3:6: func Needle() {}" })
		h.keys("<CR>")
		h.eventually("一致の位置", func() bool {
			return h.e.float == nil && h.e.bufferName(h.e.cur) == filepath.Join("sub", "beta.go") && h.e.cur.state.Cursor() == buffer.Position{Line: 2, Byte: 5}
		})
	}

	h.keys("<Space>fb")
	h.eventually("タブの一覧", func() bool { return finderText(h.e) == txt+"a.txt\n"+gop+"sub/beta.go" })
	h.keys("a.t<CR>")
	h.eventually("移ったタブ", func() bool { return h.e.float == nil && h.e.bufferName(h.e.cur) == "a.txt" })

	// finder.resume は、最後のファインダーを最後の入力で開き直す。
	h.keys("<Space>fp")
	h.eventually("開き直したファインダー", func() bool { fi := h.e.float; return fi != nil && fi.kind == floatFinder && fi.bs.buf.Line(0) == "a.t" })
	h.keys("<Esc>")
	h.eventually("閉じたファインダー", func() bool { return h.e.float == nil })

	// キーとコマンドの候補は、選ぶと本体が打ったのと同じく実行する（editor/execute。PR #249 のレビューの必須 1）。
	h.keys("<Space>fk")
	h.eventually("キーの一覧", func() bool { _, _, _, ok := finderOf(h.e); return ok })
	h.keys("（y$）")
	h.eventually("絞ったキー", func() bool { return strings.HasPrefix(finderText(h.e), "Y ") })
	h.keys("<CR>")
	h.eventually("実行した Y", func() bool {
		r, ok := h.e.cur.state.Registers().Head()
		return h.e.float == nil && ok && r.Text == "alpha" && !r.Linewise
	})
	h.keys("<Space>fc")
	h.eventually("コマンドの一覧", func() bool { _, _, _, ok := finderOf(h.e); return ok })
	h.keys("readonly")
	h.eventually("絞ったコマンド", func() bool { return strings.HasPrefix(finderText(h.e), ":readonly") })
	h.keys("<CR>")
	h.eventually("実行した :readonly", func() bool { return h.e.float == nil && h.e.cur.buf.ReadOnly() })
	h.keys(":readonly<CR>")
}

// <Space>fh はヘルプの語の一覧を開き、選んだ語の :help を開く（FR-148）。一覧はキー、Ex コマンド、設定の名前、操作の ID を含む。
// 同梱でない拡張機能（docs/usage.md に書かれていない）のキーの無いコマンドと、オペレーターなどの操作の ID も出て、選ぶと :help が開く
// （PR #309 のレビューの必須 1）。説明は、その語を定めた所の説明文（キーの一覧、宣言ファイルの description、操作の表）である。
//
// req: FR-148
func TestStandardFinderHelp(t *testing.T) {
	spec := buildFinder(t)
	third := exthost.Spec{Manifest: ext.Manifest{ID: "peek3p", Command: "/bin/true", Contributes: ext.Contributes{
		Commands:    []ext.Contribution{{ID: "peek3p.list", Description: "3p list"}},
		TextObjects: []ext.Contribution{{ID: "peek3p.obj", Description: "3p obj"}},
	}}}
	dir := writeFiles(t, map[string]string{"a.txt": "alpha\n"})
	h := start(t, Config{WorkDir: dir, Extensions: []exthost.Spec{spec, third}}, "a.txt")
	h.keys("<Space>fh")
	h.eventually("ヘルプの語の一覧", func() bool { f := h.e.float; return f != nil && strings.HasPrefix(f.title, "ヘルプ") })
	// ファインダーが送るのは上位 300 件なので、語の一覧は本体の一覧（editor/list の help）で確かめる。
	var words []ext.ListHelpWord
	var lines []string
	h.e.do(func() { words, lines = h.e.listHelpWords(), strings.Split(h.e.helpText(), "\n") })
	kinds := map[string]string{}
	for _, w := range words {
		if _, dup := kinds[w.Word]; dup {
			t.Errorf("help word %q appears twice", w.Word)
		}
		kinds[w.Word] = w.Kind
		if _, ok := helpLine(lines, w.Word); !ok {
			t.Errorf("help word %q is not found by :help", w.Word)
		}
	}
	for w, kind := range map[string]string{
		"<C-o>": ext.HelpKindKey, "<C-p>": ext.HelpKindKey, "<Space>ff": ext.HelpKindKey, ":tabmove": ext.HelpKindCommand,
		":finder.files": ext.HelpKindCommand, "lineNumber.show": ext.HelpKindOption, "page-down": ext.HelpKindAction,
		"preview.page-down": ext.HelpKindAction, "colorscheme": ext.HelpKindOption, "leader": ext.HelpKindOption,
		":peek3p.list": ext.HelpKindCommand, "peek3p.obj": ext.HelpKindOperation,
	} {
		if kinds[w] != kind {
			t.Errorf("help word %q: kind %q, want %q", w, kinds[w], kind)
		}
	}
	descs := map[string]string{
		"w":            "次の単語の先頭へ（nvo）",
		"<C-p>":        "貼った直後に、履歴の古い項目に貼り直す（n）",
		":finder.keys": "キーの一覧を探す",
		"<Space>ff":    "ファイルを探して開く",
		":peek3p.list": "3p list",
		"peek3p.obj":   "3p obj",
		"visual":       "文字単位のビジュアルモード",
	}
	for _, w := range words {
		if want, ok := descs[w.Word]; ok && w.Description != want {
			t.Errorf("%s の説明 = %q, want %q", w.Word, w.Description, want)
		}
	}
	// 同梱でない拡張機能のコマンドも、選ぶと :help が開く。:help :ID と :help ID は同じ行に当たる。
	h.keys("peek3p.list")
	h.eventually("絞った第三者のコマンド", func() bool { return strings.HasPrefix(finderText(h.e), ":peek3p.list ") })
	h.keys("<CR>")
	h.eventually(":help :peek3p.list", func() bool { f := h.e.float; return f != nil && f.kind == floatText && f.title == helpTitle })
	var at string
	h.e.do(func() { at = h.e.cur.buf.Line(h.e.cur.state.Cursor().Line) })
	if !strings.HasPrefix(at, "- `:peek3p.list`（拡張機能 peek3p のコマンド）") {
		t.Errorf(":help :peek3p.list の行 = %q", at)
	}
	if i, ok := helpLine(lines, "peek3p.list"); !ok || lines[i] != at {
		t.Errorf(":help peek3p.list = %q, %v", lines[i], ok)
	}
	h.keys("<Esc><Space>fh")
	h.eventually("ヘルプの語の一覧", func() bool { f := h.e.float; return f != nil && strings.HasPrefix(f.title, "ヘルプ") })
	h.keys("tabmove")
	h.eventually("絞ったヘルプの語", func() bool { return strings.HasPrefix(finderText(h.e), ":tabmove ") })
	h.keys("<CR>")
	h.eventually(":help :tabmove", func() bool { f := h.e.float; return f != nil && f.kind == floatText && f.title == helpTitle })
	var line string
	h.e.do(func() { line = h.e.cur.buf.Line(h.e.cur.state.Cursor().Line) })
	if !strings.Contains(line, "`:tabmove") {
		t.Errorf(":help :tabmove の行 = %q", line)
	}
}

// ファインダーを開くキーを記録したマクロを再生すると、拡張機能が応答の前にポップアップを開き、再生の残りのキーはファインダーに入る。
// 元のバッファは書き換わらない（PR #249 のレビューの必須 2）。
//
// req: FR-106, FR-05
func TestFinderMacro(t *testing.T) {
	spec := buildFinder(t)
	dir := writeFiles(t, map[string]string{"a.txt": "alpha\n", "b.txt": "beta\n"})
	h := start(t, Config{WorkDir: dir, Extensions: []exthost.Spec{spec}, Options: noIcons}, "a.txt")
	h.keys("qb<Space>ff")
	h.eventually("記録中のファインダー", func() bool { _, _, _, ok := finderOf(h.e); return ok })
	h.keys("xx<Esc>q")
	h.eventually("閉じたファインダー", func() bool { return h.e.float == nil })
	var before uint64
	h.e.do(func() { before = h.e.floatSeq })
	h.keys("0@b")
	h.settle()
	h.eventually("再生の後", func() bool { return h.e.float == nil && h.e.floatSeq == before+1 })
	var text string
	var modified bool
	h.e.do(func() { text, modified = h.e.cur.buf.Text(), h.e.cur.buf.Modified() })
	if text != "alpha" || modified {
		t.Errorf("after @b: %q modified=%v", text, modified)
	}

	// 選ぶところまで記録したマクロは、再生でも同じファイルを開く。
	h.keys("qc<Space>ff")
	h.eventually("記録中のファインダー", func() bool { return finderText(h.e) == "a.txt\nb.txt" })
	h.keys("b.t<CR>")
	h.eventually("開いた b.txt", func() bool { return h.e.float == nil && h.e.bufferName(h.e.cur) == "b.txt" })
	h.keys("q:b a.txt<CR>")
	h.eventually("a.txt", func() bool { return h.e.bufferName(h.e.cur) == "a.txt" })
	h.keys("@c")
	h.eventually("再生で開いた b.txt", func() bool { return h.e.float == nil && h.e.bufferName(h.e.cur) == "b.txt" })
	h.e.do(func() { text = h.e.findPath(filepath.Join(dir, "a.txt")).buf.Text() })
	if text != "alpha" {
		t.Errorf("a.txt after @c: %q", text)
	}
}

// ファインダーを開く再帰のマクロ（qa<Space>ff<Esc>@aq の後の @a）は、待ちを挟まない再帰と同じく入れ子の上限（maxPlayDepth）で止まる。
// <C-c> を打てば、上限より前に止まる（PR #249 の 4 回目の確認の必須）。
//
// req: FR-05, FR-106
func TestFinderRecursiveMacroStops(t *testing.T) {
	spec := buildFinder(t)
	dir := writeFiles(t, map[string]string{"a.txt": "alpha\n"})
	h := start(t, Config{WorkDir: dir, Extensions: []exthost.Spec{spec}}, "a.txt")
	h.keys("qa<Space>ff")
	h.eventually("記録中のファインダー", func() bool { _, _, _, ok := finderOf(h.e); return ok })
	h.keys("<Esc>@aq")
	h.eventually("閉じたファインダー", func() bool { return h.e.float == nil })
	stopped := func(what string) uint64 {
		t.Helper()
		var seq uint64
		deadline := time.Now().Add(60 * time.Second)
		for {
			var done bool
			h.e.do(func() { done, seq = h.e.float == nil && !h.e.replayPending(), h.e.floatSeq })
			if done {
				time.Sleep(300 * time.Millisecond)
				var again uint64
				h.e.do(func() { done, again = h.e.float == nil && !h.e.replayPending(), h.e.floatSeq })
				if done && again == seq {
					return seq
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: 再生が止まらない（ポップアップを %d 回開いた）", what, seq)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	var before uint64
	h.e.do(func() { before = h.e.floatSeq })
	h.keys("@a")
	after := stopped("上限")
	if n := after - before; n < 90 || n > 101 {
		t.Errorf("上限: ポップアップを %d 回開いた（約 %d 回で止まるはず）", n, 100)
	}
	if got := h.text(); got != "alpha" {
		t.Errorf("text = %q", got)
	}
	h.keys("@a<C-c>")
	if n := stopped("<C-c>") - after; n >= 90 {
		t.Errorf("<C-c>: ポップアップを %d 回開いた（中断で止まらなかった）", n)
	}
}

// 拡張機能の結果の待ち（再生中の command/execute。PR #231）を挟む再帰のマクロも、入れ子の上限で止まる（PR #249 の 4 回目の確認の必須）。
//
// req: FR-05, FR-19
func TestReplayWaitRecursiveMacroStops(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "alpha\n"})
	h := start(t, Config{WorkDir: dir, Extensions: fakeSpecs(t)}, "a.txt")
	h.keys(`qa\u@aq`)
	h.settle()
	h.keys("@a")
	deadline := time.Now().Add(60 * time.Second)
	for {
		var done bool
		h.e.do(func() { done = !h.e.replayPending() })
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("再生が止まらない")
		}
		time.Sleep(20 * time.Millisecond)
	}
	h.settle()
	if got := h.text(); got != "ALPHA" {
		t.Errorf("text = %q", got)
	}
}

// editor/execute は、その拡張機能のポップアップで利用者が選んだことへの応答として 1 回だけ、利用者がノーマルモードで入力の途中で
// ないときだけ実行する。選んだ後にその client がキーを打ったら、その選択の番号は使えない。
// command は、コマンドの一覧の名前と完全に一致するものだけを受け付ける（PR #249 の再レビューの必須と 3 回目の確認の推奨。
// 入力はレビューで確かめたもの）。
//
// req: FR-107
func TestEditorExecuteGuard(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "alpha\nbeta\n"})
	h := start(t, Config{WorkDir: dir, Extensions: fakeSpecs(t)}, "a.txt")
	// selectWith は、fake.exec で x を覚えさせてから、偽の拡張機能の一覧を開いて keys で選ぶ。拡張機能は選んだ後に x を送る。
	selectWith := func(x, keys string) {
		t.Helper()
		h.keys(":fake.exec " + x + "<CR>")
		h.keys(":fake.popup list<CR>")
		h.eventually("一覧", func() bool { return h.e.float != nil && h.e.float.ext == "fake" })
		h.keys(keys)
	}
	check := func(what, want string) {
		t.Helper()
		h.settle()
		if got := h.text(); got != want {
			t.Errorf("%s: text = %q, want %q", what, got, want)
		}
	}
	const notSelected = "利用者が選んだ項目への応答としてだけ"

	// 選んでいないのに頼まれた実行は断る。
	h.keys(`:fake.exec {"keys":"x","selection":99,"now":true}<CR>`)
	h.waitMessage(notSelected)
	check("選んでいない", "alpha\nbeta")

	// 選んだ後に、届く前にキーを打ったら断る。インサートモードの x を文字として入れず（A）、:write を本文に入れず（Go）、
	// 動いた先の行で x を実行しない（j。レビューの再現）。
	// 届く前かは時間で決めない。拡張機能は hold で差し止め、打ったキーを処理し終えてから送らせる（#313）。
	// 断った知らせは、前の回の知らせと取り違えないように、履歴の数が増えるのを待つ。
	refusals := func() int {
		n := 0
		h.e.do(func() {
			for _, m := range h.e.msgHistory {
				if strings.Contains(m.Text, notSelected) {
					n++
				}
			}
		})
		return n
	}
	t.Cleanup(func() { _ = os.Remove(fakeReleasePath(os.Getpid(), "exec")) })
	for _, tt := range []struct{ x, after, want string }{
		{`{"keys":"x","hold":true}`, "A", "alpha\nbeta"},
		{`{"command":"write","hold":true}`, "Go", "alpha\nbeta\n"},
		{`{"keys":"x","hold":true}`, "j", "alpha\nbeta"},
	} {
		selectWith(tt.x, "j<CR>")
		h.eventually("選んで閉じた一覧", func() bool { return h.e.float == nil })
		h.keys(tt.after)
		h.settle()
		before := refusals()
		if err := os.WriteFile(fakeReleasePath(os.Getpid(), "exec"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		h.eventuallyOff("断った知らせ", func() bool { return refusals() > before })
		check(tt.x+" "+tt.after, tt.want)
		h.keys("<Esc>")
		if tt.after == "Go" {
			h.keys("u")
		}
		h.keys("gg")
	}

	// 選んだ後もフォーカスのあるポップアップが開いている（拡張機能が閉じなかった）なら断る。
	selectWith(`{"keys":"x"}`, "<CR>")
	h.waitMessage("入力の途中か待っている間")
	h.keys("<Esc>")
	check("開いたままのポップアップ", "alpha\nbeta")

	// 範囲、引数、| の付いたコマンドは断る。
	for _, c := range []string{`s/alpha/PWNED/`, `w | q`, `%d`} {
		selectWith(fmt.Sprintf(`{"command":%q}`, c), "j<CR>")
		h.waitMessage("コマンドの一覧にありません")
		check(c, "alpha\nbeta")
	}

	// 選んだことへの応答なら実行する。同じ選択でもう一度は断る。
	selectWith(`{"keys":"x","twice":true}`, "j<CR>")
	h.waitMessage(notSelected)
	check("実行と 2 回目", "lpha\nbeta")
	var modified bool
	h.e.do(func() { modified = h.e.cur.buf.Modified() })
	if !modified {
		t.Errorf("x did not modify the buffer")
	}
}

// 拡張機能は、editor/list で本体が持つ一覧（タブ、コマンド、キーの一覧、レジスターの履歴、ジャンプリスト）を問い合わせられる。
//
// req: FR-107
func TestEditorList(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "one\ntwo\nthree\n", "b.txt": "bee\n"})
	h := start(t, Config{WorkDir: dir, Extensions: fakeSpecs(t)}, "a.txt", "b.txt")
	for _, tt := range []struct {
		keys string
		list ext.EditorListKind
		want []string
	}{
		// テストの client の ClientInfo.ID は 0 である（client の番号は本体のほかの表示と同じ ClientInfo.ID。PR #249 のレビューの必須 3）。
		{"", ext.ListTabs, []string{`"name":"a.txt","client":0,"own":true`, `"name":"b.txt","client":0,"own":true,"current":true`}},
		{"", ext.ListCommands, []string{`{"name":"write","short":"w"}`, `{"name":"fake.list","extension":"fake"}`, `{"name":"help"}`}},
		{"", ext.ListKeys, []string{`{"keys":"gg","modes":"nvo","source":"builtin"`, `"source":"extension","extension":"fake"`}},
		{"yy", ext.ListRegisters, []string{`{"text":"bee","linewise":true}`}},
		{":b a.txt<CR>G", ext.ListJumps, []string{`"name":"b.txt","position":{"line":0,"byte":0},"text":"bee"},{`, `"name":"a.txt","position":{"line":0,"byte":0},"text":"one"}]}`}},
		{"<C-o>", ext.ListJumps, []string{`"name":"a.txt","position":{"line":0,"byte":0},"text":"one","current":true`}},
	} {
		if tt.keys != "" {
			h.keys(tt.keys)
		}
		h.keys(":fake.list " + string(tt.list) + "<CR>")
		h.waitMessage("list {")
		msg := h.message()
		for _, w := range tt.want {
			if !strings.Contains(msg, w) {
				t.Errorf("%s: %s does not contain %s", tt.list, msg, w)
			}
		}
		h.keys("<Esc>")
		h.settle()
	}
}
