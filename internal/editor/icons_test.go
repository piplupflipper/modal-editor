package editor

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/piplupflipper/vi-imitation/ext"
	"github.com/piplupflipper/vi-imitation/internal/exthost"
	"github.com/piplupflipper/vi-imitation/internal/option"
	"github.com/piplupflipper/vi-imitation/internal/ui"
)

// noIcons は、アイコン（FR-152）を切る設定である。アイコンを確かめない試験で、一覧と木の行を表示の文字列だけにするのに使う。
var noIcons = map[string]option.Value{option.IconsShow: option.BoolValue(false)}

// iconOf は、既定のアイコンの表の名前 name のアイコンの文字列と、後に置く空白を返す。
func iconOf(t *testing.T, name string) string {
	t.Helper()
	ic, ok := ui.DefaultIcons()[name]
	if !ok {
		t.Fatalf("既定の表に %s が無い", name)
	}
	return ic.Text + " "
}

// iconLines は、エディタ h の今の設定で、部品 c の行と色を付ける範囲を作る（開いた木の項目は expanded）。
// アイコンの列を置くかは、新しく開いた部品と同じく c の項目で決める。
func iconLines(h *harness, c ext.ViewComponent, expanded map[string]bool) (lines []string, decs []ext.Decoration) {
	h.e.do(func() { lines, _, decs = floatLines(c, expanded, h.e.itemIcons(hasItemFile(c.Items))) })
	return lines, decs
}

