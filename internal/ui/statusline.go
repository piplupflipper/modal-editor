package ui

import (
	"fmt"
	"slices"
)

// ステータス行の部品の ID（設定の statusline に書ける ID。基本設計 7.1。FR-92。ID は仮）。
const (
	// StatusPartMode は、モードの名前（NORMAL など）である。
	StatusPartMode = "mode"
	// StatusPartName は、バッファの名前（ファイルのパス。無ければ [No Name]）である。
	StatusPartName = "name"
	// StatusPartModified は、未保存の変更があるときの印（記号 SymbolModified。既定は [+]）である。
	StatusPartModified = "modified"
	// StatusPartReadOnly は、編集を禁止しているときの印（記号 SymbolReadOnly。既定は [RO]。基本設計 4.6。FR-87）である。
	StatusPartReadOnly = "readonly"
	// StatusPartFileType は、ファイルの種類の名前（go など。基本設計 6.7）である。既定の並びには無い。
	StatusPartFileType = "filetype"
	// StatusPartRecording は、マクロの記録中の表示（recording @q）である。
	StatusPartRecording = "recording"
	// StatusPartRuler は、カーソルの位置（Vim のルーラーと同じ「行,バイトの列-表示の列」）である。
	StatusPartRuler = "ruler"
	// StatusPartPosition は、表示している範囲の位置（All、Top、Bot、NN%）である。
	StatusPartPosition = "position"
)

// statusParts は、本体が持つ部品の ID の全部である。
var statusParts = []string{
	StatusPartMode, StatusPartName, StatusPartModified, StatusPartReadOnly, StatusPartFileType,
	StatusPartRecording, StatusPartRuler, StatusPartPosition,
}

// flagParts は、前の部品に空白 1 つで続けて出す印の部品である（" name [+]" のように、Vim のステータス行の %m と %r と同じ見た目にする）。
var flagParts = map[string]bool{StatusPartModified: true, StatusPartReadOnly: true}

// StatusPartIDs は、本体が持つステータス行の部品の ID を、定義の順に返す。返す列は呼び出しごとに作り直す。
func StatusPartIDs() []string {
	return slices.Clone(statusParts)
}

// IsStatusPart は、id が本体の持つステータス行の部品の ID かを返す。
func IsStatusPart(id string) bool {
	return slices.Contains(statusParts, id)
}

// StatusLine は、ステータス行に並べる部品の ID の列である（設定の statusline。FR-92）。
// Left は左の端から右へ、Right は右の端に寄せて左から右へ並べる。並べなかった部品は出さない。
// 知らない ID は描かない。
type StatusLine struct {
	Left, Right []string
}

// DefaultStatusLine は、既定の並びを返す。段階 2 までのステータス行の並び（左にモード、名前、未保存の印、右に記録中の表示、
// カーソルの位置、範囲の位置）に、名前の後の編集の禁止の印（FR-87）を足したものである。
func DefaultStatusLine() StatusLine {
	return StatusLine{
		Left:  []string{StatusPartMode, StatusPartName, StatusPartModified, StatusPartReadOnly},
		Right: []string{StatusPartRecording, StatusPartRuler, StatusPartPosition},
	}
}

// statusSeg は、ステータス行に描く 1 つの部品の文字列と属性である。
type statusSeg struct {
	text string
	attr Attr
}

// statusPart は、部品 id の中身の文字列と、部品に重ねる色の名前を返す。中身が無ければ（未保存でない、記録していないなど）空を返す。
func (r *Renderer) statusPart(id string, text Text, f *Frame, cur cursorPlace) (s string, styles []string) {
	switch id {
	case StatusPartMode:
		return f.Mode.String(), []string{StyleStatusLineMode, modeStyle(f.Mode)}
	case StatusPartName:
		if f.Name == "" {
			return "[No Name]", []string{StyleStatusLineName}
		}
		return f.Name, []string{StyleStatusLineName}
	case StatusPartModified:
		if f.Modified {
			return r.symbol(SymbolModified), []string{StyleStatusLineModified}
		}
	case StatusPartReadOnly:
		if f.ReadOnly {
			return r.symbol(SymbolReadOnly), []string{StyleStatusLineReadOnly}
		}
	case StatusPartFileType:
		return f.FileType, []string{StyleStatusLineFileType}
	case StatusPartRecording:
		if f.Recording != 0 {
			return "recording @" + string(f.Recording), []string{StyleStatusLineRecording}
		}
	case StatusPartRuler:
		var ruler string
		switch {
		case len(text.Line(cur.line)) == 0:
			ruler = fmt.Sprintf("%d,0-1", cur.line+1)
		case cur.byte == cur.col:
			ruler = fmt.Sprintf("%d,%d", cur.line+1, cur.byte+1)
		default:
			ruler = fmt.Sprintf("%d,%d-%d", cur.line+1, cur.byte+1, cur.col+1)
		}
		return ruler, []string{StyleStatusLineRuler}
	case StatusPartPosition:
		return r.position(text.LineCount()), []string{StyleStatusLinePosition}
	}
	return "", nil
}

