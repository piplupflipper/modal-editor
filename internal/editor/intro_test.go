package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/option"
)

// startIntro は、80 列 24 行の端末で、起動の引数のファイルを files にして（無ければ引数なしで）エディタを起動する。
// 設定ファイルの場所は、手元の設定ファイルを見ないように、cfg.ExtManager.ConfigPath が空なら無いファイルの短いパス（noConfigPath）にする。
// 一時ディレクトリのパスはテストの名前を含んで長く、案内が 80 列に収まらないことがあるためである。
func startIntro(t *testing.T, cfg Config, files ...string) (*harness, string) {
	t.Helper()
	dir := writeFiles(t, map[string]string{"a.txt": "abc\n"})
	cfg.WorkDir = dir
	if cfg.ExtManager.ConfigPath == "" {
		cfg.ExtManager.ConfigPath = noConfigPath
	}
	term := newFakeTerm()
	term.w, term.h = 80, 24
	h := startTerm(t, term, cfg, files...)
	h.settle()
	return h, cfg.ExtManager.ConfigPath
}

// noConfigPath は、起動の案内のテストの、無い設定ファイルのパスである（startIntro）。
const noConfigPath = "/vi-imitation-intro-test/config.jsonc"

// introShown は、h の最後の画面に起動の案内の最初の行があるかを返す。
func introShown(h *harness) bool {
	h.t.Helper()
	h.settle()
	h.e.do(func() {}) // 最後のキーの後の描画を待つ
	return strings.Contains(h.term.screen(), "vi-imitation dev")
}

