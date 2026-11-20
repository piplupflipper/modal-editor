package editor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/piplupflipper/vi-imitation/ext"
	"github.com/piplupflipper/vi-imitation/internal/command"
	"github.com/piplupflipper/vi-imitation/internal/command/ex"
	"github.com/piplupflipper/vi-imitation/internal/config"
	"github.com/piplupflipper/vi-imitation/internal/exthost"
	"github.com/piplupflipper/vi-imitation/internal/input"
	"github.com/piplupflipper/vi-imitation/internal/option"
	"github.com/piplupflipper/vi-imitation/internal/ui"
)

// 設定の keymap の右辺に Ex コマンド（{ "command": "<正規の名前>", "args": "<引数>" }）を書くと、そのキーは、
// コマンドラインを出さずに、コマンドラインで打ったときと同じくそのコマンドを実行する。
// 引数の無いコマンド（search.clearHighlight）、引数のあるコマンド（option.toggle lineNumber.show）、拡張機能のコマンド（fake.echo）を呼べる。
// 使えない名前（別名、正規の名前の無いコマンド、知らない名前）と、操作の項目と同じキーになる項目は、起動のときに設定の誤りとして知らせる。
//
// req: FR-154, FR-155
func TestKeymapCommandConfig(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "foo bar\nbar foo\n"})
	hl := ui.Attr{Bg: ui.RGB(1, 2, 3)}
	h := start(t, Config{
		WorkDir: dir, Extensions: fakeSpecs(t),
		Options:       map[string]option.Value{option.SearchHighlight: option.BoolValue(true)},
		Colors:        map[string]ui.Attr{ui.StyleSearch: hl},
		KeymapActions: map[string]map[string]string{"normal": {"<Leader>a": "line-end"}},
		KeymapCommands: map[string]map[string]config.KeymapCommand{
			"normal": {
				"<C-l>":     {Name: "search.clearHighlight"},
				"<Leader>n": {Name: "option.toggle", Args: "lineNumber.show"},
				"<C-e>":     {Name: "fake.echo", Args: "a | b"},
				"<C-k>":     {Name: "option.toggle", Args: "filetype"},
				"<C-x>":     {Name: "nohlsearch"},
				"<C-y>":     {Name: "w"},
				"<C-z>":     {Name: "nosuch.cmd"},
				"<Space>a":  {Name: "tab.new"},
			},
			"insert": {"<C-l>": {Name: "search.clearHighlight"}},
		},
	}, "a.txt")
	if msg := h.message(); !strings.HasPrefix(msg, "設定の誤り 4 件") {
		t.Errorf("起動の message = %q", msg)
	}
	var warnings string
	h.e.do(func() {
		for _, m := range h.e.msgHistory {
			warnings += m.Text + "\n"
		}
	})
	for _, want := range []string{
		"keymap.normal.<C-x>: nohlsearch は別名なので書けない（正規の名前 search.clearHighlight を書く）",
		"keymap.normal.<C-y>: w は正規の名前の無い Ex コマンド（:write）",
		"keymap.normal.<C-z>: nosuch.cmd は、本体の Ex コマンドの正規の名前でも拡張機能のコマンドの ID でもない",
		`keymap.normal: "<Space>a" は "<Leader>a" と同じキーの列 <Space>a になるので、"<Leader>a" を使う`,
	} {
		if !strings.Contains(warnings, want) {
			t.Errorf("誤りに %q が無い:\n%s", want, warnings)
		}
	}

	rows := func() string { return searchMarks(h.term, 1, hl) + "|" + searchMarks(h.term, 2, hl) }
	h.keys("/bar<CR>")
	h.settle()
	h.eventually("検索の色", func() bool { return rows() == "    ^^^|^^^" })
	h.keys("<C-l>")
	h.settle()
	h.eventually("<C-l> で色を消す", func() bool { return rows() == "|" })
	var mode command.Mode
	h.e.do(func() { mode = h.e.cur.state.Mode() })
	if mode != command.ModeNormal {
		t.Errorf("<C-l> の後のモード = %v", mode)
	}

	// インサートモードでも呼べ、インサートモードのまま続けて入力できる。
	h.keys("n")
	h.settle()
	h.eventually("n で色が戻る", func() bool { return rows() == "^^^|    ^^^" || rows() == "    ^^^|^^^" })
	h.keys("ggI<C-l>X<Esc>")
	h.settle()
	h.eventually("インサートモードの <C-l>", func() bool { return rows() == "|" })
	if got := h.text(); got != "Xfoo bar\nbar foo" {
		t.Errorf("I<C-l>X: text = %q", got)
	}

	// 引数のあるコマンド。
	h.keys("<Space>n")
	h.settle()
	h.eventually("<Leader>n で行番号を出す", func() bool { return h.screenRow(1) == "  1 Xfoo bar" })
	h.keys("<Space>n")
	h.settle()
	h.eventually("<Leader>n で行番号を消す", func() bool { return h.screenRow(1) == "Xfoo bar" })

	// 誤りは、: で打ったときと同じ文言でメッセージの行に出る。
	h.keys(":option.toggle filetype<CR>")
	h.settle()
	typed := h.message()
	h.keys("<Esc><C-k>")
	h.settle()
	if got := h.message(); !strings.HasPrefix(typed, "E488") || got != typed {
		t.Errorf("<C-k>: message = %q, : で打ったとき = %q", got, typed)
	}

	// 拡張機能のコマンド。引数は行末までをそのまま渡す。
	h.keys("<C-e>")
	h.eventually("fake.echo", func() bool { return h.e.message.Text == `args={"text":"a | b"}` })

	// キーの一覧では、出どころを設定にし、説明に実行するコマンドラインを出す。
	var rowsList []keyRow
	h.e.do(func() { rowsList = h.e.keyRows("", nil) })
	found := false
	for _, r := range rowsList {
		if r.Label == "<C-l>" && r.Modes == input.MapNormal {
			found = r.Source == sourceUser && r.Description == ":search.clearHighlight"
		}
	}
	if !found {
		t.Errorf("キーの一覧に <C-l> の設定の行が無い")
	}
}

