package ui

// このファイルは、マウスの出来事の画面の位置を、直前の Render が描いた部品の位置に解く（FR-181、FR-185）。
// 位置を「どの部品のどこか」に解く処理はここ 1 か所に置く。後から部品（右の端の位置の印の列、窓の上端の帯など）を足すときは、
// HitArea に種類を足し、その部品を描く関数が場所を hitState に残して、HitTest がそれを引く。

// HitArea は、画面の位置にある部品の種類である（Renderer.HitTest）。
type HitArea int

const (
	// HitNone は、マウスで操作する部品が無い位置である（出力の欄、浮かべた一覧の窓、ポップアップの枠線と見出し、プレビュー、
	// ファインダーの窓、画面の外など）。
	HitNone HitArea = iota
	// HitText は、バッファの表示領域である。印の列、行番号の列、最後の行より下の "~" の行、見比べの詰め物の行を含む。
	HitText
	// HitFloat は、ポップアップの窓（Frame.Float）の中のテキストである。部品の client の画面では、窓が表示領域の全体である（Float.Fill）。
	HitFloat
	// HitTabLine は、タブの行である。
	HitTabLine
	// HitStatusLine は、ステータス行である（ホイールでその上の表示領域を送る。FR-182）。
	HitStatusLine
	// HitCommandLine は、コマンドラインの行（メッセージの行）である（ホイールで今の表示領域を送る。FR-182）。
	HitCommandLine
)

// Hit は、画面の位置にあるもの（Renderer.HitTest の結果）である。
type Hit struct {
	Area HitArea
	// Line は、Area が HitText か HitFloat のときの、バッファの行（0 から数える）である。
	// 最後の行より下の "~" の行は最後の行に、見比べの詰め物の行はその下の行（無ければ上の行）にする。
	Line int
	// Col は、Area が HitText か HitFloat のときの、行の表示の列（0 から数える）である。
	// 行を折り返さないときは行の先頭からの列（横のスクロールの位置を足したもの）、折り返すときは WrapLines の列
	// （見た目の行の番号 × 本文の幅 + 見た目の行の中の列）で、どちらも望みの列（internal/command の State の want）と同じ数え方である。
	// 印の列と行番号の列は、本文の左の端の列として数える。
	Col int
	// Below は、最後の行より下の "~" の行かである。
	Below bool
	// Tab は、Area が HitTabLine のときの、Frame.Tabs の添字である。タブの見出しの上でなければ -1 である。
	Tab int
	// Over は、HitTest を部品の中へ寄せて解いたときに、位置が部品の上（負）か下（正）へはみ出していた行の数である。
	Over int
}

// rect は、画面の矩形（行と列は 0 から数える。タブの行を除いた座標）である。
type rect struct{ top, left, h, w int }

func (b rect) contains(row, col int) bool {
	return row >= b.top && row < b.top+b.h && col >= b.left && col < b.left+b.w
}

// tabSpan は、タブの行に描いたタブの見出し 1 つの列の範囲 [from, to) と、Frame.Tabs の添字である。
type tabSpan struct{ from, to, index int }

// hitState は、直前の Render が描いた部品の場所である（HitTest が引く）。Render の始めに空にする。
type hitState struct {
	// lines は、描いたバッファの行の数である。
	lines int
	// float は、ポップアップの窓の中のテキストの場所で、子の Renderer（Renderer.float）の表示領域に当たる。floatOK が false なら無い。
	float   rect
	floatOK bool
	// covers は、表示領域の上に重ねて描いた窓の場所である（枠線、見出し、プレビュー、浮かべた一覧、ファインダー）。
	covers []rect
	// tabs は、タブの行に描いたタブの見出しの場所である。
	tabs []tabSpan
}

// reset は、h を空にする。
func (h *hitState) reset() {
	h.lines, h.floatOK = 0, false
	h.covers, h.tabs = h.covers[:0], h.tabs[:0]
}

// cover は、row 行目の x 列目から幅 w 列、高さ n 行の窓を、表示領域の上に重ねて描いたことを残す。
func (h *hitState) cover(row, x, n, w int) {
	if n > 0 && w > 0 {
		h.covers = append(h.covers, rect{top: row, left: x, h: n, w: w})
	}
}

