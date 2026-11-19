package editor

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/internal/buffer"
	"github.com/piplupflipper/modal-editor/internal/command"
	"github.com/piplupflipper/modal-editor/internal/command/ex"
	"github.com/piplupflipper/modal-editor/internal/editcmd"
	"github.com/piplupflipper/modal-editor/internal/exthost"
	"github.com/piplupflipper/modal-editor/internal/ui"
)

// ポップアップと表示の部品（基本設計 1.4。FR-68〜FR-72、FR-110）。
//
// # 部品はバッファ
//
// ポップアップに出す表示の部品（テキスト、一覧、木、入力の行）は、どれも本体のバッファ（bufferState）にして出す。
// テキストは本文を、一覧と木は 1 項目を 1 行にした文字列を、入力の行は中身の 1 行を持つバッファである。
// このため、ポップアップの中でも通常のノーマルモードのキー（移動、検索、ヤンク、q のマクロの記録）が効き（FR-71）、
// 描き方もバッファの表示領域と同じものを使える（ui.Float）。色を付ける範囲は、そのバッファの構文の色（decoration/set と同じ持ち方）にする。
// テキスト、一覧、木のバッファは編集を禁止したファイルでないバッファ（FR-88）で、入力の行のバッファだけが編集できる。
// ポップアップのために作ったバッファ（floatState.scratch）は、どのタブにもならず（bufferState.popup）、:ls に出さず、
// ポップアップを閉じたら閉じる。
//
// # 1 つの client に 1 つ
//
// ポップアップは client ごとに 1 つ（viewFields.float）で、新しいポップアップは前のものを閉じて置き換える。
// 置き場所は、カーソルの近く（ホバー、「ポップアップで見る」）と、画面の中央（ヘルプ、キーの一覧、:popup で縮めたタブ、
// 拡張機能の center のポップアップ）の 2 つである。中央のポップアップは、端末多重化ソフトのポップアップの部品の client（FR-66、FR-67）が
// まだ無いので、今は編集の client の画面の中央に描く（端末多重化ソフトの外の規則。基本設計 1.4）。
//
// # フォーカス
//
// フォーカスを入れたポップアップ（floatState.focused）では、キーをポップアップのバッファの State へ渡す。
// Editor.cur をポップアップのバッファにし、タブの行とバッファの表示領域には、開いたときのタブ（floatState.base）を出す（Editor.tab）。
// レジスター、直前の検索、マクロの記録は、:b でバッファを移るときと同じく State.TakeOver で引き継ぐ。
// 中央のポップアップは、開いたときからフォーカスを入れ、閉じるまで外さない（FR-68）。カーソルの近くのポップアップには、<C-w>w で入れる。
// フォーカスのあるポップアップは、ノーマルモードの <Esc>（キーの列の途中でないとき）で閉じる。q は閉じるキーにしない（#177 の裁定）。
// 一覧と木の <CR> は項目を選び、入力の行の <CR> は中身を選んだものとして知らせる。
//
// フォーカスの無いカーソルの近くのポップアップ（floatState.transient）は、ホバーと同じく、次のキーかカーソルの移動で閉じる。
// <C-w> は <C-w>w の始まりなので、<C-w> では閉じない。
//
// # タブとの行き来（FR-70）
//
// :popup は、カーソルのあるタブをポップアップへ縮める。縮めたバッファは、その client のタブとして数えたまま（owner を保ち、FR-62 の 1 対 1 を崩さない）
// タブの並びから外し、ポップアップを閉じたらタブの並びへ戻す。:tabopen は、ポップアップのバッファをタブへ広げる。
// 「ポップアップで見る」の写し（FR-110）を広げたときは、写しを閉じて本物のファイルを開く。

// floatKind は、ポップアップの部品の種類である。
type floatKind int

const (
	floatText floatKind = iota
	floatList
	floatTree
	floatInput
)

// floatRow は、一覧と木のポップアップの 1 行の項目である。
type floatRow struct {
	id string
	// parent は、木で子を持つ項目（開閉できる項目）であることを表す。
	parent bool
}