// keymap の Ex コマンドで別のタブへ移ると、Vim 9.1 の <Cmd>tabnew<CR> と同じく、インサートモードは移った先で続き、
// 移る前のバッファはノーマルモードに戻る。ビジュアルモードでは選択を残したまま実行し、タブを移ったときは選択を抜ける。
// args にコマンドの区切りの | を書いた項目は、起動のときに設定の誤りとして知らせる。
//
// req: FR-154
func TestKeymapCommandModes(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "abcd\nefgh\n"})
	h := start(t, Config{
		WorkDir: dir,
		KeymapCommands: map[string]map[string]config.KeymapCommand{
			"normal": {"<C-b>": {Name: "option.toggle", Args: "lineNumber.show|w"}},
			"insert": {"<C-t>": {Name: "tab.new"}},
			"visual": {"<C-t>": {Name: "tab.new"}, "<C-n>": {Name: "option.toggle", Args: "lineNumber.show"}},
		},
	}, "a.txt")
	if msg := h.message(); !strings.Contains(msg, "args に、コマンドの区切りになる | を書けない") {
		t.Errorf("起動の message = %q", msg)
	}
	modeOf := func(bs *bufferState) command.Mode {
		var m command.Mode
		h.e.do(func() { m = bs.state.Mode() })
		return m
	}
	var first *bufferState
	h.e.do(func() { first = h.e.cur })

	// ビジュアルモードの <C-n> は選択を残す。
	h.keys("vj<C-n>")
	h.settle()
	var shown bool
	h.e.do(func() { shown = h.e.options.Get(option.LineNumberShow).Bool() })
	if !shown || modeOf(first) != command.ModeVisual {
		t.Errorf("visual <C-n>: lineNumber.show %v, mode %v", shown, modeOf(first))
	}
	// ビジュアルモードのまま :tab.new で移ると、元のバッファの選択を抜ける。
	h.keys("<C-t>")
	h.settle()
	var second *bufferState
	h.e.do(func() { second = h.e.cur })
	if second == first || modeOf(first) != command.ModeNormal || modeOf(second) != command.ModeNormal {
		t.Errorf("visual <C-t>: mode %v → %v", modeOf(first), modeOf(second))
	}

	// インサートモードのまま :tab.new で移ると、移った先でインサートモードが続き、元のバッファはノーマルモードに戻る。
	h.keys(":b a.txt<CR>ggAfoo<C-t>")
	h.settle()
	var third *bufferState
	h.e.do(func() { third = h.e.cur })
	if third == first || third == second || modeOf(third) != command.ModeInsert || modeOf(first) != command.ModeNormal {
		t.Fatalf("insert <C-t>: mode %v → %v", modeOf(first), modeOf(third))
	}
	h.keys("bar<Esc>")
	h.settle()
	if got := h.text(); got != "bar" {
		t.Errorf("移った先の text = %q", got)
	}
	var oldText string
	h.e.do(func() { oldText = strings.TrimSuffix(first.buf.Text(), "\n") })
	if oldText != "abcdfoo\nefgh" {
		t.Errorf("元のバッファの text = %q", oldText)
	}
	// 移った先の . は、移る前の入力と合わせた foobar を入れる（Vim 9.1 の . のレジスターと同じ）。
	h.keys("u.")
	h.settle()
	if got := h.text(); got != "foobar" {
		t.Errorf(". の後の text = %q", got)
	}
}

