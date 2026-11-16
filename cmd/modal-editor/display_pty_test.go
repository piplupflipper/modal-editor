package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/piplupflipper/modal-editor/internal/term/ptytest"
)

// installExtension は、標準の拡張機能 name（cmd/modal-editor-ext-<name>）をビルドし、
// 宣言ファイルと一緒に xdg の下の利用者の拡張機能のディレクトリへ置く（基本設計 7 章の拡張機能を探す規則）。
func installExtension(t *testing.T, xdg, name string) {
	t.Helper()
	src := filepath.Join("..", "modal-editor-ext-"+name)
	dir := filepath.Join(xdg, "data", "modal-editor", "extensions", name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	goBuild(t, filepath.Join(dir, "modal-editor-ext-"+name), "./"+filepath.ToSlash(src))
	manifest, err := os.ReadFile(filepath.Join(src, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifest, 0o600); err != nil { //nolint:gosec // テストの一時ディレクトリへ、リポジトリの宣言ファイルを写す

		t.Fatal(err)
	}
}

// goBuild は、パッケージ pkg を out へビルドする。
func goBuild(t *testing.T, out, pkg string) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go が無い")
	}
	if b, err := exec.Command("go", "build", "-o", out, pkg).CombinedOutput(); err != nil { //nolint:gosec // テストで使う実行ファイルを一時ディレクトリへビルドする
		t.Fatalf("build %s: %v\n%s", pkg, err, b)
	}
}