// floatState は、client が出しているポップアップである。
type floatState struct {
	// id は、ポップアップの番号である（ext.ViewID と ui.Float.ID）。server の中で一意で、使い回さない。
	id   uint64
	kind floatKind
	// bs は、ポップアップに出すバッファ、base は開いたときのカーソルのあるタブである。
	bs, base *bufferState
	// anchor は、開いたときの base のカーソルである。transient のポップアップは、base のカーソルがここから動いたら閉じる。
	anchor buffer.Position
	// keys と trackKeys は、popup/show（ホバー）のポップアップで、base の State が解釈したキーの数（command.State.KeysFed）が
	// keys から進んだら閉じることを表す（マクロの再生の続きのキーで閉じるため。expireFloat）。
	keys      uint64
	trackKeys bool
	center    bool
	focused   bool
	transient bool
	// scratch は、bs をこのポップアップのために作ったことを表す。閉じたら bs も閉じる。
	scratch bool
	// shrunk は、bs が :popup で縮めたタブであることを表す。閉じたらタブの並びへ戻す。
	shrunk bool
	title  string
	// peek は、「ポップアップで見る」（FR-110）の本物の位置である。:tabopen でこの位置を開く。
	peek *ext.EditorOpenParams
	// ext と view は、拡張機能の popup/open で開いたポップアップの、持ち主の拡張機能の ID とポップアップの ID である。
	ext string
	// rows は、一覧と木の行ごとの項目である。tree と expanded は、木の元の項目と開閉の状態である。
	rows     []floatRow
	tree     []ext.ViewItem
	expanded map[string]bool
	// onSelect は、本体が出した一覧（キーの一覧）で、行 row を選んだときに呼ぶ関数である。nil なら拡張機能へ view/select を送る。
	onSelect func(row int)
	// pending は、contributes.popupKeys のキーの列の途中まで打ったキーである。
	pending []editcmd.Key
	// input は、入力の行の、最後に input/change で知らせた（または開いたときの）中身である。
	input string
}

// popupDecorationOwner は、ポップアップの部品の色を付ける範囲を、バッファの構文の色に入れるときの持ち主の名前である。
// 拡張機能の ID は空白を含まないので（ext.Manifest.Validate）、拡張機能の decoration/set と重ならない。
const popupDecorationOwner = " popup"

// tab は、読み込んでいる client のカーソルのあるタブを返す。フォーカスのあるポップアップがあれば、それを開いたときのタブである。
func (e *Editor) tab() *bufferState {
	if f := e.float; f != nil && f.focused {
		return f.base
	}
	return e.cur
}

// floatViewport は、ポップアップのバッファのページ単位のスクロール（command.State.Viewport）を、窓の表示領域につなぐ口である。
// 窓をまだ描いていなければ、1 行の表示領域として扱う。
type floatViewport struct{ r *ui.Renderer }

func (v floatViewport) Top() int {
	if s := v.r.FloatRenderer(); s != nil {
		return s.Top()
	}
	return 0
}

func (v floatViewport) Rows() int {
	if s := v.r.FloatRenderer(); s != nil {
		return max(s.TextRows(), 1)
	}
	return 1
}

func (v floatViewport) SetTop(top int) {
	if s := v.r.FloatRenderer(); s != nil {
		s.SetScroll(top, s.Left())
	}
}

// newPopupBuffer は、ポップアップのためのファイルでないバッファを作る。text を中身にし、readOnly なら編集を禁止する（FR-88）。
// どのタブにもしない（bufferState.popup）。
func (e *Editor) newPopupBuffer(title, text, fileType string, readOnly bool) *bufferState {
	b := buffer.New(text)
	b.SetFixEndOfLine(e.fixEOL)
	b.SetReadOnly(readOnly)
	bs := e.newBuffer(b, "", fileType)
	bs.title = title
	bs.popup = true
	return bs
}

// openFloat は、読み込んでいる client に f を出す。前のポップアップは閉じる（持ち主の拡張機能へ view/closed を送る）。
// f.focused なら、開いた時点でフォーカスを入れる。f.base と f.anchor は、ここで読み込んでいる client のカーソルのあるタブから決める。
func (e *Editor) openFloat(f *floatState) {
	e.closeFloat(true)
	e.floatSeq++
	f.id = e.floatSeq
	f.base = e.cur
	f.anchor = e.cur.state.Cursor()
	f.bs.state.Viewport = floatViewport{r: e.renderer}
	focus := f.focused
	f.focused = false
	e.float = f
	if focus {
		e.focusFloat()
	}
}

// switchFocus は、キーを受けるバッファを to にする。マクロの記録、レジスター、直前の検索などを、:b でバッファを移るときと同じく引き継ぐ。
// タブの行とスクロールの位置は変えない。
func (e *Editor) switchFocus(to *bufferState) {
	if e.cur == to {
		return
	}
	to.state.TakeOver(e.cur.state)
	if e.shared == e.cur.state {
		e.shared = to.state
	}
	e.cur = to
}

// focusFloat は、読み込んでいる client のポップアップへフォーカスを入れる（FR-68）。
// 以後のキーはポップアップのバッファの State へ入るので、通常のノーマルモードとインサートモードのキーが効く（FR-71）。
//
// req: FR-68, FR-71
func (e *Editor) focusFloat() {
	f := e.float
	if f == nil || f.focused {
		return
	}
	f.focused = true
	f.transient = false
	e.switchFocus(f.bs)
}

// unfocusFloat は、ポップアップのフォーカスを外し、開いたときのタブへ戻す。ポップアップは閉じずに残す。
func (e *Editor) unfocusFloat() {
	f := e.float
	if f == nil || !f.focused {
		return
	}
	f.focused = false
	f.pending = nil
	e.switchFocus(f.base)
}

