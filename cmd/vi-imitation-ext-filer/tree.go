package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/piplupflipper/vi-imitation/ext"
)

// tree は、開いた木 1 つ（pane/open で開いた部品 1 つ）である。
//
// 項目の ID は、ファイルとディレクトリの絶対パスである。木の根（root）は項目にせず、根の子を木の一番上の項目にする（見出しに根を出す）。
//
// # 開閉の状態
//
// 開閉の状態は本体が項目の ID ごとに持つ（ext.ViewItem.Expanded）。拡張機能の expanded は、本体から届いた view/expand と、
// 自分で開閉したもの（h、追従）を写したもので、どのディレクトリの子を送るか（開いたものだけ送る）と、h の判定に使う。
// view/update では、自分で開閉を変える項目だけに Expanded を書き（force）、ほかは省いて本体の状態を保たせる。
// 利用者が <CR> で開閉した直後に、その知らせを読む前の古い状態で送り直して、利用者の開閉を戻さないためである。
//
// # goroutine
//
// 開いた後の操作（本体からの知らせ）は、post で列（ops）に積み、木ごとの goroutine が 1 つずつ行う。
// ディレクトリの読み込みと git の待ちを ext/server のループの上で行わないためと、操作の順を保つためである。
// 列が始まる前（開く前）は、コマンドの goroutine だけが触る。
//
// 選んだファイルを開く操作（choose）だけは、別の列（opens）に積む。ディレクトリの読み込みの後ろで待たせないためである。
// 本体は、ペインで選んでから paneFocusWindow（internal/editor/component.go。1 秒）を過ぎて届いた editor/open では、
// 端末多重化ソフトのフォーカスを元の client へ戻さない。前に開いたディレクトリの読み込みや git が遅いと、その後ろで待った choose が
// この期限を過ぎていた（#292。PR #290 のレビューの推奨 2）。choose は木の状態（dirs など）を読まずに os.Stat で判定するので、
// ops の操作と並んで動いてよい。opens も列にするのは、続けて選んだファイルを選んだ順に開くためである。
type tree struct {
	f    *filer
	root string
	// split は、filer.pane で開いた（分割したペインに出すよう頼んだ）ことを表す。タブの追従と、filer.pane での閉じる対象に使う。
	split bool
	// view は、本体が付けた部品の ID である。register で決め、その後は変えない。
	view ext.ViewID

	ctx    context.Context
	cancel context.CancelFunc

	// ops は、choose を除く操作の列である。opens は、choose の列である。
	ops, opens *serial

	// 以下は ops の goroutine（開く前はコマンドの goroutine）だけが触る。
	// dirs は、読んだディレクトリの子である（絶対パスから）。読んでいないディレクトリは無い。
	dirs     map[string][]entry
	expanded map[string]bool
	// showIgnored は、gitignore されたものも見せることを表す（H で切り替える）。
	showIgnored bool
}

// newTree は、root を根にした木を作る。根の子はまだ読まない。
func newTree(f *filer, root string, split bool) *tree {
	ctx, cancel := context.WithCancel(f.ctx)
	return &tree{
		f: f, root: filepath.Clean(root), split: split, ctx: ctx, cancel: cancel,
		ops: newSerial(), opens: newSerial(), dirs: map[string][]entry{}, expanded: map[string]bool{},
	}
}

// start は、木の列の goroutine を始める。register で 1 度だけ呼ぶ。
func (t *tree) start() {
	go t.ops.run()
	go t.opens.run()
}

// post は、op を木の操作の列（ops）に積む。待たない。どこから呼んでもよい。
func (t *tree) post(op func()) {
	t.ops.post(op)
}

// end は、木を閉じた（view/closed を受けたか、view/close を送った）ときに呼ぶ。ディレクトリの読み込みと git をすぐに止め（ctx）、
// 列に積んである操作を行ってから列の goroutine を終わらせる。
// 選んだ（view/select）直後に本体が閉じる（closeOnSelect）と、view/closed が view/select のすぐ後に届くので、
// 積んである choose を捨てずに行うためである。閉じた後の操作は、木を送り直さない（send）。列を始める前（register の前）は呼ばない。
func (t *tree) end() {
	t.cancel()
	t.ops.stop()
	t.opens.stop()
}

// serial は、積んだ操作を 1 つの goroutine（run）で積んだ順に行う列である。
type serial struct {
	mu    sync.Mutex
	queue []func()
	wake  chan struct{}
	// done は、stop の終わりの印を読んだことを表す。run の goroutine だけが触る。
	done bool
}

func newSerial() *serial {
	return &serial{wake: make(chan struct{}, 1)}
}