// HitTest は、直前の Render が描いた画面の row 行目の col 列目（0 から数える）にあるものを返す（FR-181、FR-185）。
//
// clamp が HitText か HitFloat なら、位置をその部品の中へ寄せて解く（ドラッグの間の位置。Over にはみ出した行の数を入れる）。
// 寄せる前に、位置を画面の中へ寄せる。端末が画面の外の位置を報告しても、Neovim 0.12.5 と同じく画面の端の行として数える
// （PR #346 のレビューの推奨 2）。その部品を描いていなければ HitNone を返す。clamp が HitNone なら寄せずに、位置にある部品を返す。
// まだ描いていなければ HitNone を返す。
//
// req: FR-181, FR-182, FR-185
func (r *Renderer) HitTest(row, col int, clamp HitArea) Hit {
	none := Hit{Area: HitNone, Tab: -1}
	if !r.rendered || r.Width <= 0 || r.Height <= 0 {
		return none
	}
	if clamp == HitText || clamp == HitFloat {
		row, col = min(max(row, 0), r.Height-1), min(max(col, 0), r.Width-1)
	}
	if r.tabRows > 0 && row >= 0 && row < r.tabRows && clamp == HitNone {
		return r.hitTab(col)
	}
	row -= r.tabRows
	switch clamp {
	case HitText:
		return r.hitText(row, col, true)
	case HitFloat:
		if h, ok := r.hitFloat(row, col, true); ok {
			return h
		}
		return none
	case HitNone, HitTabLine, HitStatusLine, HitCommandLine:
	}
	if h, ok := r.hitFloat(row, col, false); ok {
		return h
	}
	for _, b := range r.hits.covers {
		if b.contains(row, col) {
			return none
		}
	}
	if col >= 0 && col < r.Width {
		// ステータス行は表示領域のすぐ下、コマンドラインの行は画面の最後の行である（render）。出力の欄の行はどちらでもない。
		body := r.Height - r.tabRows
		switch {
		case row == body-1:
			return Hit{Area: HitCommandLine, Tab: -1}
		case row == r.textRows() && body >= 2:
			return Hit{Area: HitStatusLine, Tab: -1}
		}
	}
	return r.hitText(row, col, false)
}

// hitTab は、タブの行の col 列目にあるタブの見出しを返す。
func (r *Renderer) hitTab(col int) Hit {
	for _, s := range r.hits.tabs {
		if col >= s.from && col < s.to {
			return Hit{Area: HitTabLine, Tab: s.index}
		}
	}
	return Hit{Area: HitTabLine, Tab: -1}
}

// hitFloat は、row 行目の col 列目（タブの行を除いた座標）がポップアップの窓の中のテキストの上なら、その位置を ok を true にして返す。
// clamp なら、位置を窓の中のテキストへ寄せる。
func (r *Renderer) hitFloat(row, col int, clamp bool) (Hit, bool) {
	b := r.hits.float
	if !r.hits.floatOK || r.float == nil {
		return Hit{}, false
	}
	over := 0
	if clamp {
		switch {
		case row < b.top:
			over, row = row-b.top, b.top
		case row >= b.top+b.h:
			over, row = row-(b.top+b.h-1), b.top+b.h-1
		}
		col = min(max(col, b.left), b.left+b.w-1)
	}
	if !b.contains(row, col) {
		return Hit{}, false
	}
	h := r.float.hitText(row-b.top, col-b.left, false)
	if h.Area != HitText {
		return Hit{Area: HitNone, Tab: -1}, true
	}
	h.Area, h.Over = HitFloat, over
	return h, true
}

// hitText は、表示領域の row 行目の col 列目にあるバッファの位置を返す。clamp なら、位置を表示領域の中へ寄せる。
func (r *Renderer) hitText(row, col int, clamp bool) Hit {
	none := Hit{Area: HitNone, Tab: -1}
	rows := min(r.textRows(), len(r.view))
	if rows <= 0 || r.hits.lines <= 0 {
		return none
	}
	over := 0
	if clamp {
		switch {
		case row < 0:
			over, row = row, 0
		case row >= rows:
			over, row = row-(rows-1), rows-1
		}
		col = min(max(col, 0), r.Width-1)
	}
	if row < 0 || row >= rows || col < 0 || col >= r.Width {
		return none
	}
	vr := r.view[row]
	if vr.line < 0 {
		// 見比べの詰め物の行は、その下の行（無ければ上の行）として解く。
		vr = viewRow{line: -1}
		for i := row + 1; i < rows && vr.line < 0; i++ {
			vr = viewRow{line: r.view[i].line}
		}
		for i := row - 1; i >= 0 && vr.line < 0; i-- {
			vr = viewRow{line: r.view[i].line}
		}
		if vr.line < 0 {
			return none
		}
	}
	x := max(col-r.textLeft(), 0)
	h := Hit{Area: HitText, Line: vr.line, Tab: -1, Over: over}
	if vr.line >= r.hits.lines {
		h.Line, h.Below = r.hits.lines-1, true
		vr.k = 0
	}
	if r.wrap {
		h.Col = vr.k*r.wrapper().width + x
	} else {
		h.Col = r.left + x
	}
	return h
}