// writeConfig は、xdg の下に設定ファイルを置く。
func writeConfig(t *testing.T, xdg, text string) {
	t.Helper()
	dir := filepath.Join(xdg, "config", "modal-editor")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.jsonc"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestOutputPaneInPTY は、ACP の拡張機能とテスト用のエージェントを実際に起動し、
// :output で開いた出力の欄に、エージェントの応答が出ることを確かめる（基本設計 6.5、6.7）。
//
// req: FR-34
func TestOutputPaneInPTY(t *testing.T) {
	xdg := t.TempDir()
	installExtension(t, xdg, "acp")
	agent := filepath.Join(t.TempDir(), "fakeagent")
	goBuild(t, agent, "./"+filepath.ToSlash(filepath.Join("..", "modal-editor-ext-acp", "testdata", "fakeagent")))
	writeConfig(t, xdg, `{ "extensions": { "settings": { "acp": { "agent": { "command": `+strconv.Quote(agent)+` } } } } }`)
	p := startEditorIn(t, xdg, nil, filepath.Join(t.TempDir(), "a.txt"))
	before := ptytest.Termios(t, p.tty)
	p.out.WaitForString(t, "a.txt")
	p.write(t, `:output|acp.prompt {"op":"hello"}`+"\r")
	// 端末への出力は前の画面との差分なので、空白を飛ばした語ごとに、カーソルの移動（CUP）を挟んで出る。
	// 欄の名前の行（24 行の画面の 23 行目）は、まだ出力が無い "(まだ出力はありません)" から、書き足された欄の "acp" に変わる。
	p.out.WaitForString(t, "[出力]")
	p.out.WaitFor(t, "acp の欄の名前", func(out string) bool { return outputTitleRe.MatchString(out) })
	p.out.WaitForString(t, "Hello,")
	p.out.WaitForString(t, "world.")
	p.write(t, ":q\r")
	if err := p.wait(t); err != nil {
		t.Fatalf("エディタの終了 = %v。出力: %q", err, p.out.String())
	}
	p.checkRestored(t, before)
}

// TestChoiceInPTY は、ACP の拡張機能とテスト用のエージェントを実際に起動し、80 列の端末で、
// エージェントの権限の確認の問い（choice/ask）の文と選択肢と取り消し方が、行に分けて見えることを確かめる（#144）。
// 番号のキーで選ぶと、選んだ選択肢がエージェントへ返る。
//
// req: FR-34
func TestChoiceInPTY(t *testing.T) {
	xdg := t.TempDir()
	installExtension(t, xdg, "acp")
	agent := filepath.Join(t.TempDir(), "fakeagent")
	goBuild(t, agent, "./"+filepath.ToSlash(filepath.Join("..", "modal-editor-ext-acp", "testdata", "fakeagent")))
	writeConfig(t, xdg, `{ "extensions": { "settings": { "acp": { "agent": { "command": `+strconv.Quote(agent)+` } } } } }`)
	p := startEditorIn(t, xdg, nil, filepath.Join(t.TempDir(), "a.txt"))
	before := ptytest.Termios(t, p.tty)
	p.out.WaitForString(t, "a.txt")
	p.write(t, `:output|acp.prompt {"op":"permission"}`+"\r")
	// 24 行の画面の下から、問いの文（21 行目）、選択肢（22、23 行目）、選び方の案内（24 行目）の順に出る。
	for _, re := range choiceRowRes {
		p.out.WaitFor(t, re.String(), func(out string) bool { return re.MatchString(out) })
	}
	p.write(t, "1")
	p.out.WaitForString(t, `"optionId":"allow-once"`)
	p.write(t, ":q\r")
	if err := p.wait(t); err != nil {
		t.Fatalf("エディタの終了 = %v。出力: %q", err, p.out.String())
	}
	p.checkRestored(t, before)
}

// choiceRowRes は、TestChoiceInPTY の問いの各行の先頭（CUP の 行;1H）に描かれた語に一致する。色を付ける列（SGR）が間に入ってよい。
var choiceRowRes = []*regexp.Regexp{
	regexp.MustCompile(`\x1b\[21;1H(\x1b\[[0-9;]*m)*エージェントが権限を求めている:`),
	regexp.MustCompile(`\x1b\[22;1H(\x1b\[[0-9;]*m)*\[1\]`),
	regexp.MustCompile(`\x1b\[23;1H(\x1b\[[0-9;]*m)*\[2\]`),
	regexp.MustCompile(`\x1b\[24;1H(\x1b\[[0-9;]*m)*1〜2`),
	regexp.MustCompile(`Allow`),
	regexp.MustCompile(`Reject`),
	regexp.MustCompile(`<Esc>`),
}

var (
	// outputTitleRe は、欄の名前の行の " [出力] " の後（23 行目の 9 列目）に "acp" を描いた出力に一致する。色を付ける列（SGR）が間に入ってよい。
	outputTitleRe = regexp.MustCompile(`\x1b\[23;9H(\x1b\[[0-9;]*m)*acp`)
	// signRe は、1 行目の先頭（CUP の 1;1H）に、印の列の "B" と空白に続いてバッファの行が描かれた出力に一致する。
	signRe = regexp.MustCompile(`\x1b\[1;1H(\x1b\[[0-9;]*m)*B (\x1b\[[0-9;]*m)*package`)
	// firstLineRe は、1 行目の先頭（CUP の 1;1H）に、バッファの 1 行目の "aaa" が描かれた出力に一致する。
	firstLineRe = regexp.MustCompile(`\x1b\[1;1H(\x1b\[[0-9;]*m)*aaa`)
)

// TestFitHeightInPTY は、出力の欄を閉じた後と、端末を小さくしてから大きくした後に、
// Vim 9.1 と同じく、カーソルの行が表示領域の中で前と同じ割合の位置に来るように描き直すことを確かめる（#135）。
// 20 行のファイルの最終行にカーソルを置くと、閉じた後と大きくした後は 1 行目から全体が出る（上の行を隠したまま "~" を並べない）。
func TestFitHeightInPTY(t *testing.T) {
	var text strings.Builder
	for c := 'a'; c < 'a'+20; c++ {
		text.WriteString(strings.Repeat(string(c), 3) + "\n")
	}
	path := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(path, []byte(text.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	p := startEditorIn(t, t.TempDir(), nil, path)
	before := ptytest.Termios(t, p.tty)
	p.out.WaitForString(t, "a.txt")
	// 欄を開くと表示領域は 11 行になり、カーソルの行（20 行目）が下から 2 行目に来るよう、11 行目から出す。
	p.write(t, "G:output\r")
	p.out.WaitForString(t, "[出力]")
	mark := len(p.out.String())
	p.write(t, ":outclose\r")
	p.out.WaitFor(t, "欄を閉じた後の 1 行目", func(out string) bool { return firstLineRe.MatchString(out[mark:]) })

	// 大きさが変わると、画面を消して全体を書き直す（TestEditResizeInPTY）。12 行では 12 行目から出し、24 行へ戻すと 1 行目から出す。
	clears := strings.Count(p.out.String(), "\x1b[2J")
	ptytest.SetSize(t, p.master, 80, 12)
	p.out.WaitFor(t, "12 行の画面の描き直し", func(out string) bool { return strings.Count(out, "\x1b[2J") >= clears+1 })
	ptytest.SetSize(t, p.master, 80, 24)
	p.out.WaitFor(t, "24 行へ戻した後の 1 行目", func(out string) bool {
		i := strings.LastIndex(out, "\x1b[2J")
		return strings.Count(out, "\x1b[2J") >= clears+2 && firstLineRe.MatchString(out[i:])
	})
	p.write(t, ":q\r")
	if err := p.wait(t); err != nil {
		t.Fatalf("エディタの終了 = %v。出力: %q", err, p.out.String())
	}
	p.checkRestored(t, before)
}

// TestSignColumnInPTY は、DAP の拡張機能を実際に起動し、:dap.toggleBreakpoint で置いたブレークポイントの印が、
// バッファの左の印の列に出ることを確かめる（基本設計 6.4、6.7）。ブレークポイントを置くだけなので、デバッグアダプターは起動しない。
//
// req: FR-31
func TestSignColumnInPTY(t *testing.T) {
	xdg := t.TempDir()
	installExtension(t, xdg, "dap")
	path := filepath.Join(t.TempDir(), "a.go")
	if err := os.WriteFile(path, []byte("package a\n\nfunc f() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := startEditorIn(t, xdg, nil, path)
	before := ptytest.Termios(t, p.tty)
	p.out.WaitForString(t, "a.go")
	p.write(t, ":dap.toggleBreakpoint\r")
	p.out.WaitFor(t, "ブレークポイントの印", func(out string) bool { return signRe.MatchString(out) })
	p.write(t, ":q\r")
	if err := p.wait(t); err != nil {
		t.Fatalf("エディタの終了 = %v。出力: %q", err, p.out.String())
	}
	p.checkRestored(t, before)
}
