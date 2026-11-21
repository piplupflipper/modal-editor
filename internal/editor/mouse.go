package editor

import (
	"github.com/piplupflipper/vi-imitation/internal/command"
	"github.com/piplupflipper/vi-imitation/internal/editcmd"
	"github.com/piplupflipper/vi-imitation/internal/option"
	"github.com/piplupflipper/vi-imitation/internal/ui"
)

// このファイルは、マウスの出来事（FR-180〜FR-185）の位置を、client の画面の部品に解いて、command.State へ渡す口である。
//
// マウスの出来事は、端末の解読（internal/term）が <LeftMouse> などのキーにし、位置をキーと一緒に届ける。
// キーは他のキーと同じく利用者のキーマップと操作の表を通り（FR-184）、操作の実装（internal/command の mouse.go）が、
// State.Mouse（mouseLocator）に位置を解かせる。位置は、そのキーを受けた端末の画面の座標で、
// 編集の client なら client の画面の描画の部品（viewFields.renderer）、部品の client（ポップアップとペイン）ならその部品の
// 画面の描画の部品（component.r）で解く（FR-185）。見比べのために分割したペインの client も、ほかの編集の client と同じく自分の画面で解く。
// 解く処理（画面の位置から部品への対応）は ui.Renderer.HitTest の 1 か所にある。

// mouseEvent は、いま処理しているマウスの出来事の位置である（handleKeys が noteMouse で入れる）。
// マクロの再生で記録したマウスのキーを実行するときは、最後に届いた出来事の位置を使う（Vim と同じ）。
type mouseEvent struct {
	ok       bool
	row, col int
	// c は、出来事を受けた client（部品の client なら、その元の client）である。
	c *clientView
	// comp は、出来事を受けた部品の client である。nil なら c の端末で受けた。
	comp *component
}

// noteMouse は、client c（部品の client なら comp）の端末で受けたマウスの出来事のキー k の位置を覚える。
// ボタンを押したことのキーなら、押した位置の部品の種類を、続くドラッグを寄せる部品として覚える（mouseDown）。
//
// req: FR-185
func (e *Editor) noteMouse(c *clientView, comp *component, k editcmd.Key, row, col int) {
	e.mouse = mouseEvent{ok: true, row: row, col: col, c: c, comp: comp}
	if !k.IsMousePress() {
		return
	}
	r, down := e.mouseScreen()
	*down = r.HitTest(row, col, ui.HitNone).Area
}

// mouseScreen は、いま処理しているマウスの出来事を受けた画面の描画の部品と、その画面でボタンを押した部品の種類の置き場を返す。
func (e *Editor) mouseScreen() (*ui.Renderer, *ui.HitArea) {
	if comp := e.mouse.comp; comp != nil {
		return comp.r, &comp.mouseDown
	}
	return e.viewOf(e.mouse.c).renderer, &e.mouse.c.mouseDown
}

// mouseEnabled は、端末にマウスの報告を求めるか（設定の mouse.enabled。FR-180）を返す。描くたびに読む。
//
// req: FR-180
func (e *Editor) mouseEnabled() bool {
	return e.options.Get(option.MouseEnabled).Bool()
}

// mouseLocator は、バッファ bs の State の Mouse（command.Mouse）である。いま処理しているマウスの出来事（Editor.mouse）の位置を、
// 出来事を受けた画面で解き、その位置にある部品が bs のテキストなら、その行と列を返す。
type mouseLocator struct {
	e  *Editor
	bs *bufferState
}

// hit は、いま処理しているマウスの出来事の位置にあるものを返す。drag なら、ボタンを押した部品の中へ寄せて解く。
func (m mouseLocator) hit(drag bool) (ui.Hit, bool) {
	e := m.e
	if !e.mouse.ok || e.mouse.c == nil || e.mouse.c.detached {
		return ui.Hit{}, false
	}
	r, down := e.mouseScreen()
	clamp := ui.HitNone
	if drag {
		if *down != ui.HitText && *down != ui.HitFloat {
			return ui.Hit{}, false
		}
		clamp = *down
	}
	return r.HitTest(e.mouse.row, e.mouse.col, clamp), true
}