// closeFloat は、読み込んでいる client のポップアップを閉じる。フォーカスがあれば、開いたときのタブへ戻す。
// :popup で縮めたタブはタブの並びへ戻し、ポップアップのために作ったバッファは閉じる。
// notify なら、拡張機能のポップアップの持ち主へ view/closed を送る（拡張機能が view/close で閉じたときは送らない）。
func (e *Editor) closeFloat(notify bool) {
	f := e.float
	if f == nil {
		return
	}
	e.float = nil
	if f.focused {
		f.focused = false
		base := f.base
		if !slices.Contains(e.view.tabs, base) {
			base = e.firstTab()
		}
		if base != nil {
			e.switchFocus(base)
		}
	}
	switch {
	case f.shrunk:
		e.restoreTab(f.bs, f.base)
	case f.scratch && slices.Contains(e.bufs, f.bs):
		e.closeBufferState(f.bs)
	}
	if notify && f.ext != "" {
		if err := e.host.Notify(f.ext, ext.MethodViewClosed, ext.ViewClosedParams{View: ext.ViewID(f.id)}); err != nil {
			e.log.Warn("view/closed を送れない", "extension", f.ext, "err", err)
		}
	}
}

// firstTab は、読み込んでいる client の最初のタブを返す。タブが無ければ nil である。
func (e *Editor) firstTab() *bufferState {
	if len(e.view.tabs) == 0 {
		return nil
	}
	return e.view.tabs[0]
}

// restoreTab は、:popup で縮めたバッファ bs を、読み込んでいる client のタブの並びの after の右へ戻す。カーソルのあるタブは変えない。
func (e *Editor) restoreTab(bs, after *bufferState) {
	c := e.view
	if slices.Contains(c.tabs, bs) || !slices.Contains(e.bufs, bs) {
		return
	}
	i := slices.Index(c.tabs, after)
	c.tabs = slices.Insert(c.tabs, i+1, bs)
	bs.owner = c
	bs.state.Viewport = viewport{r: e.renderer}
}

// closeFloatsOf は、すべての client のポップアップのうち、バッファ bs を出しているか、bs のタブの上に開いたものを閉じる。
// バッファを閉じる前に呼ぶ（closeBufferState）。
func (e *Editor) closeFloatsOf(bs *bufferState) {
	for _, c := range slices.Clone(e.clients) {
		if c.detached {
			continue
		}
		if f := e.viewOf(c).float; f != nil && (f.bs == bs || f.base == bs) {
			e.withView(c, func() { e.closeFloat(true) })
		}
	}
}

// closeExtensionFloats は、拡張機能 extID が開いたポップアップをすべて閉じる。拡張機能が止まったときに呼ぶ。view/closed は送らない。
func (e *Editor) closeExtensionFloats(extID string) {
	for _, c := range slices.Clone(e.clients) {
		if c.detached {
			continue
		}
		if f := e.viewOf(c).float; f != nil && f.ext == extID {
			e.withView(c, func() { e.closeFloat(false) })
		}
	}
}

// expireFloat は、読み込んでいる client のフォーカスの無い transient のポップアップを、開いたときのタブから移ったか、
// そのカーソルが動いたか、popup/show のポップアップで State が次のキーを解釈していたら閉じる（基本設計 6.7）。
// 描く前に呼ぶ。<C-w> を打った後（State がキーの列の途中）は、<C-w>w でフォーカスを入れるために閉じない。
func (e *Editor) expireFloat() {
	f := e.float
	if f == nil || f.focused || !f.transient {
		return
	}
	st := f.base.state
	if e.cur != f.base || st.Cursor() != f.anchor || (f.trackKeys && f.keys != st.KeysFed() && !st.Pending()) {
		e.closeFloat(true)
	}
}

// dismissFloat は、利用者のキー k の前に、フォーカスの無い transient のポップアップを閉じる（次のキーで閉じる。基本設計 6.7）。
// <C-w> と、<C-w> の後のキー（State がキーの列の途中）では閉じない。<C-w>w がフォーカスを入れるためである。
func (e *Editor) dismissFloat(k editcmd.Key) {
	f := e.float
	if f == nil || f.focused || !f.transient || k == ctrlW || e.cur.state.Pending() {
		return
	}
	e.closeFloat(true)
}

// ctrlW は、<C-w> のキーである。
var ctrlW = editcmd.MustParseKeys("<C-w>")[0]

