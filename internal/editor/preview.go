package editor

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/piplupflipper/vi-imitation/ext"
	"github.com/piplupflipper/vi-imitation/internal/buffer"
	"github.com/piplupflipper/vi-imitation/internal/option"
	"github.com/piplupflipper/vi-imitation/internal/ui"
)

// 一覧、木、ファインダーのカーソルの項目のプレビュー（基本設計 1.4 の「#288 のプレビュー」。FR-149）。
//
// # 汎用の口
//
// 拡張機能は、項目ごとにプレビューに出すもの（ext.ViewItem.Preview。ファイルと行、ディレクトリ、開いているバッファ）の場所だけを付ける。
// 中身は本体が読み、ファインダーとファイラのための処理は持たない。窓に出すかは、窓の幅の規則（ui.PreviewRule.Layout）だけで決め、
// 出す場所（中央のポップアップ、端末多重化ソフトのポップアップ、分割したペイン）では分けない。
//
// # 開く時点
//
// 描く前（render）に、client ごとの部品のカーソルの項目から、出したいもの（previewState.pending）を決める。
// 今出しているものと違えば、previewDelay の間を置くタイマーを掛け直し、時間が来たらイベントループで開く（loadPreview）。
// 続けてカーソルを動かしている間は掛け直し続けるので、止まってから開く。プレビューのバッファを開くと拡張機能（tree-sitter など）が
// 起動して解析するので、通り過ぎる項目ごとに開き直さないためである。ファイルとディレクトリは、イベントループの外の goroutine で読む。
// 同じファイル（同じバッファ）の別の行へ移ったときは、読み直さずにカーソルを移す。
//
// # バッファ
//
// ファイルとディレクトリの中身は、編集を禁止したファイルでないバッファ（ポップアップのバッファと同じく :ls に出さない）にして出す。
// ファイルの種類はファイルの名前から決めるので、tree-sitter が構文の色を付ける。開いているバッファ（ファイルのバッファを含む）は、
// 写さずにそのバッファを出し、カーソルとスクロールだけをプレビューの側に持つ（previewState.owned が false）。
// プレビューのバッファは、別の項目のプレビューに替えたときと、ポップアップを閉じたときに閉じる。
//
// # フォーカス
//
// プレビューを出している窓で <C-w>w を打つと、フォーカスをプレビューへ移し、ノーマルモードのキー（/、j、k、<C-d> など）で読める（FR-71）。
// <C-w>w か <Esc> で一覧（ファインダーの入力の行）へ戻る。開いているバッファを出しているときは、移る前に編集を禁止した写しに替える
// （プレビューの中のカーソルの移動で、タブのカーソルを動かさないため）。

// プレビューの読み込みの決まり（仮）。
const (
	// previewDelay は、カーソルの項目が替わってから、プレビューを開くまでの間である。
	previewDelay = 100 * time.Millisecond
	// previewMaxBytes は、中身を出すファイルの大きさの上限である。超えるファイルは、中身を出さずに理由を出す。
	previewMaxBytes = 1 << 20
	// previewBinaryScan は、バイナリのファイルかを決めるために NUL のバイトを探す、先頭のバイト数である（git の判定と同じ 8000）。
	previewBinaryScan = 8000
	// previewMaxEntries は、ディレクトリの中身の一覧に出す数の上限である。
	previewMaxEntries = 1000
)

// previewState は、ポップアップかペインの部品 1 つの、カーソルの項目のプレビューである（floatState.preview）。
type previewState struct {
	// key は、出しているもの（previewKey）である。空なら何も出していない。
	key string
	// pending は、出そうとしている（タイマーを掛けたか、読んでいる）もので、loading はそれを持っていることを表す。
	pending string
	loading bool
	// gen は、出そうとするものを替えるたびに進める番号である。古いタイマーと読み込みの結果を捨てる。
	gen uint64
	// stop は、掛けたタイマーを止める。
	stop func() bool
	// bs は、出しているバッファである。owned なら、このプレビューのために作ったバッファで、替えるときと閉じるときに閉じる。
	// owned でなければ、開いているバッファをそのまま出している。source は、bs の中身の出どころ（previewSource）である。
	bs     *bufferState
	owned  bool
	source string
	// line は、色を付ける行（0 から）で、負なら付けない。title は見出し、note は中身を出さない理由である。
	line  int
	title string
	note  string
	// id は、出すものを替えるたびに付け直す番号（ui.Preview.ID）である。描く側は、替わったら line を真ん中へスクロールし直す。
	id uint64
	// focus は、フォーカスをプレビューへ移したことを表す。キーは bs の State へ入る。
	focus bool
}

