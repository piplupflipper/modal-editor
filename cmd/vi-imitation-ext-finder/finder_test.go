package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/piplupflipper/vi-imitation/ext"
)

// 絞り込みは、空白で区切ったすべての語に、語の文字が順に現れる候補を残し、点数の高い順に並べる。
// 語の切れ目と続けての一致が点数を上げ、大文字の無い語は大文字と小文字を区別しない。
//
// req: FR-106
func TestFilter(t *testing.T) {
	labels := []string{
		"internal/editor/popup.go",
		"internal/ui/float.go",
		"cmd/vi-imitation-ext-finder/finder.go",
		"docs/basic-design.md",
		"Makefile",
	}
	tests := []struct {
		q    string
		want []string
	}{
		{"", labels},
		{"popup", []string{"internal/editor/popup.go"}},
		{"fin go", []string{"cmd/vi-imitation-ext-finder/finder.go"}},
		{"make", []string{"Makefile"}},
		{"Make", []string{"Makefile"}},
		{"MAKE", nil},
		// 語の切れ目で続けて一致する float.go が、文字が離れた Makefile より先に並ぶ。
		{"fl", []string{"internal/ui/float.go", "Makefile"}},
		{"zzz", nil},
	}
	for _, tt := range tests {
		rs, n := filter(labels, tt.q, 0)
		var got []string
		for _, r := range rs {
			got = append(got, labels[r.index])
		}
		if !slices.Equal(got, tt.want) || n != len(tt.want) {
			t.Errorf("filter(%q) = %q (%d), want %q", tt.q, got, n, tt.want)
		}
	}
	// 上限で切っても、一致した数は切る前の数を返す。
	if rs, n := filter(labels, "", 2); len(rs) != 2 || n != len(labels) {
		t.Errorf("limit: %d results, %d matched", len(rs), n)
	}
}

// 一致した文字の範囲はバイトの位置で、続いた文字はまとめる。全角の文字でもずれない。
func TestMatchSpans(t *testing.T) {
	_, spans, ok := matchOne("日本語/abc.go", parseQuery("本ab"))
	want := []ext.ViewSpan{{Start: 3, End: 6, Style: matchStyle}, {Start: 10, End: 12, Style: matchStyle}}
	if !ok || !slices.Equal(spans, want) {
		t.Errorf("spans = %+v, want %+v", spans, want)
	}
	// 前から拾った後に後ろから拾い直して、一致の範囲を縮める（axxab の ab は、先頭の a でなく b の前の a に当たる）。
	_, spans, _ = matchOne("axxab", parseQuery("ab"))
	if want := []ext.ViewSpan{{Start: 3, End: 5, Style: matchStyle}}; !slices.Equal(spans, want) {
		t.Errorf("shortest spans = %+v, want %+v", spans, want)
	}
}

func TestWordAt(t *testing.T) {
	for _, tt := range []struct {
		line string
		at   int
		want string
	}{
		{"foo bar", 1, "foo"},
		{"foo bar", 3, "bar"},
		{"x := 日本語", 5, "日本語"},
		{"  ", 0, ""},
		{"a.b_c", 2, "b_c"},
	} {
		if got := wordAt(tt.line, tt.at); got != tt.want {
			t.Errorf("wordAt(%q, %d) = %q, want %q", tt.line, tt.at, got, tt.want)
		}
	}
}

// ファイルの一覧は、.git の下を除き、hidden が false なら . で始まるものも除く。fd、rg、Go で歩くのどれでも同じ結果になる。
//
// req: FR-106
func TestFileListers(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"a.go": "", "sub/b.txt": "", ".hidden/c": "", ".git/config": "", "sub/.d": "",
	})
	listers := map[string]fileLister{"walk": walkFiles}
	if name, l := pickFileLister(); name != "walk" {
		listers[name] = l
	}
	for name, l := range listers {
		for _, hidden := range []bool{true, false} {
			var got []string
			if err := l(context.Background(), dir, hidden, func(rel string) { got = append(got, rel) }); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			slices.Sort(got)
			want := []string{"a.go", "sub/b.txt"}
			if hidden {
				want = []string{".hidden/c", "a.go", "sub/.d", "sub/b.txt"}
			}
			if !slices.Equal(got, want) {
				t.Errorf("%s hidden=%v: %q, want %q", name, hidden, got, want)
			}
		}
	}
}