// floatKey は、フォーカスのあるポップアップで、利用者のキー k をポップアップの操作として扱えれば扱い、true を返す（handleKey）。
//
//   - contributes.popupKeys のキー（拡張機能のポップアップの、ノーマルモード）: 持ち主へ view/key を送る（FR-69）
//   - ノーマルモードの <Esc>: ポップアップを閉じる（FR-68）
//   - 一覧と木のノーマルモードの <CR>: カーソルの行の項目を選ぶ。木で子を持つ項目なら開閉する
//   - 入力の行のノーマルモードかインサートモードの <CR>: 中身を選んだものとして知らせる
//
// どれもキーの列の途中（State.Pending、キーマップの途中）では扱わない。
//
// req: FR-68
func (e *Editor) floatKey(k editcmd.Key) bool {
	f := e.float
	if f == nil || !f.focused {
		return false
	}
	st := e.cur.state
	mode := st.Mode()
	if st.Pending() || e.remap.pendingKeys() || (mode != command.ModeNormal && mode != command.ModeInsert) {
		return false
	}
	if mode == command.ModeInsert {
		if f.kind == floatInput && k == editcmd.Special(editcmd.CodeEnter) {
			e.selectFloat(f)
			return true
		}
		return false
	}
	if f.ext != "" && e.popupKey(f, k) {
		return true
	}
	switch {
	case k == editcmd.Esc:
		e.closeFloat(true)
		return true
	case k == editcmd.Special(editcmd.CodeEnter) && f.kind != floatText:
		e.selectFloat(f)
		return true
	}
	return false
}

// popupKey は、拡張機能のポップアップ f で、k までのキーの列が持ち主の contributes.popupKeys のキーに当たれば view/key を送る（FR-69）。
// キーの列の途中なら覚えて true を返す。当たらなければ、覚えていたキーを通常のキーとして処理してから false を返す（k は呼び出し側が処理する）。
//
// req: FR-69
func (e *Editor) popupKey(f *floatState, k editcmd.Key) bool {
	keys := e.popupKeys[f.ext]
	if len(keys) == 0 {
		return false
	}
	seq := append(slices.Clone(f.pending), k)
	prefix := false
	for _, pk := range keys {
		switch {
		case slices.Equal(pk.keys, seq):
			f.pending = nil
			item := ""
			if f.kind == floatInput {
				item = f.bs.buf.Line(0)
			} else if row := f.bs.state.Cursor().Line; row < len(f.rows) {
				item = f.rows[row].id
			}
			e.notifyView(f, ext.MethodViewKey, ext.ViewKeyParams{View: ext.ViewID(f.id), ID: pk.id, Item: item})
			return true
		case hasKeyPrefix(pk.keys, seq):
			prefix = true
		}
	}
	if prefix {
		f.pending = seq
		return true
	}
	held := f.pending
	f.pending = nil
	for _, h := range held {
		e.feedMapped(e.remap.feed(e.mapMode(), h))
		if e.suspension() != nil {
			break
		}
	}
	return false
}

// popupKeyBinding は、contributes.popupKeys の 1 項目を、キーの列にしたものである。
type popupKeyBinding struct {
	keys []editcmd.Key
	id   string
}

// loadPopupKeys は、拡張機能の宣言ファイルの contributes.popupKeys を、拡張機能ごとのキーの列の表にする（FR-69）。
// キーの表記の誤りは、その項目を捨てて誤りを返す。
func loadPopupKeys(specs []exthost.Spec, leader []editcmd.Key) (map[string][]popupKeyBinding, []error) {
	out := map[string][]popupKeyBinding{}
	var errs []error
	for _, s := range specs {
		m := s.Manifest
		for _, c := range m.Contributes.PopupKeys {
			ks, err := editcmd.ParseKeysLeader(c.Keys, leader)
			if err == nil && len(ks) == 0 {
				err = errors.New("empty key sequence")
			}
			if err != nil {
				errs = append(errs, fmt.Errorf("拡張機能 %s の popupKeys の %s のキー %q を読めない: %w", m.ID, c.ID, c.Keys, err))
				continue
			}
			out[m.ID] = append(out[m.ID], popupKeyBinding{keys: ks, id: c.ID})
		}
	}
	return out, errs
}

// selectFloat は、ポップアップ f のカーソルの行の項目を選ぶ。木で子を持つ項目なら開閉する。
// 本体の一覧なら onSelect を呼び、拡張機能のポップアップなら view/select を送る。入力の行は中身を送る。
func (e *Editor) selectFloat(f *floatState) {
	if f.kind == floatInput {
		e.notifyView(f, ext.MethodViewSelect, ext.ViewSelectParams{View: ext.ViewID(f.id), Item: f.bs.buf.Line(0)})
		return
	}
	row := f.bs.state.Cursor().Line
	if row >= len(f.rows) {
		return
	}
	r := f.rows[row]
	if f.kind == floatTree && r.parent {
		f.expanded[r.id] = !f.expanded[r.id]
		e.setFloatContent(f, ext.ViewComponent{Kind: ext.ViewTree, Items: f.tree}, false)
		return
	}
	if f.onSelect != nil {
		f.onSelect(row)
		return
	}
	e.notifyView(f, ext.MethodViewSelect, ext.ViewSelectParams{View: ext.ViewID(f.id), Item: r.id})
}