// forget は、閉じたバッファ bs をプレビューに出していれば、出していないことにする。次の描画で出し直す。
func (p *previewState) forget(bs *bufferState) {
	if p.bs != bs {
		return
	}
	p.bs, p.owned, p.source, p.key, p.focus = nil, false, "", "", false
}

// previewRule は、設定の表の preview.* の今の値を返す（基本設計 7.3 の「エディタ全体」の項目は、使うたびに読む）。
func (e *Editor) previewRule() ui.PreviewRule {
	return ui.PreviewRule{
		MinWindowWidth:     e.options.Get(option.PreviewMinWindowWidth).Int(),
		WidthPercent:       e.options.Get(option.PreviewWidthPercent).Int(),
		WindowWidthPercent: e.options.Get(option.PreviewWindowWidthPercent).Int(),
	}
}

// previewCapable は、部品 f がプレビューの欄を持つ一覧、木、ファインダーかを返す。
func (e *Editor) previewCapable(f *floatState) bool {
	return f.hasPreview && (f.kind == floatList || f.kind == floatTree || f.kind == floatFinder)
}

// previewShown は、部品 f の窓にプレビューを出すかを返す。ui の描き方と同じ規則（ui.PreviewRule.Layout）で決める。
//
//   - 中央のポップアップ: 編集の client の画面の中央に描くときも、端末多重化ソフトのポップアップに出すときも、開いた client の画面の幅の
//     preview.windowWidthPercent % を窓の幅として決める。端末多重化ソフトのポップアップは、出すときにその百分率の幅で開く（requestPopup）。
//     出す場所で分けず、同じ設定の項目と同じ規則で決めるためである
//   - 分割したペイン: そのペインの部品の client の画面の全体が窓である
//   - カーソルの近くの窓: 出さない
//
// 読み込んでいる client が f を開いた client であるときに呼ぶ（e.renderer がその画面の描画の部品である）。
//
// req: FR-149
func (e *Editor) previewShown(f *floatState) bool {
	if !e.previewCapable(f) {
		return false
	}
	switch {
	case f.pane && f.remote != nil:
		_, _, ok := e.previewRule().Layout(f.remote.r.Width, true)
		return ok
	case f.center:
		_, _, ok := e.previewRule().Layout(e.renderer.Width, false)
		return ok
	}
	return false
}

// cursorPreview は、部品 f のカーソルの項目のプレビューに出すものを返す。無ければ nil である。
func cursorPreview(f *floatState) *ext.ViewPreview {
	src := f.bs
	if f.kind == floatFinder {
		src = f.list
	}
	if row := src.state.Cursor().Line; row < len(f.rows) {
		return f.rows[row].preview
	}
	return nil
}

// previewSource は、プレビューに出すもの pv の中身の出どころを、開いているバッファなら "b:<番号>"、それ以外は "p:<絶対パス>" で返す。
// 相対パスは、読み込んでいる client の作業ディレクトリから数える。
func (e *Editor) previewSource(pv *ext.ViewPreview) string {
	if pv.Buffer != nil {
		return "b:" + strconv.FormatUint(uint64(*pv.Buffer), 10)
	}
	return "p:" + e.absPath(pv.Path)
}

// previewKey は、プレビューに出すもの pv を見分ける文字列（出どころと行）を返す。pv が nil なら空である。
func (e *Editor) previewKey(pv *ext.ViewPreview) string {
	if pv == nil {
		return ""
	}
	line := "-"
	if pv.Line != nil {
		line = strconv.Itoa(*pv.Line)
	}
	return e.previewSource(pv) + "#" + line
}