// rg の検索は、一致の行と列と範囲を返し、rg の正規表現の誤りを誤りにする。rg が無ければ飛ばす。
//
// req: FR-106
func TestRunGrep(t *testing.T) {
	if _, err := lookPath("rg"); err != nil {
		t.Skip("rg が無い")
	}
	dir := writeTree(t, map[string]string{"a.go": "package a\n\tfunc Hello() {}\n", ".git/x": "Hello\n"})
	var got []grepMatch
	if err := runGrep(context.Background(), dir, "hello", true, func(m grepMatch) bool { got = append(got, m); return true }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Path != "a.go" || got[0].Line != 2 || got[0].Col != 6 || !slices.Equal(got[0].Spans, [][2]int{{6, 11}}) {
		t.Errorf("matches = %+v", got)
	}
	it := grepItem(dir, got[0])
	if it.label != "a.go:2:7: func Hello() {}" || len(it.spans) != 1 || it.label[it.spans[0].Start:it.spans[0].End] != "Hello" {
		t.Errorf("item = %+v", it)
	}
	if it.open.Path != filepath.Join(dir, "a.go") || *it.open.Position != (ext.Position{Line: 1, Byte: 6}) {
		t.Errorf("open = %+v", it.open)
	}
	err := runGrep(context.Background(), dir, "(", true, func(grepMatch) bool { return true })
	if err == nil || !strings.Contains(err.Error(), "regex") {
		t.Errorf("bad regex: %v", err)
	}
	// 読めないファイルがあっても、ほかの一致を出し、誤りは短い知らせにする（PR #249 のレビューの推奨 6）。
	secret := filepath.Join(dir, "secret.go")
	if werr := os.WriteFile(secret, []byte("Hello\n"), 0o000); werr != nil {
		t.Fatal(werr)
	}
	if f, oerr := os.Open(secret); oerr == nil {
		_ = f.Close()
		t.Skip("権限の無いファイルを読める（root で動いている）")
	}
	got = nil
	err = runGrep(context.Background(), dir, "Hello", true, func(m grepMatch) bool { got = append(got, m); return true })
	if !errors.Is(err, errUnreadable) || len(got) != 1 {
		t.Errorf("unreadable: %v, %d matches", err, len(got))
	}
}

// ファイル、検索、行、ジャンプリストの候補には、プレビューに出すもの（ファイルは先頭から、ほかはその行）を付け、
// キー、コマンド、レジスター、タブの候補には付けない。プレビューを付ける種類の部品は、候補が届く前から欄を持つ（FR-149）。
//
// req: FR-149
func TestItemPreviews(t *testing.T) {
	m := grepMatch{Path: "a.go", Line: 3, Col: 1, Text: "x"}
	if pv := grepItem("/w", m).preview; pv == nil || pv.Path != "/w/a.go" || pv.Buffer != nil || pv.Line == nil || *pv.Line != 2 {
		t.Errorf("grep preview = %+v", pv)
	}
	buf := ext.BufferID(4)
	r := ext.EditorListResult{
		Jumps: []ext.ListJump{{Buffer: 1, Path: "/w/b.go", Position: ext.Position{Line: 5}}, {Buffer: buf, Position: ext.Position{Line: 1}}},
		Tabs:  []ext.ListTab{{Buffer: 1, Path: "/w/b.go", Name: "b.go"}},
		Keys:  []ext.ListKey{{Keys: "gq"}},
	}
	_, jumps := listItems(ext.ListJumps, r)
	if pv := jumps[1].preview; pv == nil || pv.Path != "/w/b.go" || pv.Buffer != nil || *pv.Line != 5 {
		t.Errorf("file jump preview = %+v", pv)
	}
	if pv := jumps[0].preview; pv == nil || pv.Path != "" || pv.Buffer == nil || *pv.Buffer != buf || *pv.Line != 1 {
		t.Errorf("buffer jump preview = %+v", pv)
	}
	for _, kind := range []ext.EditorListKind{ext.ListTabs, ext.ListKeys} {
		_, its := listItems(kind, r)
		for _, it := range its {
			if it.preview != nil {
				t.Errorf("%s item %q has a preview", kind, it.label)
			}
		}
	}
	f := newFinder(context.Background())
	for kind, want := range map[string]bool{"files": true, "grep": true, "lines": true, "jumps": true, "keys": false, "tabs": false} {
		p := f.newPicker(kind, "t", "", "")
		p.add(item{id: "a", label: "a", preview: &ext.ViewPreview{Path: "/a"}})
		c, _ := p.component()
		if c.Preview != want || c.Items[0].Preview == nil {
			t.Errorf("%s: component preview = %v, item preview = %+v", kind, c.Preview, c.Items[0].Preview)
		}
	}
}

// ファイル、検索、ファイルのタブ、ファイルのジャンプの候補には、本体がアイコンを付けるファイル（FR-152）としてそのパスを付け、
// 一覧に送る項目に載せる。行、ファイルでないバッファのタブとジャンプ、キー、コマンド、レジスターの候補には付けない。
//
// req: FR-152
func TestItemFiles(t *testing.T) {
	if f := grepItem("/w", grepMatch{Path: "a.go", Line: 3, Col: 1, Text: "x"}).file; f == nil || *f != (ext.ViewFile{Path: "/w/a.go"}) {
		t.Errorf("grep file = %+v", f)
	}
	r := ext.EditorListResult{
		Jumps:     []ext.ListJump{{Buffer: 1, Path: "/w/b.go", Position: ext.Position{Line: 5}}, {Buffer: 4, Position: ext.Position{Line: 1}}},
		Tabs:      []ext.ListTab{{Buffer: 1, Path: "/w/b.go", Name: "b.go"}, {Buffer: 4, Name: "[No Name]"}},
		Keys:      []ext.ListKey{{Keys: "gq"}},
		Commands:  []ext.ListCommand{{Name: "write"}},
		Registers: []ext.ListRegister{{Name: "a", Text: "x"}},
	}
	_, jumps := listItems(ext.ListJumps, r)
	if f := jumps[1].file; f == nil || f.Path != "/w/b.go" || f.Dir || jumps[0].file != nil {
		t.Errorf("jump files = %+v, %+v", jumps[1].file, jumps[0].file)
	}
	_, tabs := listItems(ext.ListTabs, r)
	if f := tabs[0].file; f == nil || f.Path != "/w/b.go" || tabs[1].file != nil {
		t.Errorf("tab files = %+v, %+v", tabs[0].file, tabs[1].file)
	}
	for _, kind := range []ext.EditorListKind{ext.ListKeys, ext.ListCommands, ext.ListRegisters} {
		_, its := listItems(kind, r)
		for _, it := range its {
			if it.file != nil {
				t.Errorf("%s item %q has a file", kind, it.label)
			}
		}
	}
	p := newFinder(context.Background()).newPicker("files", "t", "", "")
	p.add(item{id: "a", label: "a.go", file: &ext.ViewFile{Path: "/w/a.go"}}, item{id: "b", label: "b"})
	c, _ := p.component()
	if len(c.Items) != 2 || c.Items[0].File == nil || c.Items[0].File.Path != "/w/a.go" || c.Items[1].File != nil {
		t.Errorf("component items = %+v", c.Items)
	}
}

// rg が無ければ、検索のコマンドは開かずに理由を返す。
func TestGrepWithoutRG(t *testing.T) {
	old := lookPath
	lookPath = func(string) (string, error) { return "", errors.New("not found") }
	defer func() { lookPath = old }()
	f := newFinder(context.Background())
	if err := f.openGrep("", "", func() {}); !errors.Is(err, errNoRG) {
		t.Errorf("openGrep = %v", err)
	}
	if name, _ := pickFileLister(); name != "walk" {
		t.Errorf("pickFileLister = %s, want walk", name)
	}
}

// 大きなディレクトリ（このリポジトリ）のファイルの一覧を絞り込む時間を測る。1 回の絞り込みが 1 打鍵の間（100 ms）に収まることを確かめる。
//
// req: FR-106
func TestFilterSpeedOnRepository(t *testing.T) {
	root := filepath.Join("..", "..")
	_, l := pickFileLister()
	var labels []string
	if err := l(context.Background(), root, true, func(rel string) { labels = append(labels, rel) }); err != nil {
		t.Fatal(err)
	}
	// 候補を 10 倍に増やして、大きなリポジトリを模す。
	var many []string
	for i := range 10 {
		for _, s := range labels {
			many = append(many, fmt.Sprintf("r%d/%s", i, s))
		}
	}
	for _, q := range []string{"e", "edpop", "internal editor popup.go"} {
		start := time.Now()
		_, n := filter(many, q, defaultLimit)
		d := time.Since(start)
		t.Logf("%d 件を %q で絞って %d 件: %v", len(many), q, n, d)
		if d > time.Second {
			t.Errorf("%q: %v", q, d)
		}
	}
}

// 行単位のレジスターは下の行に、文字単位はカーソルの文字の後に貼る（Vim の p）。
func TestPastePosition(t *testing.T) {
	b := lines{"ab", "cd"}
	for _, tt := range []struct {
		cur      ext.Position
		reg      ext.ListRegister
		at       ext.Position
		wantText string
	}{
		{ext.Position{Line: 0, Byte: 0}, ext.ListRegister{Text: "x"}, ext.Position{Line: 0, Byte: 1}, "x"},
		{ext.Position{Line: 0, Byte: 1}, ext.ListRegister{Text: "L", Linewise: true}, ext.Position{Line: 1}, "L\n"},
		{ext.Position{Line: 1, Byte: 0}, ext.ListRegister{Text: "L", Linewise: true}, ext.Position{Line: 1, Byte: 2}, "\nL"},
	} {
		at, text := pastePosition(b, tt.cur, tt.reg)
		if at != tt.at || text != tt.wantText {
			t.Errorf("pastePosition(%+v, %+v) = %+v %q", tt.cur, tt.reg, at, text)
		}
	}
}

// lines は、行の列を lineSource にしたものである。
type lines []string

func (l lines) LineCount() int    { return len(l) }
func (l lines) Line(i int) string { return l[i] }

// writeTree は、一時ディレクトリに files（相対パスから内容）を置いて、そのパスを返す。
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, text := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