// 外からの要求（待つだけの client が開いたファイル。FR-83）でバッファを移ったときは、インサートモードを移った先へ持ち込まず、
// 移る前のバッファの入力と選択も終える。Vim 9.1 の --remote-wait-silent と同じく、どちらのバッファもノーマルモードになる
// （PR #303 のレビューの必須 1。前は開いたファイルがインサートモードで始まり、ZZ が ZZx になった）。
//
// req: FR-154
func TestExternalSwitchKeepsNormal(t *testing.T) {
	for _, tc := range []struct{ name, keys, want string }{
		{"インサートモード", "Afoo", "abcfoo"},
		{"ビジュアルモード", "vl", "abc"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeFiles(t, map[string]string{"a.txt": "abc\n", "x.txt": "x\n"})
			h := start(t, Config{WorkDir: dir}, "a.txt")
			h.keys(tc.keys)
			h.settle()
			var first *bufferState
			h.e.do(func() { first = h.e.cur })
			done := make(chan string, 1)
			if _, err := h.e.Wait(context.Background(), WaitInfo{Files: []string{filepath.Join(dir, "x.txt")}, Done: func(r string) { done <- r }}); err != nil {
				t.Fatal(err)
			}
			h.settle()
			var cur *bufferState
			var curMode, firstMode command.Mode
			var firstText string
			h.e.do(func() {
				cur, curMode, firstMode = h.e.cur, h.e.cur.state.Mode(), first.state.Mode()
				firstText = strings.TrimSuffix(first.buf.Text(), "\n")
			})
			if cur == first || filepath.Base(cur.path) != "x.txt" {
				t.Fatalf("移った先 = %q", cur.path)
			}
			if curMode != command.ModeNormal || firstMode != command.ModeNormal || firstText != tc.want {
				t.Errorf("x.txt %v、a.txt %v %q", curMode, firstMode, firstText)
			}
			// 続けて打ったキーはノーマルモードのキーとして効き、:q で開いたファイルを閉じて、待つだけの client が終わる。
			h.keys(":q<CR>")
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatalf(":q で閉じない（x.txt の text %q）", h.text())
			}
		})
	}
}

