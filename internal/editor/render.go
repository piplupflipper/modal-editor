package editor

import (
	"fmt"
	"strings"

	"github.com/piplupflipper/vi-imitation/internal/command"
	"github.com/piplupflipper/vi-imitation/internal/ui"
)

// uiMode は、command のモードを描画のモードにする。
func uiMode(m command.Mode) ui.Mode {
	switch m {
	case command.ModeInsert:
		return ui.ModeInsert
	case command.ModeVisual:
		return ui.ModeVisual
	case command.ModeVisualLine:
		return ui.ModeVisualLine
	case command.ModeCommandLine:
		return ui.ModeCommandLine
	case command.ModeNormal:
	}
	return ui.ModeNormal
}

// frame は、カーソルのあるバッファの状態と表示のデータから、描画の入力を作る。
// コマンドラインの行には、確認の問い、選択肢の問い、メッセージの順に、最初にあるものを出す。
// 問いが無ければ、ポップアップを浮かべる。問いもポップアップもメッセージも無く、ノーマルモードで
// キーの列の途中でなければ、カーソルの行の診断の本文をメッセージの行に出す（cursorDiagnostic）。
//
// フォーカスのあるポップアップがあれば、バッファの表示領域には開いたときのタブ（tab）を描き、
// ステータス行、モード、コマンドラインは、キーを受けるポップアップのバッファ（cur）のものを出す。
func (e *Editor) frame() *ui.Frame {
	bs := e.tab()
	cur := e.cur
	st := cur.state
	f := &ui.Frame{
		Text:            bs.buf,
		Name:            bufferTitle(cur),
		Modified:        cur.buf.Modified(),
		ReadOnly:        cur.buf.ReadOnly(),
		FileType:        cur.fileType,
		Mode:            uiMode(st.Mode()),
		Cursor:          bs.state.Cursor(),
		CommandLine:     st.CommandLine(),
		CommandLineType: st.CommandLineType(),
		Recording:       st.Recording(),
		Message:         e.message,
		Decorations:     bs.decoView,
		Diagnostics:     bs.uiDiagnostics(),
		Signs:           bs.uiSigns(),
		Output:          e.uiOutput(),
		Tabs:            e.uiTabs(),
		Diff:            e.uiDiff(bs),
		ExtParts:        e.extParts,
	}
	f.HitEnter = e.hitEnter()
	f.OperatorPending = st.Mode() == command.ModeNormal && st.Pending()
	if start, end, linewise, ok := st.Selection(); ok && cur == bs {
		f.Selection = ui.Selection{Active: true, Start: start, End: end, Linewise: linewise}
	}
	// 問いは、問いを出した client の画面にだけ出す（clients.go の doc）。
	question := e.showsQuestion()
	switch {
	case question && e.confirm != nil:
		f.Message = ui.Message{Text: e.confirm.Prompt, Level: ui.MessageWarning}
	case question && e.prompt != nil:
		f.Message = ui.Message{Text: e.prompt.text, Level: ui.MessageWarning}
	case question && e.activeChoice() != nil:
		f.Choice = e.uiChoice()
		f.List = e.uiRegList()
	case !question && (e.asking() || e.activeChoice() != nil):
		// 問いの間は、このペインのキーを処理せずに待たせるので、その理由を出す（PR #223 のレビューの推奨 2）。
		q := e.asker
		if q == nil {
			q = e.op
		}
		f.Message = ui.Message{Text: fmt.Sprintf("client %d の問いの答えを待っています（答えるまで、このペインのキーは処理しません）", q.info.ID), Level: ui.MessageWarning}
	case e.regList != nil:
		f.List = e.uiRegList()
	case e.float != nil:
		if e.float.remote == nil {
			// 部品の client が描くポップアップ（端末多重化ソフトのポップアップの中。component.go）は、この画面には描かない。
			f.Float = e.floatFrame(e.float)
		}
	case f.Message.Text == "" && st.Mode() == command.ModeNormal && !st.Pending():
		// 他に出すものが無ければ、カーソルの行の診断の本文を出す。
		if m, ok := bs.cursorDiagnostic(toExtPos(st.Cursor())); ok {
			f.Message = m
		}
	}
	return f
}

// hitEnter は、改行を含むメッセージを出していて、Vim の hit-enter の問いと同じくキーを待っているかを返す。
// 待っている間は、メッセージの下に問いを出してカーソルをその末尾に置き（ui.Frame.HitEnter）、<CR> と <Space> はメッセージを消すだけにする（handleKey）。
// 確認の問いと選択肢の問いを出している間と、コマンドラインモード（コマンドラインの行にメッセージを出さない）では待たない。
// モードは問わない。インサートモードの間に拡張機能のメッセージが届いても待つ（Vim も、インサートモードの間に出た複数行のメッセージで hit-enter の問いを出す）。
func (e *Editor) hitEnter() bool {
	return !e.asking() && e.activeChoice() == nil && e.cur.state.Mode() != command.ModeCommandLine &&
		strings.Contains(strings.TrimRight(e.message.Text, "\n"), "\n")
}

// viewport は、描画の表示領域（ui.Renderer の縦のスクロールの位置）を、command.State のページ単位のスクロール
// （<PageUp>、<PageDown>）へ渡す口である（command.Viewport）。
type viewport struct{ r *ui.Renderer }

// Top は、表示領域の最初の行を返す。
func (v viewport) Top() int { return v.r.Top() }

// Rows は、表示領域の行数を返す。直前の描画で、ステータス行とコマンドラインの行と出力の欄を除いた行数である。
// 画面の大きさがまだ届いていないとき（0 行）も、1 行として扱う。
func (v viewport) Rows() int { return max(v.r.TextRows(), 1) }

// SetTop は、表示領域の最初の行を変える。横のスクロールの位置は変えない。
func (v viewport) SetTop(top int) { v.r.SetScroll(top, v.r.Left()) }

// render は、繋いでいるすべての client の画面を描いて、それぞれの端末へ出す。
// 描き終えたら、読み込む client を描く前のものに戻す。
// 見比べている組は、描く前に差分の計算し直しを始め、描いた後に両側のスクロールをそろえる（diff.go）。
func (e *Editor) render() {
	e.syncAsker()
	e.refreshDiffs()
	e.eachView(func(*clientView) {
		e.expireFloat()
		e.renderView()
		e.renderComponents()
	})
	e.syncDiffScroll()
}

// renderView は、読み込んでいる client の画面を描いて端末へ出す。
func (e *Editor) renderView() {
	if e.cur == nil || e.quit || e.renderer.Width <= 0 || e.renderer.Height <= 0 {
		return
	}
	scr := e.renderer.Render(e.frame())
	if err := e.term.Draw(scr); err != nil {
		e.log.Error("描画に失敗", "err", err)
	}
}