// modeStyle は、モード m の、モードの部品の色の名前を返す。
func modeStyle(m Mode) string {
	switch m {
	case ModeInsert:
		return StyleStatusLineModeInsert
	case ModeVisual, ModeVisualLine:
		return StyleStatusLineModeVisual
	case ModeCommandLine:
		return StyleStatusLineModeCommandLine
	case ModeNormal:
	}
	return StyleStatusLineModeNormal
}

// statusSegs は、ids の部品を、描く順の文字列と属性の列にする。中身の無い部品は飛ばす。
// 部品は左右に空白を 1 つずつ置き、部品の色で描く（lualine の部品と同じく、色を付けた部品が帯に見えるようにするため）。
// 印の部品（flagParts）は、前に部品があれば左の空白を置かずに続ける。
func (r *Renderer) statusSegs(ids []string, theme Theme, base Attr, text Text, f *Frame, cur cursorPlace) []statusSeg {
	var segs []statusSeg
	for _, id := range ids {
		s, styles := r.statusPart(id, text, f, cur)
		if s == "" {
			continue
		}
		a := base
		for _, st := range styles {
			a = a.Over(theme.attr(st))
		}
		if !flagParts[id] || len(segs) == 0 {
			s = " " + s
		}
		segs = append(segs, statusSeg{text: s + " ", attr: a})
	}
	return segs
}

// drawStatus は、row 行目（バッファの表示領域のすぐ下）にステータス行を描く。
//
// 部品の並びは Renderer.StatusLine（nil なら DefaultStatusLine）で決める（FR-92）。
// 左の部品を左の端から、右の部品を右の端に寄せて描き、幅が足りなければ、右の部品を優先して左の部品を右の端で切る。
// 部品の中身は statusPart の doc にある。カーソルの位置は Vim のルーラーと同じく「行,バイトの列-表示の列」で、
// 2 つの列が同じなら表示の列を省き、空の行では "0-1" と出す。範囲の位置は Vim と同じく All、Top、Bot、または上に隠れた行の割合である。
// 編集を禁止したバッファでは、編集の禁止の印（既定は [RO]）を出す（FR-87）。
//
// req: FR-87, FR-92
func (r *Renderer) drawStatus(g *Grid, theme Theme, text Text, f *Frame, cur cursorPlace, row int) {
	a := theme.attr(StyleStatusLine)
	for c := range r.Width {
		g.set(row, c, blank(a))
	}
	layout := DefaultStatusLine()
	if r.StatusLine != nil {
		layout = *r.StatusLine
	}
	right := r.statusSegs(layout.Right, theme, a, text, f, cur)
	rw := 0
	for _, s := range right {
		rw += StringWidth(s.text, r.TabStop)
	}
	if rw > r.Width && len(right) > 0 {
		// 右の部品だけで幅を超えるなら、左の端の余白を詰めて、右の部品の中身を 1 列でも多く出す（段階 2 までと同じ）。
		right[0].text = right[0].text[1:]
		rw--
	}
	start := max(r.Width-rw, 0)
	r.drawSegs(g, row, start, r.Width, right)
	r.drawSegs(g, row, 0, start, r.statusSegs(layout.Left, theme, a, text, f, cur))
}

// drawSegs は、segs を row 行目の col 列から、列 limit の手前まで順に描く。収まらない文字の手前で止め、後の部品は描かない。
func (r *Renderer) drawSegs(g *Grid, row, col, limit int, segs []statusSeg) {
	tabStop := tabStopOr(r.TabStop)
	for _, s := range segs {
		want := col
		for off := 0; off < len(s.text); {
			gl := nextGlyph(s.text, off, want, tabStop)
			want += gl.width
			off += gl.size
		}
		if r.drawText(g, row, col, limit, s.text, s.attr) != want {
			return
		}
		col = want
	}
}