// 一覧と木の、ファイルかディレクトリを指す項目の前に、本体がファイルの種類ごとのアイコンと空白 1 つを置き、アイコンの文字に色の名前を付ける。
// ファイルの種類は、拡張子とファイル名から、バッファを開くときと同じ対応表（既定と設定の fileTypes）で決める。
// ディレクトリは開いたものと閉じたもので分け、種類の分からないファイルと、表に無い種類のファイルは IconFile にする。
// 同じ部品のファイルを指さない項目は空白で表示の文字列の列をそろえ、項目の色の範囲はアイコンの分だけずらす。
//
// req: FR-152
func TestItemIcons(t *testing.T) {
	h := start(t, Config{FileTypes: map[string]string{".lua": "lua", ".zz": "nosuchtype"}})
	file := func(p string) *ext.ViewFile { return &ext.ViewFile{Path: p} }
	c := ext.ViewComponent{Kind: ext.ViewTree, Items: []ext.ViewItem{
		{ID: "sub", Label: "sub", File: &ext.ViewFile{Path: "/w/sub", Dir: true}, HasChildren: true, Children: []ext.ViewItem{
			{ID: "x", Label: "x.go", File: file("/w/sub/x.go")},
		}},
		{ID: "empty", Label: "empty", File: &ext.ViewFile{Path: "/w/empty", Dir: true}, HasChildren: true},
		{ID: "mk", Label: "Makefile", File: file("/w/Makefile")},
		{ID: "j", Label: "a.jsonc", File: file("a.jsonc"), Highlights: []ext.ViewSpan{{Start: 0, End: 1, Style: ui.StyleFinderMatch}}},
		{ID: "r", Label: "README", File: file("/w/README")},
		{ID: "l", Label: "c.lua", File: file("/w/c.lua")},
		{ID: "z", Label: "d.zz", File: file("/w/d.zz")},
		{ID: "n", Label: "no file"},
	}}
	lines, decs := iconLines(h, c, map[string]bool{"sub": true})
	want := []string{
		"▾ " + iconOf(t, "directoryOpen") + "sub",
		"    " + iconOf(t, "fileTypes.go") + "x.go",
		"▸ " + iconOf(t, "directory") + "empty",
		"  " + iconOf(t, "fileTypes.make") + "Makefile",
		"  " + iconOf(t, "fileTypes.jsonc") + "a.jsonc",
		"  " + iconOf(t, "file") + "README",
		"  " + iconOf(t, "fileTypes.lua") + "c.lua",
		"  " + iconOf(t, "file") + "d.zz",
		"  " + "  " + "no file",
	}
	if !slices.Equal(lines, want) {
		t.Errorf("lines =\n%q\nwant\n%q", lines, want)
	}
	// アイコンの文字の範囲に、そのアイコンの色の名前を付ける。
	icon := func(line int, name string) ext.Decoration {
		start := strings.Index(lines[line], ui.DefaultIcons()[name].Text)
		return ext.Decoration{
			Range: ext.Range{Start: ext.Position{Line: line, Byte: start}, End: ext.Position{Line: line, Byte: start + len(ui.DefaultIcons()[name].Text)}},
			Style: ui.IconStyle(name),
		}
	}
	for _, d := range []ext.Decoration{
		icon(0, "directoryOpen"), icon(1, "fileTypes.go"), icon(2, "directory"), icon(3, "fileTypes.make"),
		icon(4, "fileTypes.jsonc"), icon(5, "file"), icon(6, "fileTypes.lua"), icon(7, "file"),
	} {
		if !slices.Contains(decs, d) {
			t.Errorf("decorations %+v lack %+v", decs, d)
		}
	}
	// 項目の色の範囲は、表示の文字列の中の位置からアイコンの分だけずれる。
	at := len(lines[4]) - len("a.jsonc")
	match := ext.Decoration{Range: ext.Range{Start: ext.Position{Line: 4, Byte: at}, End: ext.Position{Line: 4, Byte: at + 1}}, Style: ui.StyleFinderMatch}
	if !slices.Contains(decs, match) {
		t.Errorf("decorations %+v lack the shifted highlight %+v", decs, match)
	}
	if len(decs) != 9 {
		t.Errorf("%d decorations, want 8 icons and 1 highlight: %+v", len(decs), decs)
	}

	// どの項目もファイルを指さない一覧（キー、コマンド、レジスター）には、アイコンも空白も置かない。
	keys := ext.ViewComponent{Kind: ext.ViewList, Items: []ext.ViewItem{{ID: "1", Label: "gg"}, {ID: "2", Label: ":write"}}}
	if lines, decs := iconLines(h, keys, nil); !slices.Equal(lines, []string{"gg", ":write"}) || len(decs) != 0 {
		t.Errorf("a list without files: %q %+v", lines, decs)
	}
	// 一覧とファインダーのディレクトリは、閉じたディレクトリのアイコンにする。
	list := ext.ViewComponent{Kind: ext.ViewFinder, Items: []ext.ViewItem{{ID: "d", Label: "sub/", File: &ext.ViewFile{Path: "sub", Dir: true}}}}
	if lines, _ := iconLines(h, list, map[string]bool{"d": true}); !slices.Equal(lines, []string{iconOf(t, "directory") + "sub/"}) {
		t.Errorf("a directory in a finder: %q", lines)
	}
}

// ファインダーで絞り込んで、ファイルを指す項目が一覧から無くなっても、アイコンの列（空白）を置き続け、表示の文字列の列が動かない
// （PR #305 のレビューの推奨 1。<Space>fb で [No Name] だけが残る絞り込み）。
// アイコンの列を置くかは、開いてから一度でもファイルを指す項目が来たかで決まる。ファイルを指す項目が一度も来ない部品には置かない。
//
// req: FR-152
func TestIconColumnStaysWhileFiltering(t *testing.T) {
	h := start(t, Config{})
	tabs := []ext.ViewItem{
		{ID: "1", Label: "a.go", File: &ext.ViewFile{Path: "/w/a.go"}},
		{ID: "2", Label: "[No Name]"},
	}
	var before, after, keys string
	h.e.do(func() {
		f := h.e.newFinderFloat(ext.ViewComponent{Kind: ext.ViewFinder, Items: tabs}, "")
		before = f.list.buf.Line(1)
		h.e.setFinderList(f, tabs[1:], true) // "Nam" で絞り込んだ結果
		after = f.list.buf.Line(0)
		g := h.e.newFinderFloat(ext.ViewComponent{Kind: ext.ViewFinder, Items: []ext.ViewItem{{ID: "k", Label: "gg"}}}, "")
		h.e.setFinderList(g, []ext.ViewItem{{ID: "k", Label: "gg"}, {ID: "w", Label: ":write"}}, false)
		keys = g.list.buf.Text()
	})
	if before != "  [No Name]" || after != before {
		t.Errorf("[No Name] before filtering %q, after %q", before, after)
	}
	if keys != "gg\n:write" {
		t.Errorf("a finder without files: %q", keys)
	}
}