// notifyView は、拡張機能のポップアップ f の持ち主へ通知を送る。持ち主がいなければ送らない。
func (e *Editor) notifyView(f *floatState, method string, params any) {
	if f.ext == "" {
		return
	}
	if err := e.host.Notify(f.ext, method, params); err != nil {
		e.log.Warn("ポップアップの知らせを送れない", "extension", f.ext, "method", method, "err", err)
	}
}

// afterFloatKey は、キーを処理した後に、入力の行の中身が変わっていれば input/change で知らせる（FR-72）。
func (e *Editor) afterFloatKey() {
	f := e.float
	if f == nil || f.kind != floatInput || !slices.Contains(e.bufs, f.bs) {
		return
	}
	if text := f.bs.buf.Line(0); text != f.input {
		f.input = text
		e.notifyView(f, ext.MethodInputChange, ext.InputChangeParams{View: ext.ViewID(f.id), Text: text})
	}
}

// floatLines は、部品 c をバッファの中身にした行と、一覧と木の行ごとの項目と、色を付ける範囲を返す。
// 木は、開いた項目の子を 2 列ずつ字下げし、子を持つ項目の前に開閉の印（▸ と ▾）を置く。
func floatLines(c ext.ViewComponent, expanded map[string]bool) (lines []string, rows []floatRow, decs []ext.Decoration) {
	switch c.Kind {
	case ext.ViewText:
		text := c.Text
		if c.Format == ext.PopupMarkdown {
			text = ui.StripCodeFences(text)
		}
		return strings.Split(strings.TrimRight(text, "\n"), "\n"), nil, c.Highlights
	case ext.ViewInput:
		return []string{c.Text}, nil, nil
	case ext.ViewList, ext.ViewTree:
	}
	var walk func(items []ext.ViewItem, depth int)
	walk = func(items []ext.ViewItem, depth int) {
		for _, it := range items {
			prefix := ""
			if c.Kind == ext.ViewTree {
				mark := "  "
				if len(it.Children) > 0 {
					mark = "▸ "
					if expanded[it.ID] {
						mark = "▾ "
					}
				}
				prefix = strings.Repeat("  ", depth) + mark
			}
			line := len(lines)
			lines = append(lines, prefix+it.Label)
			rows = append(rows, floatRow{id: it.ID, parent: len(it.Children) > 0})
			for _, s := range it.Highlights {
				decs = append(decs, ext.Decoration{
					Range: ext.Range{
						Start: ext.Position{Line: line, Byte: len(prefix) + s.Start},
						End:   ext.Position{Line: line, Byte: len(prefix) + s.End},
					},
					Style: s.Style,
				})
			}
			if c.Kind == ext.ViewTree && expanded[it.ID] {
				walk(it.Children, depth+1)
			}
		}
	}
	walk(c.Items, 0)
	if len(lines) == 0 {
		lines = []string{""}
	}
	return lines, rows, decs
}

// initExpanded は、木の項目の最初の開閉の状態（ext.ViewItem.Expanded）を集める。
func initExpanded(items []ext.ViewItem, m map[string]bool) {
	for _, it := range items {
		if it.Expanded {
			m[it.ID] = true
		}
		initExpanded(it.Children, m)
	}
}

// setFloatContent は、ポップアップ f のバッファの中身を部品 c にする。カーソルの行は、行数に収まる範囲で保つ。
// 中身を替えるのは、編集の禁止の判定を通らない読み直しの入り口で行う（基本設計 4.6 の「ファイルでないバッファの中身の更新」）。
// reset なら、木の開閉の状態を c の Expanded に戻す。
func (e *Editor) setFloatContent(f *floatState, c ext.ViewComponent, reset bool) {
	if c.Kind == ext.ViewTree {
		f.tree = c.Items
		if reset || f.expanded == nil {
			f.expanded = map[string]bool{}
			initExpanded(c.Items, f.expanded)
		}
	}
	lines, rows, decs := floatLines(c, f.expanded)
	f.rows = rows
	cursor := f.bs.state.Cursor()
	e.reload(f.bs, buffer.New(strings.Join(lines, "\n")))
	line := min(cursor.Line, f.bs.buf.LineCount()-1)
	f.bs.state.SetCursor(buffer.Position{Line: line, Byte: min(cursor.Byte, len(f.bs.buf.Line(line)))})
	if c.Kind == ext.ViewInput {
		f.input = c.Text
	}
	e.setPopupDecorations(f.bs, decs)
}

// setPopupDecorations は、ポップアップのバッファ bs の色を付ける範囲を decs にする。
func (e *Editor) setPopupDecorations(bs *bufferState, decs []ext.Decoration) {
	e.setDecorations(popupDecorationOwner, ext.DecorationSetParams{Buffer: bs.id, Version: ext.Version(bs.buf.Version()), Decorations: decs})
}