// post は、op を列に積む。待たない。どこから呼んでもよい。
func (s *serial) post(op func()) {
	s.mu.Lock()
	s.queue = append(s.queue, op)
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// stop は、終わりの印を列に積む。run は、それより前に積んだ操作を行ってから終わる。
func (s *serial) stop() {
	s.post(func() { s.done = true })
}

// run は、stop の終わりの印を読むまで、列に積んだ操作を順に行う。
func (s *serial) run() {
	for {
		<-s.wake
		for {
			s.mu.Lock()
			if len(s.queue) == 0 {
				s.mu.Unlock()
				break
			}
			op := s.queue[0]
			s.queue = s.queue[1:]
			s.mu.Unlock()
			op()
			if s.done {
				return
			}
		}
	}
}

// load は、ディレクトリ dir の子を読み直す。読めなければ、子を空にして理由を知らせる。木を閉じた後は何もしない。
func (t *tree) load(dir string) {
	if t.ctx.Err() != nil {
		return
	}
	es, err := readDir(t.ctx, dir)
	if err != nil && t.ctx.Err() == nil {
		_ = t.f.srv.ShowMessage(ext.MessageWarning, fmt.Sprintf("ディレクトリ %s を読めません: %v", dir, err))
	}
	t.dirs[dir] = es
}

// ensure は、ディレクトリ dir の子をまだ読んでいなければ読む。
func (t *tree) ensure(dir string) {
	if _, ok := t.dirs[dir]; !ok {
		t.load(dir)
	}
}

// entryOf は、読んだディレクトリの子のうち、パス path のものを返す。
func (t *tree) entryOf(path string) (entry, bool) {
	for _, e := range t.dirs[filepath.Dir(path)] {
		if e.name == filepath.Base(path) {
			return e, true
		}
	}
	return entry{}, false
}

// visible は、子 e を木に出すかを返す。gitignore されたものは、showIgnored のときだけ出す。
func (t *tree) visible(e entry) bool {
	return !e.ignored || t.showIgnored
}

// inside は、path が根の下（根そのものを除く）にあるかを返す。
func (t *tree) inside(path string) bool {
	rel, err := filepath.Rel(t.root, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// reveal は、ファイル path の場所まで木を開く（読んでいないディレクトリは読む）。開いたディレクトリを opened に返す。
// path が木に出る（根の下にあり、途中のディレクトリも path も隠していない）なら、cursor に path を返す。出なければ cursor は空である。
// path の親のディレクトリの子に path が無ければ（木を開いた後に作ったファイル）、その親を読み直す。
func (t *tree) reveal(path string) (cursor string, opened []string) {
	if path == "" || !t.inside(path) {
		return "", nil
	}
	t.ensure(t.root)
	rel, _ := filepath.Rel(t.root, path)
	parts := strings.Split(rel, string(filepath.Separator))
	dir := t.root
	for _, p := range parts[:len(parts)-1] {
		dir = filepath.Join(dir, p)
		e, ok := t.entryOf(dir)
		if !ok {
			t.load(filepath.Dir(dir))
			e, ok = t.entryOf(dir)
		}
		if !ok || !e.dir || !t.visible(e) {
			return "", opened
		}
		t.ensure(dir)
		if !t.expanded[dir] {
			t.expanded[dir] = true
			opened = append(opened, dir)
		}
	}
	e, ok := t.entryOf(path)
	if !ok {
		t.load(filepath.Dir(path))
		e, ok = t.entryOf(path)
	}
	if !ok || !t.visible(e) {
		return "", opened
	}
	return path, opened
}

// component は、今の木を部品にする。cursor はカーソルを置く項目（空なら本体が保つ）、force は開閉を書く項目である（tree の doc）。
func (t *tree) component(cursor string, force map[string]bool) ext.ViewComponent {
	return ext.ViewComponent{Kind: ext.ViewTree, Title: t.title(), Items: t.items(t.root, force), Cursor: cursor, Preview: true}
}

// items は、ディレクトリ dir の子の項目を返す。開いたディレクトリ（expanded）で読んだものだけ、子の項目を入れる。
// ディレクトリはどれも子を持つ印（HasChildren）を付け、開いたら view/expand で知らせさせる。
// どの項目にも、プレビューに出すもの（その絶対パス）と、指すファイルかディレクトリ（本体がアイコンを付ける）を付ける。
//
// req: FR-149, FR-152
func (t *tree) items(dir string, force map[string]bool) []ext.ViewItem {
	var out []ext.ViewItem
	for _, e := range t.dirs[dir] {
		if !t.visible(e) {
			continue
		}
		id := filepath.Join(dir, e.name)
		// プレビュー（FR-149）は場所だけを渡し、中身（ファイルの中身とディレクトリの一覧）は本体が読む。
		// 大きなディレクトリでも、項目ごとにファイルを読まないためである（PR #290 のレビューの推奨 5）。
		// アイコン（FR-152）も、ファイルかディレクトリかとパスだけを渡し、文字と色は本体が決める（#295 のオーナーの決め）。
		it := ext.ViewItem{
			ID: id, Label: cleanLabel(e.name), Preview: &ext.ViewPreview{Path: id},
			File: &ext.ViewFile{Path: id, Dir: e.dir},
		}
		if e.dir {
			it.HasChildren = true
			if _, loaded := t.dirs[id]; loaded && t.expanded[id] {
				it.Children = t.items(id, force)
			}
			if v, ok := force[id]; ok {
				it.Expanded = &v
			}
		}
		out = append(out, it)
	}
	return out
}

// cleanLabel は、名前から、部品の表示の文字列に入れられない改行を除く（\r と \n を空白にする）。
func cleanLabel(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' {
			return ' '
		}
		return r
	}, s)
}