// 表示幅が 2 のアイコンを設定しても、アイコンの列の幅（表の最大の表示幅）まで空白で埋めるので、
// 幅 1 のアイコンの行、幅 2 のアイコンの行、ファイルを指さない行で、表示の文字列が同じ列から始まる（PR #305 のレビューの推奨 2）。
//
// req: FR-152
func TestWideIconColumn(t *testing.T) {
	h := start(t, Config{Icons: ui.Icons{"fileTypes.jsonc": {Text: "あ"}}})
	c := ext.ViewComponent{Kind: ext.ViewFinder, Items: []ext.ViewItem{
		{ID: "j", Label: "conf.jsonc", File: &ext.ViewFile{Path: "conf.jsonc"}},
		{ID: "g", Label: "a.go", File: &ext.ViewFile{Path: "a.go"}},
		{ID: "n", Label: "[No Name]"},
	}}
	lines, decs := iconLines(h, c, nil)
	want := []string{"あ conf.jsonc", strings.TrimSuffix(iconOf(t, "fileTypes.go"), " ") + "  a.go", "   [No Name]"}
	if !slices.Equal(lines, want) {
		t.Errorf("lines = %q, want %q", lines, want)
	}
	for i, l := range lines {
		label := []string{"conf.jsonc", "a.go", "[No Name]"}[i]
		if w := ui.StringWidth(strings.TrimSuffix(l, label), ui.DefaultTabStop); w != 3 {
			t.Errorf("line %d: the label starts at column %d, want 3", i, w)
		}
	}
	// アイコンの色の範囲は、アイコンの文字だけに付ける（埋めた空白には付けない）。
	if len(decs) != 2 || decs[0].Range.End.Byte != len("あ") || decs[1].Range.End.Byte != len(ui.DefaultIcons()["fileTypes.go"].Text) {
		t.Errorf("decorations = %+v", decs)
	}
}

// 既定のアイコンの表は、本体が判定できるファイルの種類（defaultFileTypes）を全部持つ（#295 の決まったこと）。
//
// req: FR-152
func TestDefaultIconsCoverFileTypes(t *testing.T) {
	icons := ui.DefaultIcons()
	for key, ft := range defaultFileTypes() {
		if _, ok := icons[ui.IconFileType(ft)]; !ok {
			t.Errorf("%s の種類 %s のアイコンが既定の表に無い", key, ft)
		}
	}
}

// 設定の icons.show（既定は true）を false にすると、ファイルを指す項目にもアイコンを付けない。実行中に :option.set で変えると、
// 次に作る一覧から効く。
//
// req: FR-152
func TestIconsShowOption(t *testing.T) {
	items := []ext.ViewItem{{ID: "a", Label: "a.go", File: &ext.ViewFile{Path: "a.go"}}, {ID: "b", Label: "keys"}}
	c := ext.ViewComponent{Kind: ext.ViewList, Items: items}

	h := start(t, Config{})
	if lines, _ := iconLines(h, c, nil); !slices.Equal(lines, []string{iconOf(t, "fileTypes.go") + "a.go", "  keys"}) {
		t.Errorf("default: %q", lines)
	}
	f := openPreviewFloat(h, ext.ViewList, items)
	h.settle()
	h.e.do(func() {
		if got := f.bs.buf.Line(0); got != iconOf(t, "fileTypes.go")+"a.go" {
			t.Errorf("popup line = %q", got)
		}
	})
	h.keys("<Esc>:option.set icons.show false<CR>")
	h.settle()
	if lines, decs := iconLines(h, c, nil); !slices.Equal(lines, []string{"a.go", "keys"}) || len(decs) != 0 {
		t.Errorf("after :option.set icons.show false: %q %+v", lines, decs)
	}
	f = openPreviewFloat(h, ext.ViewList, items)
	h.settle()
	h.e.do(func() {
		if got := f.bs.buf.Text(); got != "a.go\nkeys" {
			t.Errorf("popup after :option.set = %q", got)
		}
	})

	off := start(t, Config{Options: noIcons})
	if lines, _ := iconLines(off, c, nil); !slices.Equal(lines, []string{"a.go", "keys"}) {
		t.Errorf("icons.show false in the config: %q", lines)
	}
}