// 引数なしで起動したら、画面の中央に、名前と版、:q と :help の案内、設定ファイルの場所を出す。
// 設定ファイルが無ければ、置く場所として出す。
//
// req: FR-205
func TestIntroAtStartup(t *testing.T) {
	h, conf := startIntro(t, Config{})
	scr := h.term.screen()
	for _, want := range []string{"vi-imitation dev", ":q<Enter>     で終わる", ":help<Enter>  でヘルプを開く", "設定ファイル（まだ無い）", conf} {
		if !strings.Contains(scr, want) {
			t.Errorf("画面に %q が無い:\n%s", want, scr)
		}
	}
	rows := strings.Split(scr, "\n")
	if !strings.Contains(rows[1+12], "設定ファイル（まだ無い）") || !strings.Contains(rows[1+13], conf) {
		t.Errorf("設定ファイルの見出しとパスの行が違う:\n%s", scr)
	}
	// タブの行の下が表示領域（21 行）で、案内の 7 行を上に (21-7)/2 = 7 行を空けて置く。
	if !strings.HasPrefix(rows[1+7], "~") || !strings.Contains(rows[1+7], "vi-imitation dev") {
		t.Errorf("案内の最初の行の位置が違う:\n%s", scr)
	}
	// 同じ列から始めるコマンドの 2 行は、表示幅をそろえてあるので、同じ列から描く。
	q, help := rows[1+9], rows[1+10]
	if strings.Index(q, ":q") != strings.Index(help, ":help") {
		t.Errorf("コマンドの 2 行の列がそろっていない:\n%s\n%s", q, help)
	}

	// 設定ファイルがあれば、そのパスを出す。
	conf = filepath.Join(t.TempDir(), "config.jsonc")
	if err := os.WriteFile(conf, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	term := newFakeTerm()
	term.w, term.h = 200, 24 // 一時ディレクトリのパスが長くても収まる幅にする
	h2 := startTerm(t, term, Config{WorkDir: t.TempDir(), ExtManager: ExtManagerConfig{ConfigPath: conf}})
	h2.settle()
	if scr := h2.term.screen(); !strings.Contains(scr, conf) || strings.Contains(scr, "まだ無い") || !strings.Contains(scr, "設定ファイル") {
		t.Errorf("設定ファイルがあるときの画面:\n%s", scr)
	}
}

// 起動の引数にファイルを書いたら出さない。設定 intro.show が偽なら、引数なしでも出さない。
//
// req: FR-205, FR-206
func TestIntroNotShown(t *testing.T) {
	h, _ := startIntro(t, Config{}, "a.txt")
	if introShown(h) {
		t.Errorf("ファイルを開いて起動したのに出した:\n%s", h.term.screen())
	}
	h, _ = startIntro(t, Config{Options: map[string]option.Value{option.IntroShow: option.BoolValue(false)}})
	if introShown(h) {
		t.Errorf("intro.show が偽なのに出した:\n%s", h.term.screen())
	}
}

// 案内は、Neovim 0.12.5 の intro と同じく、インサートモードに入っても、Ex コマンドを打っても、設定を変えても、
// 空振りの操作をしても消えない。バッファを変える、別のバッファを開く、フォーカスのあるポップアップを開く、
// 出力の欄を開く、2 つ目の client が繋ぐ、intro.show を偽にすると消え、その後に元へ戻しても出さない。
//
// req: FR-205, FR-206
func TestIntroDismiss(t *testing.T) {
	stays := []string{"i", "<Esc>", "v<Esc>", ":option.set lineNumber.show true<CR>", "l", "\"ap", ":messages<CR>"}
	h, _ := startIntro(t, Config{})
	for _, k := range stays {
		h.keys(k)
		if !introShown(h) {
			t.Fatalf("%q の後に消えた:\n%s", k, h.term.screen())
		}
	}

	cases := []struct {
		name string
		keys string // 消す操作
		back string // 条件を元へ戻す操作。戻しても出さない
	}{
		{"文字を打って消す", "ix", "<BS><Esc>"},
		{"文字を打ってアンドゥする", "ix<Esc>", "u"},
		{"行を足す", "yyp", "u"},
		{"ファイルを開く", ":e a.txt<CR>", ":b1<CR>"},
		{"タブを足す", ":tab.new<CR>", ":bdelete<CR>"},
		{"名前を付ける", ":w b.txt<CR>", ""},
		{"ヘルプのポップアップを開く", ":help<CR>", "<Esc>"},
		{"出力の欄を開く", ":output<CR>", ":outclose<CR>"},
		{"intro.show を偽にする", ":option.set intro.show false<CR>", ":option.set intro.show true<CR>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hc, _ := startIntro(t, Config{})
			if !introShown(hc) {
				t.Fatalf("起動したときに出していない:\n%s", hc.term.screen())
			}
			hc.keys(c.keys)
			if introShown(hc) {
				t.Fatalf("%q の後も出している:\n%s", c.keys, hc.term.screen())
			}
			if c.back != "" {
				hc.keys(c.back)
			}
			if introShown(hc) {
				t.Errorf("%q で戻した後に出した:\n%s", c.back, hc.term.screen())
			}
		})
	}

	// 2 つ目の client が繋いだら消え、その client が終わっても出さない。
	h, _ = startIntro(t, Config{})
	p := h.attach(ClientInfo{ID: 2, WorkDir: h.e.cfg.WorkDir})
	h.keys("l") // 最初の client の画面を描き直させる
	if introShown(h) {
		t.Errorf("2 つ目の client が繋いだ後も出している:\n%s", h.term.screen())
	}
	if strings.Contains(p.term.screen(), "vi-imitation dev") {
		t.Errorf("2 つ目の client の画面に出した:\n%s", p.term.screen())
	}
	p.term.closeInput()
	h.eventually("2 つ目の client が終わる", func() bool { return len(h.e.clients) == 1 })
	h.keys("l")
	if introShown(h) {
		t.Errorf("2 つ目の client が終わった後に出した:\n%s", h.term.screen())
	}
}