// syncPreviews は、読み込んでいる client のポップアップとペインの部品のプレビューを、カーソルの項目に合わせる。描く前に呼ぶ。
func (e *Editor) syncPreviews() {
	if f := e.float; f != nil {
		e.syncPreview(f)
	}
	for _, p := range e.panes {
		e.syncPreview(p)
	}
}

// syncPreview は、部品 f に出したいプレビュー（窓に出さないか、カーソルの項目にプレビューが無ければ空）が、出しているものと違えば、
// previewDelay の後に開くタイマーを掛ける（パッケージの doc の「開く時点」）。既に同じものを開こうとしていれば何もしない。
// 出したいものが出しているものに戻ったら、掛けたタイマーを止める。バッファの開閉はここでは行わず、タイマーの先（loadPreview）で行う。
//
// req: FR-149
func (e *Editor) syncPreview(f *floatState) {
	p := &f.preview
	var pv *ext.ViewPreview
	if e.previewShown(f) {
		pv = cursorPreview(f)
	}
	want := e.previewKey(pv)
	if p.loading && want == p.pending {
		return
	}
	if want == p.key {
		e.cancelPreview(p)
		return
	}
	e.cancelPreview(p)
	p.gen++
	p.pending, p.loading = want, true
	gen, c := p.gen, e.view
	stop := e.stop
	p.stop = e.afterFunc(previewDelay, func() {
		select {
		case e.inspect <- func() { e.loadPreview(c, f, gen, pv) }:
		case <-stop:
		}
	})
}

// cancelPreview は、プレビューを開くタイマーを止め、読み込み中の結果を捨てさせる。
func (e *Editor) cancelPreview(p *previewState) {
	if !p.loading {
		return
	}
	if p.stop != nil {
		p.stop()
		p.stop = nil
	}
	p.gen++
	p.pending, p.loading = "", false
}

// previewAlive は、client c の部品 f がまだ開いているかを返す。
func (e *Editor) previewAlive(c *clientView, f *floatState) bool {
	if !c.attached() {
		return false
	}
	v := e.viewOf(c)
	return v.float == f || slices.Contains(v.panes, f)
}

// loadPreview は、タイマーの時間が来たときに、client c の部品 f のプレビューを pv にする（イベントループで呼ぶ）。
// gen が今の番号でなければ（その後にカーソルの項目が替わった）捨てる。
//
//   - pv が nil: プレビューを空にする
//   - 出しているバッファと同じ出どころ: 読み直さずに行だけを移す
//   - 開いているバッファ（Buffer か、開いているファイルの Path）: そのバッファを出す
//   - それ以外: イベントループの外でファイルかディレクトリを読み、読み終えたらイベントループで出す（installPreview）
//
// req: FR-149
func (e *Editor) loadPreview(c *clientView, f *floatState, gen uint64, pv *ext.ViewPreview) {
	if !e.previewAlive(c, f) || f.preview.gen != gen {
		return
	}
	e.withView(c, func() {
		p := &f.preview
		p.stop = nil
		key := e.previewKey(pv)
		if pv == nil {
			e.setPreview(f, key, previewContent{}, nil, "")
			return
		}
		line := -1
		if pv.Line != nil {
			line = *pv.Line
		}
		source := e.previewSource(pv)
		if p.bs != nil && p.source == source && slices.Contains(e.bufs, p.bs) {
			e.movePreviewLine(f, key, line, pv)
			return
		}
		var open *bufferState
		if pv.Buffer != nil {
			open = e.findBuffer(*pv.Buffer)
			if open == nil {
				e.setPreview(f, key, previewContent{note: fmt.Sprintf("バッファ %d は開いていません", *pv.Buffer)}, nil, source)
				return
			}
		} else if bs := e.findPath(e.absPath(pv.Path)); bs != nil && !bs.popup {
			open = bs
		}
		if open != nil {
			e.setPreview(f, key, previewContent{line: line, title: e.previewTitle(e.bufferName(open), open.path, line)}, open, source)
			return
		}
		path := e.absPath(pv.Path)
		title := e.previewTitle(path, path, line)
		go func() {
			defer e.recoverPanic()
			content := readPreview(path)
			content.line, content.title = line, title
			if content.dir {
				content.title = strings.TrimSuffix(title, string(filepath.Separator)) + string(filepath.Separator)
			}
			e.later(func() {
				if !e.previewAlive(c, f) || f.preview.gen != gen {
					return
				}
				e.withView(c, func() {
					if !content.dir && content.note == "" {
						content.fileType = e.detectFileType(path)
					}
					e.setPreview(f, key, content, nil, source)
				})
			})
		}()
	})
}