// 設定の icons で、表のアイコンを名前ごとに置き換えられる。置き換えた名前は文字と色が設定のものになり、書かなかった名前は既定のままである。
// アイコンの色は、窓の色の上に重ねて描く。
//
// req: FR-152
func TestIconsConfigOverride(t *testing.T) {
	gofg, dirfg := ui.RGB(1, 2, 3), ui.Indexed(5)
	term := newFakeTerm()
	term.w, term.h = 80, 12
	h := startTerm(t, term, Config{Icons: ui.Icons{
		"fileTypes.go": {Text: "G", Attr: ui.Attr{Fg: gofg}},
		"directory":    {Text: "D", Attr: ui.Attr{Fg: dirfg, Bold: true}},
	}})
	items := []ext.ViewItem{
		{ID: "d", Label: "dir", File: &ext.ViewFile{Path: "dir", Dir: true}},
		{ID: "a", Label: "a.go", File: &ext.ViewFile{Path: "a.go"}},
		{ID: "j", Label: "b.json", File: &ext.ViewFile{Path: "b.json"}},
	}
	lines, _ := iconLines(h, ext.ViewComponent{Kind: ext.ViewList, Items: items}, nil)
	if want := []string{"D dir", "G a.go", iconOf(t, "fileTypes.json") + "b.json"}; !slices.Equal(lines, want) {
		t.Errorf("lines = %q, want %q", lines, want)
	}
	openPreviewFloat(h, ext.ViewList, items)
	h.eventually("置き換えたアイコンの色", func() bool {
		g := screenGrid(h.term)
		if g == nil {
			return false
		}
		popup := ui.DefaultTheme()[ui.StylePopup]
		g1, ok1 := findCell(g, "G a.go")
		d1, ok2 := findCell(g, "D dir")
		if !ok1 || !ok2 {
			return false
		}
		ga, da := g.At(g1[0], g1[1]).Attr, g.At(d1[0], d1[1]).Attr
		// 窓の背景（選んでいない行は popup、カーソルの行は popup.selected）の上に、アイコンの文字の色を重ねる。
		return ga.Fg == gofg && ga.Bg == popup.Bg && da.Fg == dirfg && da.Bold
	})
}

// screenGrid は、偽の端末に最後に描いた画面を返す。まだ描いていなければ nil である。
func screenGrid(term *fakeTerm) *ui.Grid {
	term.mu.Lock()
	defer term.mu.Unlock()
	return term.last.Grid
}

// findCell は、画面 g の中で、文字列 text が始まるセルの行と列を返す。各セルの文字をつないだ行の中で探す。
func findCell(g *ui.Grid, text string) ([2]int, bool) {
	for row := range g.Height() {
		var cols []int
		line := ""
		for c := range g.Width() {
			s := g.At(row, c).Text
			for range len(s) {
				cols = append(cols, c)
			}
			line += s
		}
		if i := strings.Index(line, text); i >= 0 {
			return [2]int{row, cols[i]}, true
		}
	}
	return [2]int{}, false
}