// newComponentFloat は、部品 c を中身にしたポップアップを作る。ext は持ち主の拡張機能の ID（本体の部品なら空）である。
// 開くのは呼び出し側が openFloat で行う。
func (e *Editor) newComponentFloat(c ext.ViewComponent, extID string) *floatState {
	f := &floatState{ext: extID, title: c.Title, scratch: true}
	switch c.Kind {
	case ext.ViewList:
		f.kind = floatList
	case ext.ViewTree:
		f.kind = floatTree
		f.expanded = map[string]bool{}
		initExpanded(c.Items, f.expanded)
		f.tree = c.Items
	case ext.ViewInput:
		f.kind = floatInput
		f.input = c.Text
	case ext.ViewText:
	}
	lines, rows, decs := floatLines(c, f.expanded)
	f.rows = rows
	title := c.Title
	if title == "" {
		title = "[ポップアップ]"
	}
	f.bs = e.newPopupBuffer(title, strings.Join(lines, "\n"), "", f.kind != floatInput)
	if len(decs) > 0 {
		e.setPopupDecorations(f.bs, decs)
	}
	if f.kind == floatInput {
		f.bs.state.SetCursor(buffer.Position{Byte: len(c.Text)})
	}
	return f
}

// popupOpenRequest は、拡張機能の popup/open を処理する（FR-68、FR-72、FR-115）。
// その拡張機能へ最後に問い合わせを始めた client（呼び出し側が読み込む）の画面に出し、ポップアップの番号を応答する。
//
// req: FR-72
func (e *Editor) popupOpenRequest(ev *exthost.PopupOpenRequest) {
	p := ev.Params
	if e.cur == nil {
		_ = ev.ReplyError(errors.New("no client to show the popup"))
		return
	}
	f := e.newComponentFloat(p.Component, ev.Extension)
	f.center = p.Placement == ext.PopupCenter
	f.focused = f.center || p.Focus
	f.transient = !f.focused
	e.openFloat(f)
	_ = ev.Reply(ext.PopupOpenResult{View: ext.ViewID(f.id)})
}

// findViewFloat は、拡張機能 extID が開いたポップアップ id を出している client と、そのポップアップを返す。無ければ nil である。
func (e *Editor) findViewFloat(extID string, id ext.ViewID) (*clientView, *floatState) {
	for _, c := range e.clients {
		if c.detached {
			continue
		}
		if f := e.viewOf(c).float; f != nil && f.ext == extID && ext.ViewID(f.id) == id {
			return c, f
		}
	}
	return nil, nil
}

// viewUpdate は、拡張機能の view/update を処理する。部品の種類が開いたときと違うか、知らないポップアップなら捨てる。
func (e *Editor) viewUpdate(ev *exthost.ViewUpdate) {
	c, f := e.findViewFloat(ev.Extension, ev.Params.View)
	if f == nil || componentKind(ev.Params.Component.Kind) != f.kind {
		return
	}
	e.withView(c, func() {
		if ev.Params.Component.Title != "" {
			f.title = ev.Params.Component.Title
		}
		e.setFloatContent(f, ev.Params.Component, true)
	})
}

// viewClose は、拡張機能の view/close を処理する。知らないポップアップなら捨てる。
func (e *Editor) viewClose(ev *exthost.ViewClose) {
	c, f := e.findViewFloat(ev.Extension, ev.Params.View)
	if f == nil {
		return
	}
	e.withView(c, func() { e.closeFloat(false) })
}

// componentKind は、部品の種類を floatKind にする。
func componentKind(k ext.ViewKind) floatKind {
	switch k {
	case ext.ViewList:
		return floatList
	case ext.ViewTree:
		return floatTree
	case ext.ViewInput:
		return floatInput
	case ext.ViewText:
	}
	return floatText
}

// openTextFloat は、本体が出すテキスト text を、編集を禁止したテキストのバッファの部品のポップアップに出す。
// center でなければカーソルの近くに、フォーカスを入れずに出し、次のキーかカーソルの移動で閉じる。
// center なら画面の中央に、フォーカスを入れて出す。長い行は、窓の幅で空白の位置で折り返す。
func (e *Editor) openTextFloat(title, text string, center bool) *floatState {
	return e.openLinesFloat(title, ui.WrapText(text, ui.PopupTextWidth(e.renderer.Width), e.renderer.TabStop), center)
}

// openLinesFloat は、openTextFloat の、行を折り返さない形である。長い行は窓の中で横にスクロールする（診断の一覧）。
func (e *Editor) openLinesFloat(title string, lines []string, center bool) *floatState {
	f := &floatState{kind: floatText, title: title, scratch: true, center: center, focused: center, transient: !center}
	f.bs = e.newPopupBuffer(cmp.Or(title, "[ポップアップ]"), strings.Join(lines, "\n"), "", true)
	e.openFloat(f)
	return f
}