// ignoredMark は、gitignore されたものも見せている間に見出しの先頭に置く印である。
const ignoredMark = "（gitignore されたものも表示）"

// title は、木の見出しである。根を、ホームディレクトリの下なら ~/ から書く。gitignore されたものも見せているときは、
// その旨（ignoredMark）を根の前に置く。本体は見出しを窓の幅で末尾から切る（internal/ui の drawText）ので、
// 根が長くても印が切れないようにするためである（#292。PR #290 のレビューの推奨 4）。
//
// req: FR-105
func (t *tree) title() string {
	s := t.root
	if home, err := os.UserHomeDir(); err == nil {
		if rel, rerr := filepath.Rel(home, t.root); rerr == nil && !strings.HasPrefix(rel, "..") {
			s = filepath.Join("~", rel)
		}
	}
	if t.showIgnored {
		s = ignoredMark + s
	}
	return s
}

// send は、今の木を view/update で送る。木を閉じた後は送らない。
func (t *tree) send(cursor string, force map[string]bool) {
	if t.ctx.Err() != nil {
		return
	}
	if err := t.f.srv.UpdateView(ext.ViewUpdateParams{View: t.view, Component: t.component(cursor, force)}); err != nil {
		fmt.Fprintf(os.Stderr, "view/update: %v\n", err)
	}
}

// expand は、利用者が項目 item を開閉した（view/expand）ことを写す。開いたら、まだ読んでいなければ子を読み、子を入れた木を送る。
//
// req: FR-105
func (t *tree) expand(item string, open bool) {
	t.expanded[item] = open
	if !open {
		return
	}
	t.ensure(item)
	t.send("", nil)
}

// collapse は、h（filer.close）のキーを項目 item で打ったときの操作である（neo-tree の close_node と同じ。
// neo-tree.nvim の lua/neo-tree/sources/common/commands.lua）。
// 開いたディレクトリなら閉じる。それ以外（ファイルと閉じたディレクトリ）なら、親のディレクトリを閉じて、その項目へカーソルを移す。
// 親が根なら何もしない。
//
// req: FR-105
func (t *tree) collapse(item string) {
	if item == "" {
		return
	}
	target := item
	if e, ok := t.entryOf(item); !ok || !e.dir || !t.expanded[item] {
		target = filepath.Dir(item)
	}
	if !t.inside(target) {
		return
	}
	t.expanded[target] = false
	t.send(target, map[string]bool{target: false})
}

// toggleIgnored は、H（filer.toggleIgnored）のキーの操作である。gitignore されたものを見せるかを切り替えて、木を送り直す。
//
// req: FR-105
func (t *tree) toggleIgnored() {
	t.showIgnored = !t.showIgnored
	t.send("", nil)
}

// follow は、分割したペインに出した木で、元の client のタブがファイル path に替わった（view/tabChange）ときに、
// そのファイルの場所まで木を開いてカーソルを置く（neo-tree の follow_current_file）。
// 中央のポップアップには届かない（本体が送らない）。木に出ないファイルなら、開いたディレクトリだけを送る。
//
// req: FR-105
func (t *tree) follow(path string) {
	cursor, opened := t.reveal(path)
	if cursor == "" && len(opened) == 0 {
		return
	}
	force := map[string]bool{}
	for _, d := range opened {
		force[d] = true
	}
	t.send(cursor, force)
}

// choose は、利用者が項目 item を選んだ（view/select）ときに、そのファイルを、ファイラを開いた元の client のタブで開く
// （editor/open の focus。基本設計 1.2）。中央のポップアップを閉じるのと、分割したペインからフォーカスを戻すのは本体が行う。
// opens の列で呼ぶ（tree の doc の goroutine）。木の状態は読まず、ディレクトリかどうかは os.Stat で見る
// （readDir と同じく、ディレクトリを指すシンボリックリンクはディレクトリとして扱う）。
// ディレクトリなら何もしない（開閉は本体が view/expand で知らせる）。os.Stat が失敗したもの（行き先の無いリンク、自分を指すリンク）は
// ファイルとして editor/open に渡し、開けない理由を本体に出させる（readDir もこれらをファイルとして木に出す）。
//
// req: FR-105
func (t *tree) choose(item string) {
	if fi, err := os.Stat(item); err == nil && fi.IsDir() {
		return
	}
	if _, err := t.f.srv.OpenEditor(t.f.ctx, ext.EditorOpenParams{Path: item, Focus: true}); err != nil {
		_ = t.f.srv.ShowMessage(ext.MessageError, fmt.Sprintf("%s を開けません: %v", item, err))
	}
}
