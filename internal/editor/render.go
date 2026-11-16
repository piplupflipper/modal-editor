package editor

import (
	"strings"

	"github.com/piplupflipper/modal-editor/internal/command"
	"github.com/piplupflipper/modal-editor/internal/ui"
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
// 問いが無ければ、ポップアップをカーソルの近くに浮かべる。問いもポップアップもメッセージも無く、ノーマルモードで
// キーの列の途中でなければ、カーソルの行の診断の本文をメッセージの行に出す（cursorDiagnostic）。
func (e *Editor) frame() *ui.Frame {
	bs := e.cur
	st := bs.state
	f := &ui.Frame{
		Text:            bs.buf,
		Name:            bs.path,
		Modified:        bs.buf.Modified(),
		Mode:            uiMode(st.Mode()),
		Cursor:          st.Cursor(),
		CommandLine:     st.CommandLine(),
		CommandLineType: st.CommandLineType(),
		Recording:       st.Recording(),
		Message:         e.message,
		Decorations:     bs.decoView,
		Diagnostics:     bs.uiDiagnostics(),
		Signs:           bs.uiSigns(),
		Output:          e.uiOutput(),
	}
	f.HitEnter = e.hitEnter()
	f.OperatorPending = st.Mode() == command.ModeNormal && st.Pending()
	if start, end, linewise, ok := st.Selection(); ok {
		f.Selection = ui.Selection{Active: true, Start: start, End: end, Linewise: linewise}
	}
	if p := e.popup; p != nil && (p.bs != bs || p.cursor != st.Cursor()) {
		e.popup = nil
	}
	switch {
	case e.confirm != nil:
		f.Message = ui.Message{Text: e.confirm.Prompt, Level: ui.MessageWarning}
	case e.prompt != nil:
		f.Message = ui.Message{Text: e.prompt.text, Level: ui.MessageWarning}
	case e.activeChoice() != nil:
		f.Choice = uiChoice(e.choices[0])
	case e.popup != nil:
		f.Popup = e.popup.text
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

// render は、画面を描いて端末へ出す。
func (e *Editor) render() {
	if e.renderer.Width <= 0 || e.renderer.Height <= 0 {
		return
	}
	scr := e.renderer.Render(e.frame())
	if err := e.term.Draw(scr); err != nil {
		e.log.Error("描画に失敗", "err", err)
	}
}