// previewTitle は、プレビューの見出しを返す。name は出す名前で、path が作業ディレクトリの下ならそこからの相対パスにする（「ポップアップで見る」と同じ）。
// line が負でなければ、":行" を足す。
func (e *Editor) previewTitle(name, path string, line int) string {
	if path != "" {
		name = path
		if rel, err := filepath.Rel(e.workDir(), path); err == nil && filepath.IsLocal(rel) {
			name = rel
		}
	}
	if line >= 0 {
		return fmt.Sprintf("%s:%d", name, line+1)
	}
	return name
}

// movePreviewLine は、出しているバッファのまま、色を付ける行を line にする（同じファイルの grep の別の一致）。
func (e *Editor) movePreviewLine(f *floatState, key string, line int, pv *ext.ViewPreview) {
	p := &f.preview
	p.key, p.pending, p.loading = key, "", false
	p.line = line
	name := ""
	if p.bs.path == "" {
		name = e.bufferName(p.bs)
	}
	path := p.bs.path
	if p.owned && pv.Path != "" {
		path = e.absPath(pv.Path)
	}
	p.title = e.previewTitle(name, path, line)
	if p.owned && p.bs.buf.LineCount() > 0 {
		at := max(line, 0)
		p.bs.state.SetCursor(buffer.Position{Line: min(at, p.bs.buf.LineCount()-1)})
	}
	e.previewSeq++
	p.id = e.previewSeq
}

// previewContent は、プレビューに出す中身である。
type previewContent struct {
	// text は、ファイルの中身かディレクトリの一覧である。note が空でなければ読まない。
	text     string
	fileType string
	// dir は、ディレクトリの一覧であることを表す。
	dir bool
	// note は、中身を出さない理由（大きなファイル、読めないファイル、バイナリのファイル、空のディレクトリ）である。
	note  string
	line  int
	title string
}

// setPreview は、部品 f のプレビューを、key の中身にする。前に作ったバッファは閉じる。
// open が nil でなければ、開いているバッファ open をそのまま出す。そうでなく content.note が空なら、content.text を中身にした
// 編集を禁止したファイルでないバッファを作る（ファイルの種類 content.fileType で拡張機能が起動して構文の色を付ける）。
// フォーカスがプレビューにあれば、一覧へ戻してから替える。
func (e *Editor) setPreview(f *floatState, key string, content previewContent, open *bufferState, source string) {
	p := &f.preview
	e.dropPreviewBuffer(f)
	p.key, p.pending, p.loading = key, "", false
	p.line, p.title, p.note, p.source = -1, content.title, content.note, source
	e.previewSeq++
	p.id = e.previewSeq
	switch {
	case key == "":
		p.title = ""
	case open != nil:
		p.bs, p.owned, p.line = open, false, content.line
	case content.note == "":
		bs := e.newPopupBuffer(previewBufferName(content.title), content.text, content.fileType, true)
		bs.state.Viewport = previewViewport{f: f, r: e.renderer}
		p.bs, p.owned, p.line = bs, true, content.line
		if content.dir {
			p.line = -1
		}
		if p.line >= 0 {
			bs.state.SetCursor(buffer.Position{Line: min(p.line, bs.buf.LineCount()-1)})
		}
	}
}

// previewBufferName は、プレビューのバッファの名前（ステータス行に出る）を返す。見出しが空なら決まった名前にする。
func previewBufferName(title string) string {
	if title == "" {
		return "[プレビュー]"
	}
	return title
}