// mouseFloat は、いま処理しているマウスの出来事を受けた画面のポップアップの窓の部品を返す。
// 部品の client の画面なら、その client が描く部品である（窓が画面の全体）。
func (e *Editor) mouseFloat() *floatState {
	if comp := e.mouse.comp; comp != nil {
		return comp.f
	}
	return e.float
}

// Target は、command.Mouse の Target である（FR-181、FR-185）。
//
//   - 編集の client の画面のバッファの表示領域は、そのタブのバッファ（tab）のテキストである。フォーカスのある
//     カーソルの近くのポップアップのバッファから見ると、ほかの部品（MouseElsewhere。Activate で元のタブへ戻る）である
//   - ポップアップの窓の中のテキストは、その窓のバッファのテキストである。部品の client の画面では、窓が画面の全体である
//   - タブの行のタブの見出しは、ほかの部品（MouseElsewhere。Activate でそのタブへ移る）である
//   - ほか（ステータス行、コマンドラインの行、枠線、ファインダー、閉じた窓の跡など）は MouseNone である
func (m mouseLocator) Target(drag bool) command.MouseTarget {
	none := command.MouseTarget{Kind: command.MouseNone}
	h, ok := m.hit(drag)
	if !ok {
		return none
	}
	e := m.e
	text := command.MouseTarget{Kind: command.MouseText, Line: h.Line, Col: h.Col, Over: h.Over}
	switch h.Area {
	case ui.HitText:
		if e.mouse.comp != nil {
			return none
		}
		if e.tab() == m.bs {
			return text
		}
		if f := e.float; f != nil && f.focused && !f.center && f.bs == m.bs && !drag {
			return command.MouseTarget{Kind: command.MouseElsewhere}
		}
	case ui.HitFloat:
		if f := e.mouseFloat(); f != nil && f.bs == m.bs {
			return text
		}
	case ui.HitTabLine:
		// 中央のポップアップを開いている間は、それだけを操作する（FR-68）。
		if f := e.float; f != nil && f.focused && f.center {
			return none
		}
		if h.Tab >= 0 && h.Tab < len(e.view.tabs) && e.mouse.comp == nil && !drag {
			return command.MouseTarget{Kind: command.MouseElsewhere}
		}
	case ui.HitNone:
	}
	return none
}

// Activate は、command.Mouse の Activate である（FR-181）。
// タブの見出しならそのタブへ移る（:b と同じく、移るとマクロの再生の残りは移った先のバッファで続く）。
// フォーカスのあるカーソルの近くのポップアップの外のバッファの表示領域なら、フォーカスを元のタブへ戻し、その位置へカーソルを移す
// （Neovim でほかの窓を押したときと同じ）。中央のポップアップは、開いている間はそれだけを操作する（FR-68）ので、Target が外を返さない。
//
// req: FR-181
func (m mouseLocator) Activate() error {
	h, ok := m.hit(false)
	if !ok {
		return nil
	}
	e := m.e
	switch h.Area {
	case ui.HitTabLine:
		if h.Tab < 0 || h.Tab >= len(e.view.tabs) {
			return nil
		}
		if to := e.view.tabs[h.Tab]; to != e.tab() {
			e.gotoBuffer(m.bs, to)
		}
	case ui.HitText:
		f := e.float
		if f == nil || !f.focused || f.center || f.bs != m.bs {
			return nil
		}
		e.unfocusFloat()
		if base := e.cur; base != m.bs {
			base.state.MoveToMouse(command.MouseTarget{Kind: command.MouseText, Line: h.Line, Col: h.Col})
		}
	case ui.HitFloat, ui.HitNone:
	}
	if e.cur != m.bs {
		return command.ErrBufferSwitched
	}
	return nil
}