// アイコンを付けた項目の窓の右にプレビュー（FR-149）を出しても、縦の線とプレビューの列は、アイコンの無い行と同じ列に並ぶ。
// アイコンの文字（私用領域のグリフ）を本体が 1 列と数え、後の空白と合わせて 2 列を取るためである（ui.CheckIcon）。
//
// req: FR-152
func TestIconsBesidePreview(t *testing.T) {
	dir := previewDir(t)
	h, ft := startPreview(t, dir, 200, 12, nil)
	items := []ext.ViewItem{
		{ID: "a", Label: "a.go", File: &ext.ViewFile{Path: filepath.Join(dir, "a.go")}, Preview: pvOf(filepath.Join(dir, "a.go"))},
		{ID: "s", Label: "sub", File: &ext.ViewFile{Path: filepath.Join(dir, "sub"), Dir: true}, Preview: pvOf(filepath.Join(dir, "sub"))},
		{ID: "n", Label: "plain"},
	}
	f := openPreviewFloat(h, ext.ViewList, items)
	fireLast(h, ft, 0)
	waitPreview(h, f, "p:"+filepath.Join(dir, "a.go")+"#-")
	h.eventually("アイコンとプレビューの並び", func() bool {
		g := screenGrid(h.term)
		if g == nil {
			return false
		}
		goIcon, ok1 := findCell(g, iconOf(t, "fileTypes.go")+"a.go")
		dirIcon, ok2 := findCell(g, iconOf(t, "directory")+"sub")
		plain, ok3 := findCell(g, "plain")
		if !ok1 || !ok2 || !ok3 {
			return false
		}
		// アイコンは 1 列、後の空白は 1 列で、表示の文字列はアイコンの 2 列後から始まる。ファイルを指さない項目は同じ列から始まる。
		if g.At(goIcon[0], goIcon[1]).Width != 1 || g.At(goIcon[0], goIcon[1]+2).Text != "a" ||
			dirIcon[1] != goIcon[1] || plain[1] != goIcon[1]+2 {
			return false
		}
		// 縦の線は、どの行でも同じ列にあり、プレビューの中身（a.go の 1 行目と 2 行目）は、アイコンのある行（sub の行）と
		// 無い行（plain の行）で同じ列から始まる。
		sep := -1
		for c := range g.Width() {
			if g.At(goIcon[0], c).Text == "│" {
				sep = c
				break
			}
		}
		pkg, ok4 := findCell(g, "package main")
		line2, ok5 := findCell(g, "// line 2")
		return sep > 0 && ok4 && ok5 && pkg[0] == dirIcon[0] && line2[0] == plain[0] && pkg[1] > sep && line2[1] == pkg[1] &&
			g.At(dirIcon[0], sep).Text == "│" && g.At(plain[0], sep).Text == "│"
	})
}

// 標準のファイラの拡張機能の木は、ディレクトリに開いたものと閉じたもののアイコンを、ファイルにファイルの種類のアイコンを付けて出す。
// 開閉すると、ディレクトリのアイコンも替わる。
//
// req: FR-152
func TestStandardFilerIcons(t *testing.T) {
	spec := buildStdExt(t, "vi-imitation-ext-filer")
	dir := filerDir(t)
	h := start(t, Config{WorkDir: dir, Extensions: []exthost.Spec{spec}}, filepath.Join(dir, "sub", "deep", "c.txt"))
	d, o, f := iconOf(t, "directory"), iconOf(t, "directoryOpen"), iconOf(t, "file")
	wait := func(what, want string) {
		t.Helper()
		h.eventually(what, func() bool {
			text, _ := filerTree(h.e.float)
			return text == want
		})
	}
	h.keys("<Space>e")
	wait("アイコンを付けた木", strings.Join([]string{
		"▸ " + d + ".git",
		"▾ " + o + "sub",
		"  ▾ " + o + "deep",
		"      " + f + "c.txt",
		"      " + f + "d.txt",
		"    " + f + "b.txt",
		"  " + f + ".gitignore",
		"  " + f + ".hidden",
		"  " + f + "a.txt",
	}, "\n"))
	h.keys("h")
	wait("閉じた deep", strings.Join([]string{
		"▸ " + d + ".git", "▾ " + o + "sub", "  ▸ " + d + "deep", "    " + f + "b.txt",
		"  " + f + ".gitignore", "  " + f + ".hidden", "  " + f + "a.txt",
	}, "\n"))
}