// インサートモードの keymap の Ex コマンドで開いたフォーカスのあるポップアップ（ファインダーと同じ形の一覧）で選ぶと、
// 拡張機能の editor/open で選んだファイルへ移る（:tab.new と同じく、キーで呼んだコマンドの結果として移る。PR #303 のレビューの推奨 1）。
// ポップアップへフォーカスを入れるときに開いたタブの入力を終えるので、開いたタブはノーマルモードに戻り、移った先もノーマルモードである。
//
// req: FR-154
func TestKeymapCommandPopupFromInsert(t *testing.T) {
	opened := fakeOpenedPath(os.Getpid())
	if err := os.WriteFile(opened, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(opened) })
	dir := writeFiles(t, map[string]string{"a.txt": "abc\n"})
	h := start(t, Config{
		WorkDir: dir, Extensions: fakeSpecs(t),
		KeymapCommands: map[string]map[string]config.KeymapCommand{
			"insert": {"<C-p>": {Name: "fake.popup", Args: "files"}},
		},
	}, "a.txt")
	var first *bufferState
	h.e.do(func() { first = h.e.cur })
	h.keys("Afoo<C-p>")
	h.eventually("ポップアップ", func() bool { return h.e.float != nil && h.e.float.focused })
	var baseMode command.Mode
	h.e.do(func() { baseMode = first.state.Mode() })
	if baseMode != command.ModeNormal {
		t.Errorf("ポップアップを開いた後の a.txt のモード = %v", baseMode)
	}
	h.keys("gg<CR>")
	h.eventually("選んだファイルへ移る", func() bool { return h.e.cur.path == opened })
	var curMode command.Mode
	var text string
	h.e.do(func() {
		curMode, baseMode = h.e.cur.state.Mode(), first.state.Mode()
		text = strings.TrimSuffix(first.buf.Text(), "\n")
	})
	if curMode != command.ModeNormal || baseMode != command.ModeNormal || text != "abcfoo" {
		t.Errorf("移った先 %v、a.txt %v %q", curMode, baseMode, text)
	}
}

// ペインの部品（別の client）でキーを打っても、元の client のインサートモードの入力は続く（PR #303 の再レビューの必須 1）。
// ペインのキーは、キーのたびにペインへ移って戻るだけで、元の client のフォーカスは移らないためである。
//
// req: FR-154
func TestPaneKeyKeepsInsert(t *testing.T) {
	var calls muxCalls
	cfg := Config{Extensions: fakeSpecs(t)}
	calls.config(&cfg)
	h := start(t, cfg)
	peer := h.attach(ClientInfo{ID: 2, Pane: "%9"})
	peer.keys(":fake.pane tree<CR>")
	h.eventually("OpenPane", func() bool { _, ok := calls.last(true); return ok })
	call, _ := calls.last(true)
	h.waitMessage("err=<nil>")
	comp := h.attachComponent(call.view, true)
	comp.waitScreen("▸ dir")

	peer.keys("ofoo")
	h.settle()
	comp.keys("j")
	h.settle()
	var mode command.Mode
	h.e.do(func() { mode = h.e.viewOf(h.clientOf(peer.term)).cur.state.Mode() })
	if mode != command.ModeInsert {
		t.Errorf("ペインで j を打った後の元の client のモード = %v", mode)
	}
	peer.keys("bar<Esc>")
	h.settle()
	var text string
	h.e.do(func() { text = strings.TrimSuffix(h.e.viewOf(h.clientOf(peer.term)).cur.buf.Text(), "\n") })
	if text != "\nfoobar" {
		t.Errorf("元の client の text = %q, want %q", text, "\nfoobar")
	}
}

