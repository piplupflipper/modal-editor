package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/piplupflipper/modal-editor/internal/term/ptytest"
)

// gitEnv は、利用者の git の設定を読まずに、一時のリポジトリでコミットできる環境変数である。
func gitEnv() []string {
	return []string{
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
	}
}

// :!git commit で、git が起動したエディタ（環境変数 GIT_EDITOR の modal-editor）が待つだけの client になり、
// コミットのメッセージを :! を打った client のタブで開き、:wq で閉じると git がコミットを続ける（FR-82、FR-83、FR-117）。
// :! は端末を子プロセスへ渡し、終わったら「続けるには ENTER を押してください」で待ち、<CR> で画面を描き直す（FR-80）。
// 端末を渡している間の <C-c> は子プロセスにだけ効き、エディタは終わらない。
//
// req: FR-80, FR-82, FR-83, FR-117
func TestHandoverInPTY(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git が無い")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...) //nolint:gosec // テストが一時のリポジトリを作る git の引数である
		cmd.Dir, cmd.Env = dir, append(os.Environ(), gitEnv()...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "a.txt")

	p := startEditorIn(t, t.TempDir(), gitEnv(), filepath.Join(dir, "a.txt"))
	before := ptytest.Termios(t, p.tty)
	p.out.WaitForString(t, "a.txt")

	// :!{cmd}: 出力の後に問いを出し、<CR> で描き直す。
	p.write(t, ":!echo handed-$((40+2))\r")
	p.out.WaitForString(t, "handed-42")
	p.out.WaitForString(t, "続けるには ENTER を押してください")
	mark := len(p.out.String())
	p.write(t, "\r")
	p.out.WaitFor(t, "描き直し", func(out string) bool { return strings.Contains(out[mark:], "hello") })

	// 端末を渡している間の <C-c> は子プロセスを止め、エディタは続く。
	p.write(t, ":!sleep 30\r")
	p.out.WaitFor(t, "sleep を起動", func(string) bool { return childNamed(t, p.cmd.Process.Pid, "sleep") })
	mark = len(p.out.String())
	p.write(t, "\x03")
	p.out.WaitFor(t, "<C-c> の後の問い", func(out string) bool {
		return strings.Contains(out[mark:], "続けるには ENTER を押してください")
	})
	p.write(t, "\r")

	// :!git commit。メッセージのタブで書いて :wq すると、git がコミットを続ける。
	// エディタの作業ディレクトリはテストのもの（このリポジトリの中）なので、一時のリポジトリへ移ってからコミットする。
	mark = len(p.out.String())
	p.write(t, ":!cd '"+dir+"' && git commit\r")
	p.out.WaitFor(t, "コミットのメッセージのタブ", func(out string) bool { return strings.Contains(out[mark:], "COMMIT_EDITMSG") })
	p.write(t, "ggOfrom the handed-over terminal\x1b")
	p.write(t, ":wq\r")
	p.out.WaitFor(t, "コミットの後の問い", func(out string) bool {
		rest := out[mark:]
		return strings.Contains(rest, "from the handed-over terminal") && strings.Contains(rest, "続けるには ENTER を押してください")
	})
	p.write(t, "\r:q\r")
	if err := p.wait(t); err != nil {
		t.Fatalf("エディタの終了 = %v。出力: %q", err, p.out.String())
	}
	p.checkRestored(t, before)
	cmd := exec.Command("git", "log", "--format=%s")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil || strings.TrimSpace(string(out)) != "from the handed-over terminal" {
		t.Errorf("git log = %q, %v", out, err)
	}
}

// --new を付けた modal-editor は、MODAL_EDITOR_SOCKET があっても親の server に繋がず、新しい server と client を起動する（FR-84）。
// :! の中で起動すると、渡された端末でエディタとして動き、終わると :! の問いに戻る。
//
// req: FR-84
func TestNewInPTY(t *testing.T) {
	dir := t.TempDir()
	b := filepath.Join(dir, "b.txt")
	p := startEditor(t, filepath.Join(dir, "a.txt"))
	before := ptytest.Termios(t, p.tty)
	p.out.WaitForString(t, "a.txt")
	mark := len(p.out.String())
	p.write(t, ":!'"+os.Args[0]+"' --new '"+b+"'\r")
	// 入れ子のエディタの画面には、親のタブ（a.txt）が無い。待つだけの client なら、親が端末を取り戻して a.txt と b.txt のタブを描く。
	p.out.WaitFor(t, "入れ子のエディタ", func(out string) bool {
		rest := out[mark:]
		i := strings.LastIndex(rest, "\x1b[?1049h")
		return i >= 0 && strings.Contains(rest[i:], "b.txt") && strings.Contains(rest[i:], "NORMAL")
	})
	if rest := p.out.String()[mark:]; strings.Contains(rest[strings.LastIndex(rest, "\x1b[?1049h"):], "a.txt") {
		t.Errorf("入れ子のエディタが親の server に繋いだ: %q", rest)
	}
	p.write(t, "inested\x1b:wq\r")
	p.out.WaitFor(t, "入れ子のエディタの後の問い", func(out string) bool {
		return strings.Contains(out[mark:], "続けるには ENTER を押してください")
	})
	if got, err := os.ReadFile(b); err != nil || string(got) != "nested\n" {
		t.Errorf("b.txt = %q, %v", got, err)
	}
	p.write(t, "\r:q\r")
	if err := p.wait(t); err != nil {
		t.Fatalf("エディタの終了 = %v。出力: %q", err, p.out.String())
	}
	p.checkRestored(t, before)
}

