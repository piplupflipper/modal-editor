package editor

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/option"
	"github.com/piplupflipper/vi-imitation/internal/ui"
)

// completionFiles は、ファイルの補完を試すディレクトリの中身である。Neovim 0.12.5 と比べたディレクトリ（PR の説明）と同じ形にした。
var completionFiles = map[string]string{
	"Alpha.txt": "", "alpha2.go": "", "alpine.md": "", ".hidden": "", "sp ace.txt": "",
	"foo.c": "", "foo.h": "", "foo.o": "", "beta/one.txt": "", "beta/two.txt": "", ".hdir/x": "",
}

// selAttr は、試験で色の名前 popup.selected に付ける色である。
var selAttr = ui.Attr{Bg: ui.RGB(9, 9, 9)}

// startCompletion は、files を置いた作業ディレクトリで、40 列 10 行の偽の端末で起動する。options は設定の値である。
func startCompletion(t *testing.T, files map[string]string, options map[string]option.Value) *harness {
	t.Helper()
	dir := writeFiles(t, files)
	term := newFakeTerm()
	term.w, term.h = 40, 10
	h := startTerm(t, term, Config{WorkDir: dir, Options: options, Colors: map[string]ui.Attr{ui.StylePopupSelected: selAttr, ui.StylePopupMatch: {}}})
	h.settle()
	return h
}

// cmdline は、カーソルのあるバッファのコマンドラインの文字列を返す。
func (h *harness) cmdline() string {
	var s string
	h.e.do(func() { s = h.e.cur.state.CommandLine() })
	return s
}

// menu は、出している候補の一覧の項目と選んでいる項目を、"a [b] c" の形で返す。出していなければ空を返す。
func (h *harness) menu() string {
	var s string
	h.e.do(func() {
		m := h.e.wildMenuFrame(h.e.cur)
		if m == nil {
			return
		}
		items := slices.Clone(m.Items)
		if m.Selected >= 0 {
			items[m.Selected] = "[" + items[m.Selected] + "]"
		}
		s = strings.Join(items, " ")
	})
	return s
}

// waitCmdline は、コマンドラインが line に、候補の一覧が menu になるまで待つ。
func (h *harness) waitCmdline(what, line, menu string) {
	h.t.Helper()
	h.settle()
	h.eventually(what, func() bool {
		m := h.e.wildMenuFrame(h.e.cur)
		got := ""
		if m != nil {
			items := slices.Clone(m.Items)
			if m.Selected >= 0 {
				items[m.Selected] = "[" + items[m.Selected] + "]"
			}
			got = strings.Join(items, " ")
		}
		return h.e.cur.state.CommandLine() == line && got == menu
	})
}