// dropPreviewBuffer は、部品 f のプレビューのバッファを外す。このプレビューのために作ったバッファなら閉じる。
// フォーカスがプレビューにあれば、先に一覧（ファインダーの入力の行）へ戻す。
func (e *Editor) dropPreviewBuffer(f *floatState) {
	p := &f.preview
	if p.focus {
		e.leavePreview(f)
	}
	bs, owned := p.bs, p.owned
	p.bs, p.owned, p.source = nil, false, ""
	if owned && bs != nil && slices.Contains(e.bufs, bs) {
		e.closeBufferState(bs)
	}
}

// closePreview は、部品 f を閉じるときに、プレビューのタイマーを止め、プレビューのために作ったバッファを閉じる（FR-149）。
//
// req: FR-149
func (e *Editor) closePreview(f *floatState) {
	p := &f.preview
	e.cancelPreview(p)
	p.focus = false
	e.dropPreviewBuffer(f)
	p.key = ""
}

// togglePreviewFocus は、<C-w>w で、部品 f の一覧（ファインダーの入力の行）とプレビューの間でフォーカスを移す（FR-149、FR-71）。
// 開いているバッファを出していれば、移る前に編集を禁止した写しに替える。中身の無いプレビューへは移らずに誤りを返す。
//
// req: FR-149
func (e *Editor) togglePreviewFocus(f *floatState) error {
	p := &f.preview
	if p.focus {
		e.leavePreview(f)
		return nil
	}
	if p.bs == nil || !slices.Contains(e.bufs, p.bs) {
		return errors.New("プレビューに中身がありません")
	}
	if !p.owned {
		src := p.bs
		lines := make([]string, src.buf.LineCount())
		for i := range lines {
			lines[i] = src.buf.Line(i)
		}
		bs := e.newPopupBuffer(previewBufferName(p.title), strings.Join(lines, "\n"), src.fileType(), true)
		bs.state.Viewport = previewViewport{f: f, r: e.renderer}
		if n := bs.buf.LineCount(); n > 0 {
			bs.state.SetCursor(buffer.Position{Line: min(max(p.line, 0), n-1)})
		}
		p.bs, p.owned = bs, true
	}
	p.focus = true
	e.switchFocus(p.bs)
	return nil
}

// leavePreview は、プレビューのフォーカスを、部品 f の一覧（ファインダーの入力の行）へ戻す。
func (e *Editor) leavePreview(f *floatState) {
	p := &f.preview
	p.focus = false
	if p.bs != nil && e.cur == p.bs {
		e.switchFocus(f.bs)
	}
}

// uiPreview は、部品 f のプレビューの描画の入力を作る。
func (e *Editor) uiPreview(f *floatState) *ui.Preview {
	p := &f.preview
	out := &ui.Preview{ID: p.id, Title: p.title, Note: p.note, Line: -1, Focused: p.focus}
	bs := p.bs
	if bs == nil || !slices.Contains(e.bufs, bs) {
		out.Focused = false
		return out
	}
	n := bs.buf.LineCount()
	if p.line >= 0 && n > 0 {
		out.Line = min(p.line, n-1)
	}
	view := &ui.Frame{Text: bs.buf, Decorations: bs.decoView}
	switch {
	case p.owned:
		st := bs.state
		view.Cursor = st.Cursor()
		if p.focus {
			view.Mode = uiMode(st.Mode())
			view.OperatorPending = st.Pending()
			if start, end, linewise, ok := st.Selection(); ok {
				view.Selection = ui.Selection{Active: true, Start: start, End: end, Linewise: linewise}
			}
		}
	default:
		// 開いているバッファは、そのカーソルを動かさず、色を付ける行にカーソルを置いた形で描く。
		view.Cursor = buffer.Position{Line: max(out.Line, 0)}
	}
	out.View = view
	return out
}

// previewViewport は、プレビューのバッファのページ単位のスクロール（command.State.Viewport）を、プレビューの表示領域につなぐ口である。
type previewViewport struct {
	f *floatState
	r *ui.Renderer
}

// sub は、プレビューの中を描く子の Renderer を返す。まだ描いていなければ nil である。
func (v previewViewport) sub() *ui.Renderer {
	if c := v.f.remote; c != nil {
		return c.r.PreviewRenderer()
	}
	return v.r.PreviewRenderer()
}