// showPopup は、popup/show（ホバーの説明）を、カーソルのあるバッファで、バージョン番号が現在と一致し、
// カーソルが表示の基準の位置（Position）にあり、最後の拡張機能の操作の問い合わせの後に次のキーを解釈していない場合だけ、
// テキストのバッファの部品のポップアップとしてカーソルの近くに出す（基本設計 6.7 の段階 3 の表。FR-68）。
//
// フォーカスを入れていない間は、次のキー入力かカーソルの移動で閉じる（基本設計 6.7）ので、届いた時点でそのどちらかが起きていれば、
// 既に消えたものとして出さない。出した後も、State が次のキーを解釈したら閉じる（floatState.trackKeys。expireFloat）。
// マクロの再生の途中のホバー（FR-19。#210）は、再生を止めて結果を待つ間に届いて出る（replayWait）。
// 再生の残りのキーも command.State.KeysFed が数えるので、記録したときと同じく次のキーで消え、再生の後に残らない（#217、#224）。
// <C-w>w でフォーカスを入れると、スクロールして読め、<Esc> で閉じる。
// Markdown は、コードの囲みの行を除き、他の記法は整形しない（#151）。長い行は窓の幅で空白の位置で折り返す（#155）。
// 段階 3 では、バッファをカーソルのあるタブに持つ client の画面に出す（応答が届くまでに、別の client がキーを打っていてもよい。#215）。
//
// req: FR-30, FR-19, FR-68
func (e *Editor) showPopup(p ext.PopupShowParams) {
	bs := e.findBuffer(p.Buffer)
	if bs == nil || bs.owner == nil || uint64(p.Version) != bs.buf.Version() || bs.state.Cursor() != fromExtPos(p.Position) {
		return
	}
	e.withView(bs.owner, func() {
		if e.cur != bs {
			return
		}
		if m := e.popupMark; m.state == bs.state && m.keys != bs.state.KeysFed() {
			return
		}
		text := p.Text
		if p.Format == ext.PopupMarkdown {
			text = ui.StripCodeFences(text)
		}
		f := e.openTextFloat("", text, false)
		f.keys, f.trackKeys = bs.state.KeysFed(), true
	})
}

// peekLinesBefore と peekLinesTotal は、「ポップアップで見る」の写しに入れる、移動先の行より前の行数と、全体の行数である（仮）。
const (
	peekLinesBefore = 3
	peekLinesTotal  = 20
)

// peekOpen は、editor/open の peek（FR-110）を処理する。移動先の位置の周りの行の写しを、編集を禁止したファイルでないバッファとして、
// 読み込んでいる client のカーソルの近くのポップアップに出し、その写しのバッファを返す。
// 移動先が開いているバッファなら、その今の行を写す。開いていなければファイルを読む。
// :tabopen で広げると、写しを閉じて本物のファイルを開き、移動先の位置へ移る（expandFloat）。
//
// req: FR-110
func (e *Editor) peekOpen(p ext.EditorOpenParams) (*bufferState, error) {
	var src *bufferState
	path := p.Path
	if p.Buffer != nil {
		src = e.findBuffer(*p.Buffer)
		if src == nil {
			return nil, invalidParams("unknown buffer %d", *p.Buffer)
		}
		path = src.path
	} else {
		src = e.findPath(e.absPath(p.Path))
	}
	var lines []string
	if src != nil {
		for i := range src.buf.LineCount() {
			lines = append(lines, src.buf.Line(i))
		}
	} else {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("%s を読めない: %w", path, err)
		}
		lines = strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	}
	pos := *p.Position
	at := min(max(pos.Line, 0), max(len(lines)-1, 0))
	start := max(at-peekLinesBefore, 0)
	end := min(start+peekLinesTotal, len(lines))
	name := path
	if name == "" && src != nil {
		name = e.bufferName(src)
	} else if rel, err := filepath.Rel(e.workDir(), path); err == nil && filepath.IsLocal(rel) {
		name = rel
	}
	title := fmt.Sprintf("%s:%d", name, at+1)
	bs := e.newPopupBuffer(title, strings.Join(lines[start:end], "\n"), e.detectFileType(path), true)
	line := at - start
	bs.state.SetCursor(buffer.Position{Line: line, Byte: min(max(pos.Byte, 0), len(bs.buf.Line(line)))})
	target := ext.EditorOpenParams{Position: p.Position, Focus: true}
	if path != "" {
		target.Path = path
	} else {
		id := src.id
		target.Buffer = &id
	}
	f := &floatState{kind: floatText, bs: bs, title: title, scratch: true, transient: true, peek: &target}
	e.openFloat(f)
	return bs, nil
}