// 本体の既定（commandLine.completeMode が full。Vim と Neovim の既定）では、<Tab> で最初の候補を入れて一覧を出し、
// 続く <Tab> と <C-n> で次の候補、<S-Tab> と <C-p> で前の候補を入れる。最後の候補の次は打った語に戻る。
// <C-e> は打った語に戻して一覧を閉じ、<C-y> は入れている候補のまま閉じる。候補が 1 つなら、入れて一覧を出さない。
// ファイルの候補は名前のバイトの順で、隠しファイルは語が "." で始まるときだけ出し、Vim の 'suffixes' の既定に当たるファイルは後ろへ回す。
// ディレクトリには "/" を付け、名前の空白の前には "\" を付けて入れ、<CR> で開くファイルは空白を含む名前になる（Neovim 0.12.5 と同じ）。
//
// req: FR-195, FR-196, FR-198, FR-199
func TestCmdlineCompleteFull(t *testing.T) {
	h := startCompletion(t, completionFiles, nil)
	h.keys(":e al<Tab>")
	h.waitCmdline(":e al<Tab>", "e alpha2.go", "[alpha2.go] alpine.md")
	for _, tt := range []struct{ keys, line, menu string }{
		{"<Tab>", "e alpine.md", "alpha2.go [alpine.md]"},
		{"<Tab>", "e al", "alpha2.go alpine.md"},
		{"<C-n>", "e alpha2.go", "[alpha2.go] alpine.md"},
		{"<C-p>", "e al", "alpha2.go alpine.md"},
		{"<S-Tab>", "e alpine.md", "alpha2.go [alpine.md]"},
		{"<Up>", "e alpha2.go", "[alpha2.go] alpine.md"},
		{"<Down>", "e alpine.md", "alpha2.go [alpine.md]"},
		{"<Left>", "e alpha2.go", "[alpha2.go] alpine.md"},
		{"<Right>", "e alpine.md", "alpha2.go [alpine.md]"},
		{"<C-e>", "e al", ""},
		{"<Tab><C-y>", "e alpha2.go", ""},
		{"x", "e alpha2.gox", ""},
	} {
		h.keys(tt.keys)
		h.waitCmdline(tt.keys, tt.line, tt.menu)
	}
	h.keys("<Esc>:e b<Tab>")
	h.waitCmdline(":e b<Tab>", "e beta/", "")
	h.keys("<Tab>")
	h.waitCmdline(":e beta/<Tab>", "e beta/one.txt", "[one.txt] two.txt")
	h.keys("<Esc>:e .<Tab>")
	h.waitCmdline(":e .<Tab>", "e ./", "[./] ../ .hdir/ .hidden")
	h.keys("<Esc>:e f<Tab>")
	h.waitCmdline(":e f<Tab>", "e foo.c", "[foo.c] foo.h foo.o")
	h.keys("<Esc>:e <Tab>")
	h.waitCmdline(":e <Tab>", "e Alpha.txt", "[Alpha.txt] alpha2.go alpine.md beta/ foo.c sp\\ ace.txt foo.h foo.o")
	h.keys("<Esc>:e s<Tab>")
	h.waitCmdline(":e s<Tab>", `e sp\ ace.txt`, "")
	h.keys("<CR>")
	h.settle()
	var name string
	h.e.do(func() { name = h.e.bufferName(h.e.cur) })
	if name != "sp ace.txt" {
		t.Errorf("開いたバッファ = %q", name)
	}
	// 候補の無い語では何も入れず、補うものの無い所（:q の引数）ではタブ文字が入る（Neovim と同じ）。
	h.keys(":e zz<Tab>")
	h.waitCmdline(":e zz<Tab>", "e zz", "")
	h.keys("<Esc>:q <Tab>")
	h.waitCmdline(":q <Tab>", "q \t", "")
	h.keys("<Esc>")
}

// オーナーの nvim の設定（longest:full,full と、大文字と小文字を区別しない）では、1 回目の <Tab> で共通の先頭まで入れて、
// 何も選んでいない一覧を出し、2 回目からの <Tab> で候補を順に入れる。共通の先頭は大文字と小文字を区別して数えるので、
// a に Alpha.txt と alpha2.go が当たると、語は伸びない（Neovim 0.12.5 と同じ）。
// 続けて打った <Tab><Tab><C-y> は、1 回目の <Tab> の候補を集め終える前に届いても、集め終えてから順に効く。
//
// req: FR-196, FR-198, FR-199
func TestCmdlineCompleteLongest(t *testing.T) {
	h := startCompletion(t, completionFiles, map[string]option.Value{
		option.CommandLineCompleteMode:       option.StringValue("longest:full,full"),
		option.CommandLineCompleteIgnoreCase: option.BoolValue(true),
	})
	h.keys(":e a<Tab>")
	h.waitCmdline(":e a<Tab>", "e a", "Alpha.txt alpha2.go alpine.md")
	h.keys("<Tab>")
	h.waitCmdline(":e a<Tab><Tab>", "e Alpha.txt", "[Alpha.txt] alpha2.go alpine.md")
	h.keys("<Esc>:e alp<Tab>")
	h.waitCmdline(":e alp<Tab>", "e alp", "Alpha.txt alpha2.go alpine.md")
	h.keys("<Esc>:e B<Tab>")
	h.waitCmdline(":e B<Tab>", "e beta/", "")
	h.keys("<Tab><Tab><C-y>")
	h.waitCmdline(":e beta/<Tab><Tab><C-y>", "e beta/one.txt", "")
	h.keys("<Esc>")
}