// インサートモードの keymap の Ex コマンドで呼んだ拡張機能のコマンドが、別のバッファを開く結果（Open。Focus が true）を返すと、
// :tab.new と同じく移り、移った先でインサートモードが続く（PR #303 の再レビューの必須 2）。
// Vim 9.1 で set hidden と inoremap <C-g> <Cmd>buffer b.txt<CR> の Afoo<C-g>bar<Esc> は、b.txt へ移って bar を入れる（レビューで確かめた）。
// 外からの editor/open（拡張機能が自分で送るもの）は、今までどおり入力の途中のバッファからは移さない。
//
// req: FR-154
func TestKeymapCommandOpenFromInsert(t *testing.T) {
	// fake.goto は、カーソルのあるバッファでない最も小さい番号のバッファ（a.txt）の 2 行目へ移る Open を返す。
	dir := writeFiles(t, map[string]string{"a.txt": "a1\na2\n", "b.txt": "abc\ndef\n"})
	h := start(t, Config{
		WorkDir: dir, Extensions: fakeSpecs(t),
		KeymapCommands: map[string]map[string]config.KeymapCommand{
			"insert": {"<C-g>": {Name: "fake.goto"}},
		},
	}, "a.txt", "b.txt")
	var first *bufferState
	h.e.do(func() { first = h.e.cur })
	h.keys("ggAfoo<C-g>")
	h.eventually("a.txt へ移る", func() bool { return h.e.cur.path == filepath.Join(dir, "a.txt") })
	var mode, firstMode command.Mode
	h.e.do(func() { mode, firstMode = h.e.cur.state.Mode(), first.state.Mode() })
	if mode != command.ModeInsert || firstMode != command.ModeNormal {
		t.Fatalf("移った先 %v、a.txt %v", mode, firstMode)
	}
	h.keys("bar<Esc>")
	h.settle()
	var text, firstText string
	h.e.do(func() {
		text = h.e.cur.buf.Text()
		firstText = strings.TrimSuffix(first.buf.Text(), "\n")
	})
	if text != "a1\nbara2" || firstText != "abcfoo\ndef" {
		t.Errorf("移った先の text %q、b.txt %q", text, firstText)
	}
}

// KeymapCommandCheck は、keymap の Ex コマンドの名前を、拡張機能の宣言ファイルと Ex コマンドの別名の表（設定の別名を足した後）で確かめる。
// 拡張機能のコマンドと provides の操作の名前は書けるが、本体が拡張機能のコマンドと同じ口から呼ぶ :help などは、
// 正規の名前の無いコマンドなので書けない。モードと左辺は、操作の項目と同じ規則で確かめる。
//
// req: FR-154
func TestKeymapCommandCheck(t *testing.T) {
	specs := []exthost.Spec{{Manifest: ext.Manifest{
		ID: "x", Contributes: ext.Contributes{Commands: []ext.Contribution{{ID: "x.run"}}}, Provides: []ext.Operation{ext.OpHover},
	}}}
	aliases := ex.NewAliases()
	if err := aliases.Add(ex.RenameAlias{Name: "xr", Min: 2, Target: "x.run"}, []string{"x.run"}); err != nil {
		t.Fatal(err)
	}
	check := KeymapCommandCheck("", specs, aliases)
	tests := []struct {
		mode, lhs string
		cmd       config.KeymapCommand
		wantErr   string
	}{
		{"normal", "<C-l>", config.KeymapCommand{Name: "search.clearHighlight"}, ""},
		{"visual", "<C-l>", config.KeymapCommand{Name: "option.toggle", Args: "search.highlight"}, ""},
		{"insert", "<C-l>", config.KeymapCommand{Name: "x.run", Args: "a"}, ""},
		{"normal", "<Leader>h", config.KeymapCommand{Name: "hover"}, ""},
		{"normal", "<C-l>", config.KeymapCommand{Name: "xr"}, "別名なので書けない（正規の名前 x.run を書く）"},
		{"normal", "<C-l>", config.KeymapCommand{Name: "help"}, "正規の名前でも"},
		{"normal", "<C-l>", config.KeymapCommand{Name: "messages"}, "正規の名前でも"},
		{"insert", "jk", config.KeymapCommand{Name: "x.run"}, "インサートモードの左辺は 1 つのキー"},
		{"insert", "a", config.KeymapCommand{Name: "x.run"}, "操作を割り当てられない"},
		{"normal", "3", config.KeymapCommand{Name: "x.run"}, "先に読まれるキー"},
		{"commandLine", "<C-l>", config.KeymapCommand{Name: "x.run"}, "操作もコマンドも割り当てられない"},
		{"select", "<C-l>", config.KeymapCommand{Name: "x.run"}, "未知のモード"},
	}
	for _, tt := range tests {
		err := check(tt.mode, tt.lhs, tt.cmd)
		switch {
		case tt.wantErr == "" && err != nil:
			t.Errorf("%s %s %+v: err = %v", tt.mode, tt.lhs, tt.cmd, err)
		case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
			t.Errorf("%s %s %+v: err = %v, want %q を含む", tt.mode, tt.lhs, tt.cmd, err, tt.wantErr)
		}
	}
}