// childNamed は、プロセス pid の子孫に、コマンドの名前が name のプロセスがあるかを返す。
func childNamed(t *testing.T, pid int, name string) bool {
	t.Helper()
	ents, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatal(err)
	}
	parent := map[int]int{}
	names := map[int]string{}
	for _, e := range ents {
		n, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join("/proc", e.Name(), "stat"))
		if err != nil {
			continue
		}
		s := string(b)
		i, j := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
		if i < 0 || j < i {
			continue
		}
		names[n] = s[i+1 : j]
		// ')' の後は、状態、親のプロセス ID の順である。
		if f := strings.Fields(s[j+1:]); len(f) > 1 {
			if pp, err := strconv.Atoi(f[1]); err == nil {
				parent[n] = pp
			}
		}
	}
	for n, nm := range names {
		if nm != name {
			continue
		}
		for q := parent[n]; q > 1; q = parent[q] {
			if q == pid {
				return true
			}
		}
	}
	return false
}

// 権限の不足で書けないファイルへの :w は、権限を上げて書くかを問い、y で設定の elevateCommand（ここでは偽の sudo）に tee を付けて動かす。
// 偽の sudo は、標準入力がバッファの中身のパイプでも、パスワードを端末（/dev/tty）から読める（FR-79）。本物の sudo は打たない。
//
// req: FR-79
func TestElevatedSaveInPTY(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root は権限の不足にならない")
	}
	dir := t.TempDir()
	ro := filepath.Join(dir, "ro")
	if err := os.Mkdir(ro, 0o700); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(ro, "f.txt")
	// ファイルも書けなくする（ファイルが書けてディレクトリだけが書けないなら、権限を上げずにその場で書くため）。
	if err := os.WriteFile(f, []byte("old\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(ro, 0o500); err != nil { //nolint:gosec // テストの一時ディレクトリの書き込みを外して、権限の不足を起こす
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ro, 0o700) }) //nolint:gosec // 一時ディレクトリを消せるように書き込みを戻すだけである
	fake := filepath.Join(dir, "fake-sudo")
	script := "#!/bin/sh\nprintf 'fake password: ' >/dev/tty\nIFS= read -r pw </dev/tty\nprintf '%s' \"$pw\" >\"$FAKE_LOG\"\n" +
		"chmod u+w \"$FAKE_DIR\" \"$FAKE_DIR\"/*\n\"$@\"\nrc=$?\nchmod u-w \"$FAKE_DIR\" \"$FAKE_DIR\"/*\nexit $rc\n"
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil { //nolint:gosec // テストが起動する偽の sudo である
		t.Fatal(err)
	}
	log := filepath.Join(dir, "log")
	xdg := t.TempDir()
	cfgDir := filepath.Join(xdg, "config", "modal-editor")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.jsonc"), []byte(`{"elevateCommand": [`+quoteJSON(fake)+`]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p := startEditorIn(t, xdg, []string{"FAKE_LOG=" + log, "FAKE_DIR=" + ro}, f)
	before := ptytest.Termios(t, p.tty)
	p.out.WaitForString(t, "f.txt")
	p.write(t, "ccnew\x1b:w\r")
	p.out.WaitForString(t, "権限を上げて書き込みますか (y/n)?")
	p.write(t, "y")
	p.out.WaitForString(t, "fake password: ")
	mark := len(p.out.String())
	p.write(t, "secret\r")
	// 書き込んだら、未保存の印（[+]）の無い画面に描き直す。
	// 端末を取り戻した client の大きさの読み直し（Resize）と、書き込みの結果（TerminalDone）は別の goroutine から server へ届く。
	// Resize が先に届くと、server は保存済みにする前の画面（[+] あり）を全体で描き、その後に [+] を消す差分を描く（#241）。
	// どちらの順でも最後の画面に [+] が無ければよいので、代替画面に入った後の出力を画面に当ててから調べる。
	p.out.WaitFor(t, "書き込んだ後の描き直し", func(out string) bool {
		b, _ := os.ReadFile(f)
		i := strings.LastIndex(out, "\x1b[?1049h")
		if string(b) != "new\n" || i < mark {
			return false
		}
		v := newVScreen(80, 24)
		v.feed([]byte(out[i:]))
		return strings.Contains(v.row(1), "new") && !strings.Contains(v.String(), "[+]")
	})
	if b, err := os.ReadFile(log); err != nil || string(b) != "secret" {
		t.Errorf("偽の sudo が端末から読んだパスワード = %q, %v", b, err)
	}
	// 保存済みなので、:q で終われる（E37 にならない）。
	p.write(t, ":q\r")
	if err := p.wait(t); err != nil {
		t.Fatalf("エディタの終了 = %v。出力: %q", err, p.out.String())
	}
	p.checkRestored(t, before)
}

// quoteJSON は、s を JSON の文字列にする。
func quoteJSON(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