// マクロを記録している間に補うと、補った後の文字が記録に入り（<Tab> は入らない）、再生すると同じコマンドラインになる。
// 再生の中の <Tab> は補わずにタブ文字を入れる（Vim と同じ）。
//
// req: FR-195
func TestCmdlineCompleteMacro(t *testing.T) {
	h := startCompletion(t, map[string]string{"alpha.txt": "a\n", "beta.txt": "b\n"}, nil)
	h.keys("qa:e al<Tab>")
	h.waitCmdline(":e al<Tab>", "e alpha.txt", "")
	h.keys("<CR>q")
	h.settle()
	var reg string
	h.e.do(func() {
		r, _ := h.e.cur.state.Registers().Get('a')
		reg = r.Text
	})
	if want := ":e al" + "pha.txt" + "\r"; reg != want {
		t.Errorf(`"a = %q, want %q`, reg, want)
	}
	h.keys(":e beta.txt<CR>@a")
	h.settle()
	h.e.do(func() { reg = h.e.bufferName(h.e.cur) })
	if reg != "alpha.txt" {
		t.Errorf("@a の後のバッファ = %q", reg)
	}
}

// コマンドの名前、:set と :option.set の設定の名前と値、バッファ、:help の語、:ext の引数を補う。
// コマンドの名前には、本体が同じコマンドのために足した短い名前（:h、:mes）を出さない。
//
// req: FR-195
func TestCmdlineCompleteKinds(t *testing.T) {
	h := startCompletion(t, map[string]string{"alpha.txt": "a\n", "dir/alpine.txt": "b\n"}, nil)
	h.keys(":e alpha.txt<CR>:e dir/alpine.txt<CR>")
	h.settle()
	for _, tt := range []struct{ keys, line, menu string }{
		{":he<Tab>", "help", ""},
		{":mes<Tab>", "messages", ""},
		{":ex<Tab>", "ext", ""},
		{":ext <Tab>", "ext sync", "[sync] update"},
		{":set nu<Tab>", "set number", "[number] numberwidth"},
		{":set wildm<Tab>", "set wildmode", ""},
		{":set wildmode=<Tab>", "set wildmode=full", "[full] longest noselect"},
		{":option.set commandLine.completeMode l<Tab>", "option.set commandLine.completeMode longest", ""},
		{":option.toggle mouse.<Tab>", "option.toggle mouse.enabled", ""},
		{":b al<Tab>", "b alpha.txt", "[alpha.txt] dir/alpine.txt"},
		{":b pine<Tab>", "b dir/alpine.txt", ""},
		{":help :option.se<Tab>", "help :option.set", ""},
	} {
		h.keys(tt.keys)
		h.waitCmdline(tt.keys, tt.line, tt.menu)
		h.keys("<Esc>")
	}
}

// 候補の一覧は、コマンドラインの行の真上に、補う語の列から並べ、その 1 列左に空白を置く。選んでいる項目は popup.selected の色にする。
// 一覧の幅は、項目の幅と 15（Vim の 'pumwidth'）の大きい方である。completionMenu.height で行の数を限る。
//
// req: FR-197
func TestCmdlineCompleteScreen(t *testing.T) {
	files := map[string]string{}
	for _, n := range []string{"a1", "a2", "a3", "a4", "a5"} {
		files[n] = ""
	}
	h := startCompletion(t, files, map[string]option.Value{option.CompletionMenuHeight: option.IntValue(3)})
	h.keys(":e a<Tab>")
	h.waitCmdline(":e a<Tab>", "e a1", "[a1] a2 a3 a4 a5")
	h.eventually("一覧を描く", func() bool { return strings.HasPrefix(h.screenRow(6), "~  a1") })
	for row, want := range map[int]string{6: "~  a1", 7: "~  a2"} {
		if got := h.screenRow(row); !strings.HasPrefix(got, want) {
			t.Errorf("%d 行目 = %q, want %q で始まる", row, got, want)
		}
	}
	if got := h.screenRow(8); !strings.HasPrefix(got[2:], " a3") {
		t.Errorf("8 行目 = %q", got)
	}
	if got := searchMarks(h.term, 6, selAttr); got != "  "+strings.Repeat("^", 16) {
		t.Errorf("選んでいる項目の色 = %q", got)
	}
	if got := h.screenRow(9); got != ":e a1" {
		t.Errorf("コマンドラインの行 = %q", got)
	}
	h.keys("<Esc>")
}