// フォーカスを入れないポップアップ（ホバーに当たる、カーソルの近くのテキストの部品）は、案内を消さない（Neovim の one_window が
// 浮かべた窓を数えないのと同じ）。フォーカスを入れるポップアップ（中央の一覧）は消す。
//
// req: FR-205
func TestIntroPopup(t *testing.T) {
	h, _ := startIntro(t, Config{Extensions: fakeSpecs(t)})
	h.keys(":fake.popup text<CR>")
	h.eventually("テキストのポップアップ", func() bool { return h.e.float != nil && h.e.float.ext == "fake" })
	var off, focused bool
	h.e.do(func() { off, focused = h.e.intro.off, h.e.float.focused })
	if off || focused || !introShown(h) {
		t.Fatalf("フォーカスを入れないポップアップで消えた（off=%v focused=%v）:\n%s", off, focused, h.term.screen())
	}
	h.keys("l") // テキストのポップアップは次のキーで閉じる
	h.keys(":fake.popup list<CR>")
	h.eventually("一覧のポップアップ", func() bool { return h.e.float != nil && h.e.float.focused })
	h.keys("<Esc>")
	if introShown(h) {
		t.Errorf("フォーカスを入れるポップアップを閉じた後に出した:\n%s", h.term.screen())
	}
}

// :intro は、条件によらず、表示領域を空白で塗ってから案内を出し、次のキーを 1 つ受けて消す。そのキーはコマンドとして使わない。
// 引数を付けたら E488 にする。
//
// req: FR-207
func TestIntroCommand(t *testing.T) {
	h, _ := startIntro(t, Config{}, "a.txt")
	h.keys(":intro<CR>")
	if !introShown(h) {
		t.Fatalf(":intro で出していない:\n%s", h.term.screen())
	}
	if scr := h.term.screen(); strings.Contains(scr, "abc") {
		t.Errorf(":intro の案内の下にバッファの行が残っている:\n%s", scr)
	}
	h.keys("x")
	if introShown(h) {
		t.Errorf("次のキーの後も出している:\n%s", h.term.screen())
	}
	if got := h.text(); got != "abc" {
		t.Errorf("案内を消したキーをコマンドとして使った: text = %q", got)
	}
	h.keys("x")
	h.settle()
	if got := h.text(); got != "bc" {
		t.Errorf("案内を消した後のキー: text = %q", got)
	}

	// 省略形と、引数の誤り。
	h.keys(":int<CR>")
	if !introShown(h) {
		t.Errorf(":int で出していない:\n%s", h.term.screen())
	}
	h.keys("<Esc>:intro x<CR>")
	h.settle()
	if msg := h.message(); msg != "E488: 余分な文字があります: x" {
		t.Errorf(":intro x のメッセージ = %q", msg)
	}
	if introShown(h) {
		t.Errorf(":intro x で出した:\n%s", h.term.screen())
	}

	// 起動したときの案内を出している間の :intro も、次のキーで消え、そのキーは使わない。消えた後も、条件を満たす間は起動の案内を出す。
	h2, _ := startIntro(t, Config{})
	h2.keys(":intro<CR>i")
	if !introShown(h2) {
		t.Errorf("起動の案内が消えた:\n%s", h2.term.screen())
	}
	h2.keys("a")
	h2.settle()
	if got := h2.text(); strings.Contains(got, "a") {
		t.Errorf(":intro の後の i を使った: text = %q", got)
	}
}

// 設定ファイルのパスは、ホームディレクトリの下なら ~/ からの形にする。
func TestShortHome(t *testing.T) {
	for _, c := range []struct{ path, home, want string }{
		{"/home/u/.config/vi-imitation/config.jsonc", "/home/u", "~/.config/vi-imitation/config.jsonc"},
		{"/home/u/.config/x", "/home/u/", "~/.config/x"},
		{"/home/u", "/home/u", "~"},
		{"/home/user2/x", "/home/u", "/home/user2/x"},
		{"/etc/x", "/home/u", "/etc/x"},
		{"/etc/x", "", "/etc/x"},
	} {
		if got := shortHome(c.path, c.home); got != c.want {
			t.Errorf("shortHome(%q, %q) = %q, want %q", c.path, c.home, got, c.want)
		}
	}
}