func (v previewViewport) Top() int {
	if s := v.sub(); s != nil {
		return s.Top()
	}
	return 0
}

func (v previewViewport) Rows() int {
	if s := v.sub(); s != nil {
		return max(s.TextRows(), 1)
	}
	return 1
}

func (v previewViewport) SetTop(top int) {
	if s := v.sub(); s != nil {
		s.SetScroll(top, s.Left())
	}
}

// readPreview は、path のファイルかディレクトリを、プレビューの中身として読む。イベントループの外で呼ぶ。
// ディレクトリなら、中身をディレクトリを先に名前の順で並べ、ディレクトリの名前の後に / を付けた一覧にする。
// 読めないもの、通常のファイルでないもの（FIFO などは読むと止まりうる）、大きなファイル（previewMaxBytes を超える）、
// バイナリのファイル（先頭の previewBinaryScan バイトに NUL を含む）は、中身を出さずに理由（note）を返す。
//
// req: FR-149
func readPreview(path string) previewContent {
	fi, err := os.Stat(path)
	if err != nil {
		return previewContent{note: "読めません: " + reason(err)}
	}
	if fi.IsDir() {
		return readPreviewDir(path)
	}
	if !fi.Mode().IsRegular() {
		return previewContent{note: "通常のファイルでないので、中身を出しません"}
	}
	if fi.Size() > previewMaxBytes {
		return previewContent{note: fmt.Sprintf("大きなファイル（%d バイト）なので、中身を出しません（上限は %d バイト）", fi.Size(), previewMaxBytes)}
	}
	fp, err := os.Open(path)
	if err != nil {
		return previewContent{note: "読めません: " + reason(err)}
	}
	defer func() { _ = fp.Close() }() // 読むだけなので、閉じたときの誤りは使わない
	data, err := io.ReadAll(io.LimitReader(fp, previewMaxBytes+1))
	switch {
	case err != nil:
		return previewContent{note: "読めません: " + reason(err)}
	case len(data) > previewMaxBytes:
		return previewContent{note: fmt.Sprintf("大きなファイル（%d バイトを超える）なので、中身を出しません", previewMaxBytes)}
	case bytes.IndexByte(data[:min(len(data), previewBinaryScan)], 0) >= 0:
		return previewContent{note: "バイナリのファイルなので、中身を出しません"}
	}
	return previewContent{text: strings.TrimSuffix(string(data), "\n")}
}

// readPreviewDir は、ディレクトリ dir の中身の一覧を読む（readPreview）。
func readPreviewDir(dir string) previewContent {
	des, err := os.ReadDir(dir)
	if err != nil && len(des) == 0 {
		return previewContent{dir: true, note: "読めません: " + reason(err)}
	}
	type entry struct {
		name string
		dir  bool
	}
	es := make([]entry, 0, len(des))
	for _, d := range des {
		isDir := d.IsDir()
		if d.Type()&fs.ModeSymlink != 0 {
			if fi, serr := os.Stat(filepath.Join(dir, d.Name())); serr == nil {
				isDir = fi.IsDir()
			}
		}
		es = append(es, entry{name: d.Name(), dir: isDir})
	}
	if len(es) == 0 {
		return previewContent{dir: true, note: "空のディレクトリです"}
	}
	slices.SortFunc(es, func(a, b entry) int {
		if a.dir != b.dir {
			if a.dir {
				return -1
			}
			return 1
		}
		return strings.Compare(a.name, b.name)
	})
	var lines []string
	for i, x := range es {
		if i == previewMaxEntries {
			lines = append(lines, fmt.Sprintf("（ほか %d 個）", len(es)-i))
			break
		}
		name := strings.Map(func(r rune) rune {
			if r == '\n' || r == '\r' {
				return ' '
			}
			return r
		}, x.name)
		if x.dir {
			name += string(filepath.Separator)
		}
		lines = append(lines, name)
	}
	return previewContent{dir: true, text: strings.Join(lines, "\n")}
}

// reason は、ファイルを読めなかった理由 err から、パスを除いた文を返す（見出しにパスを出すため）。
func reason(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}