// Ex コマンドのファイルの引数の、先頭の "~" と環境変数を展開する（Vim と Neovim と同じ）。引けない環境変数はそのまま残す。
//
// req: FR-200
func TestExpandFilePath(t *testing.T) {
	env := func(name string) (string, bool) {
		switch name {
		case "HOME":
			return "/h", true
		case "D":
			return "x y", true
		}
		return "", false
	}
	for _, tt := range []struct{ in, want string }{
		{"~", "/h/me"},
		{"~/a", "/h/me/a"},
		{"~x/a", "~x/a"},
		{"a~/b", "a~/b"},
		{"$HOME/a", "/h/a"},
		{"${HOME}/a", "/h/a"},
		{"$D/$NOSUCH/a", "x y/$NOSUCH/a"},
		{"a$", "a$"},
		{"${HOME", "${HOME"},
	} {
		if got := expandFilePath(tt.in, "/h/me", env); got != tt.want {
			t.Errorf("expandFilePath(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// :e ~/a と :w $名前/b は、ホームディレクトリと環境変数を展開したパスを開いて書く。
//
// req: FR-200
func TestEditExpandsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("VI_IMITATION_TEST_DIR", home)
	if err := os.WriteFile(filepath.Join(home, "a.txt"), []byte("home\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := startCompletion(t, nil, nil)
	h.keys(":e ~/a.txt<CR>")
	h.settle()
	if got := h.text(); got != "home" {
		t.Fatalf("text = %q", got)
	}
	h.keys(":w $VI_IMITATION_TEST_DIR/b.txt<CR>")
	h.settle()
	if data, err := os.ReadFile(filepath.Join(home, "b.txt")); err != nil || string(data) != "home\n" {
		t.Errorf("b.txt = %q, %v", data, err)
	}
	// :e ~/ の後の <Tab> は "~/" を残してホームディレクトリの中を補う。
	h.keys(":e ~/a<Tab>")
	h.waitCmdline(":e ~/a<Tab>", "e ~/a.txt", "")
	h.keys("<Esc>")
}

// ファイルの候補を集めている間は入力を止めない。集めている間に打った補完のキーは、集め終えてから打った順に効き、
// ほかのキーは集めるのを取り消してから働く（取り消した後に届いた候補は使わない）。
//
// req: FR-199
func TestCmdlineCompleteSlowFiles(t *testing.T) {
	h := startCompletion(t, completionFiles, nil)
	release := make(chan struct{})
	started := make(chan struct{}, 4)
	h.e.do(func() {
		h.e.wildCollect = func(ctx context.Context, q fileQuery) ([]string, []string, error) {
			started <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
			}
			return collectFiles(context.Background(), q)
		}
	})
	h.keys(":e al<Tab><Tab><C-y>")
	<-started
	h.settle()
	if got := h.cmdline(); got != "e al" {
		t.Fatalf("集めている間のコマンドライン = %q", got)
	}
	release <- struct{}{}
	h.waitCmdline("集め終えた後", "e alpine.md", "")
	h.keys("<Esc>:e al<Tab>")
	<-started
	h.keys("x")
	h.waitCmdline("集めている間に打った x", "e alx", "")
	close(release)
	h.settle()
	h.e.do(func() {})
	if got, menu := h.cmdline(), h.menu(); got != "e alx" || menu != "" {
		t.Errorf("取り消した後 = %q、一覧 = %q", got, menu)
	}
	h.keys("<Esc>")
}

// ファイルの候補を集める途中で取り消されたら、誤りを返して候補を返さない。読めないディレクトリも誤りにする。
//
// req: FR-199
func TestCollectFilesCancel(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a": "", "b": ""})
	q := fileQuery{word: "", workDir: dir, env: envLookup(nil)}
	items, _, err := collectFiles(context.Background(), q)
	if err != nil || !slices.Equal(items, []string{"a", "b"}) {
		t.Fatalf("collectFiles = %q, %v", items, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if items, _, err := collectFiles(ctx, q); err == nil || items != nil {
		t.Errorf("取り消した collectFiles = %q, %v", items, err)
	}
	q.word = "nosuch/"
	if _, _, err := collectFiles(context.Background(), q); err == nil {
		t.Error("無いディレクトリで誤りにならない")
	}
}