// floatAction は、ポップアップの Ex コマンドとキーの動作（ex.ActionPopup）を行う。bs は、コマンドを打ったバッファである。
//
// req: FR-68, FR-70
func (e *Editor) floatAction(bs *bufferState, a ex.Action) error {
	switch a.Popup {
	case ex.PopupFocus:
		f := e.float
		switch {
		case f == nil:
			return errors.New("ポップアップがありません")
		case f.focused && f.center:
			// 中央のポップアップは、開いている間はそれだけを操作する（FR-68）。
			return errors.New("中央のポップアップからは移れません（<Esc> で閉じます）")
		case f.focused:
			e.unfocusFloat()
		default:
			e.focusFloat()
		}
		return nil
	case ex.PopupShrink:
		return e.shrinkTab(bs)
	case ex.PopupExpand:
		return e.expandFloat()
	}
	return fmt.Errorf("未知のポップアップの操作 %d", a.Popup)
}

// shrinkTab は、:popup で、カーソルのあるタブ bs をポップアップへ縮める（FR-70）。
// バッファの番号、内容、未保存の変更、カーソルは変えない。縮めたバッファは、この client のタブとして数えたまま（owner を保つ）
// タブの並びから外し、残ったタブのうち裏のタブ（無ければ隣のタブ）へ移ってから、画面の中央にフォーカスを入れて出す。
// ポップアップを閉じたら、タブの並びへ戻す。タブが 1 つだけなら縮めない。
//
// req: FR-70
func (e *Editor) shrinkTab(bs *bufferState) error {
	if f := e.float; f != nil && f.focused {
		return errors.New("ポップアップの中では :popup を使えません")
	}
	c := e.view
	i := slices.Index(c.tabs, bs)
	if i < 0 || bs != e.cur {
		return fmt.Errorf("内部の誤り: カーソルのあるバッファ %d がタブにありません", bs.id)
	}
	if len(c.tabs) == 1 {
		return errors.New("タブが 1 つだけなので、ポップアップへ縮められません")
	}
	var next *bufferState
	switch {
	case e.alt != nil && e.alt != bs && e.alt.owner == c && slices.Contains(c.tabs, e.alt):
		next = e.alt
	case i+1 < len(c.tabs):
		next = c.tabs[i+1]
	default:
		next = c.tabs[i-1]
	}
	e.enter(next)
	c.tabs = slices.Delete(c.tabs, i, i+1)
	f := &floatState{kind: floatText, bs: bs, title: e.bufferName(bs), shrunk: true, center: true, focused: true}
	e.openFloat(f)
	return nil
}

// expandFloat は、:tabopen で、読み込んでいる client のポップアップのバッファをタブへ広げる（FR-70、FR-110）。
//
//   - :popup で縮めたタブ: タブの並びへ戻し、そのタブへ移る
//   - 「ポップアップで見る」の写し: 写しを閉じ、本物のファイルを開いて移動先の位置へ移る。本物が別の client のタブなら、
//     その client のペインへフォーカスを移す（FR-63。editorOpen と同じ）
//   - 本体と拡張機能のテキスト（ヘルプ、ホバー）: そのバッファを新しいタブにする。拡張機能のポップアップなら view/closed を送る
//
// 一覧、木、入力の行はタブにできないので断る。
//
// req: FR-70, FR-110
func (e *Editor) expandFloat() error {
	f := e.float
	if f == nil {
		return errors.New("ポップアップがありません")
	}
	if f.kind != floatText {
		return errors.New("一覧、木、入力の行のポップアップはタブへ広げられません")
	}
	switch {
	case f.peek != nil:
		target := *f.peek
		e.closeFloat(true)
		if _, _, err := e.editorOpen(target); err != nil {
			return err
		}
		return nil
	case f.shrunk:
		f.shrunk = false
		e.closeFloat(true)
		e.restoreTab(f.bs, f.base)
		e.enter(f.bs)
		return nil
	}
	f.scratch = false
	e.closeFloat(true)
	f.bs.popup = false
	e.enter(f.bs)
	return nil
}

// floatFrame は、ポップアップ f の描画の入力を作る。
func (e *Editor) floatFrame(f *floatState) *ui.Float {
	st := f.bs.state
	view := &ui.Frame{
		Text:        f.bs.buf,
		Cursor:      st.Cursor(),
		Decorations: f.bs.decoView,
		Diagnostics: f.bs.uiDiagnostics(),
		Signs:       f.bs.uiSigns(),
	}
	if f.focused {
		view.Mode = uiMode(st.Mode())
		view.OperatorPending = st.Mode() == command.ModeNormal && st.Pending()
		if start, end, linewise, ok := st.Selection(); ok {
			view.Selection = ui.Selection{Active: true, Start: start, End: end, Linewise: linewise}
		}
	}
	return &ui.Float{
		ID: f.id, View: view, Title: f.title, Center: f.center, Focused: f.focused,
		CursorLine: f.kind == floatList || f.kind == floatTree,
	}
}
