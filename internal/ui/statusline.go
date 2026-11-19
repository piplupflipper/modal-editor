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

// StatusLine は、ステータス行かタブ行に並べる部品の ID の列である（設定の statusline と tabline の left と right。FR-92）。
// Left は左の端から右へ、Right は右の端に寄せて左から右へ並べる。並べなかった部品は出さない。
// 本体の部品でない ID は、拡張機能が足した部品として Frame.ExtParts で引き、無ければ描かない。
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
	id   string // 部品の ID
	text string
	attr Attr
	// ext は、拡張機能が足した部品（Frame.ExtParts）かどうかである。幅が足りなければ本体の部品より先に切る（trimExtSegs）。
	ext bool
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
	default:
		// 本体の部品でなければ、拡張機能が足した部品として引く（FR-92）。
		if p, ok := f.ExtParts[id]; ok && p.Text != "" {
			return p.Text, []string{StyleStatusLineExt, p.Style}
		}
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
//
// 部品の色は base（statusline の色）の上に重ねる。ただし部品の色が文字か背景の色を持てば、base の反転を外す（部品の色の反転だけを使う）。
// 既定の statusline は反転なので、外さないと部品の文字の色と背景の色が入れ替わって出るためである（PR #206 のレビュー）。
func (r *Renderer) statusSegs(ids []string, theme Theme, base Attr, text Text, f *Frame, cur cursorPlace) []statusSeg {
	var segs []statusSeg
	for _, id := range ids {
		s, styles := r.statusPart(id, text, f, cur)
		if s == "" {
			continue
		}
		a := base
		for _, st := range styles {
			o := theme.attr(st)
			if o.Fg != ColorDefault || o.Bg != ColorDefault {
				a.Reverse = false
			}
			a = a.Over(o)
		}
		if !flagParts[id] || len(segs) == 0 {
			s = " " + s
		}
		segs = append(segs, statusSeg{id: id, text: s + " ", attr: a, ext: !IsStatusPart(id)})
	}
	return segs
}

// nameKeep は、幅が足りないときに、拡張機能の部品より優先して残す名前の末尾の列の数である（drawStatus。仮）。
const nameKeep = 20

// trimExtSegs は、部品の列 lists の幅の合計が、拡張機能の部品を除いた幅を保ったまま width を over 列超えるとき、
// 拡張機能の部品（statusSeg.ext）を幅の広いものから順に切り詰めるか落として、超えた分を減らす（PR #239 のレビューの推奨 1）。
// 拡張機能の部品の長さには上限が無いので、幅の狭い端末でも本体の部品（モード、位置など）を残すためである。
// 切り詰めた部品は、中身の最後の 1 列を ">" にする。中身が 1 列も残らなければ部品を落とす。
// 返す列は、落とした部品を除いたものである。
func (r *Renderer) trimExtSegs(over int, lists ...[]statusSeg) [][]statusSeg {
	tabStop := tabStopOr(r.TabStop)
	for over > 0 {
		// 残っている拡張機能の部品のうち、最も幅の広いもの（同じなら後ろのもの）を選ぶ。
		var s *statusSeg
		w := 0
		for li := range lists {
			for i := range lists[li] {
				c := &lists[li][i]
				if cw := StringWidth(c.text, r.TabStop); c.ext && c.text != "" && cw >= w {
					s, w = c, cw
				}
			}
		}
		if s == nil {
			break
		}
		// s.text は " " + 中身 + " "（印の部品と違い、拡張機能の部品は常に左の空白を持つ）。
		keep := w - over - 2 - 1 // 残す中身の列（">" の 1 列を除く）
		if keep < 1 {
			over -= w
			s.text = ""
			continue
		}
		body := s.text[1 : len(s.text)-1]
		col, off := 0, 0
		for off < len(body) {
			gl := nextGlyph(body, off, col, tabStop)
			if col+gl.width > keep {
				break
			}
			col += gl.width
			off += gl.size
		}
		over -= w - (col + 3)
		s.text = " " + body[:off] + "> "
	}
	out := make([][]statusSeg, len(lists))
	for i, segs := range lists {
		out[i] = slices.DeleteFunc(segs, func(s statusSeg) bool { return s.ext && s.text == "" })
	}
	return out
}

// fitName は、左の部品 segs が幅 width に収まらなければ、名前の部品（StatusPartName）の先頭を切って "<" を付け、
// 後ろの部品（編集の禁止の印、未保存の印など）が収まるようにする（Vim 9.1 のステータス行と同じ。FR-87）。
// 名前を "<" だけにしても収まらなければ、名前を空白 1 つにして印を残し、それでも収まらない分は drawSegs が右の端で切る。
func (r *Renderer) fitName(segs []statusSeg, width int) {
	total := 0
	for _, s := range segs {
		total += StringWidth(s.text, r.TabStop)
	}
	over := total - width
	if over <= 0 {
		return
	}
	for i, s := range segs {
		if s.id != StatusPartName {
			continue
		}
		// s.text は " " + 名前 + " "。"<" の 1 列を足すので、over+1 列以上を名前の先頭から落とす。
		name := s.text[1 : len(s.text)-1]
		tabStop := tabStopOr(r.TabStop)
		dropped, off := 0, 0
		for off < len(name) && dropped < over+1 {
			gl := nextGlyph(name, off, dropped, tabStop)
			dropped += gl.width
			off += gl.size
		}
		if dropped < over+1 && i+1 < len(segs) {
			// 名前を "<" だけにしても、後ろの印が収まらない。名前を空白 1 つにして、印を 1 列でも多く残す（PR #206 の再レビュー）。
			segs[i].text = " "
			return
		}
		segs[i].text = " <" + name[off:] + " "
		return
	}
}

// drawStatus は、row 行目（バッファの表示領域のすぐ下）にステータス行を描く。
//
// 部品の並びは Renderer.StatusLine（nil なら DefaultStatusLine）で決める（FR-92）。
// 左の部品を左の端から、右の部品を右の端に寄せて描く。幅が足りなければ、まず拡張機能の部品を右の後ろから切り詰めるか落とす（trimExtSegs）。
// それでも足りなければ右の部品を優先し、左の部品は名前の先頭を "<" で切って
// 後ろの印を残し（fitName）、それでも収まらなければ右の端で切る。
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
	left := r.statusSegs(layout.Left, theme, a, text, f, cur)
	// 名前は fitName が先頭を "<" で切って詰めるので、名前の末尾の nameKeep 列（ファイル名の部分）を残しても足りない分だけ、
	// 拡張機能の部品を先に切る。長いパスで拡張機能の部品がすべて消えないように、名前の全体は守らない（仮）。
	over := -r.Width
	for _, s := range slices.Concat(left, right) {
		w := StringWidth(s.text, r.TabStop)
		if s.id == StatusPartName {
			w = min(w, nameKeep+2)
		}
		over += w
	}
	if over > 0 {
		trimmed := r.trimExtSegs(over, left, right)
		left, right = trimmed[0], trimmed[1]
	}
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
	r.fitName(left, start)
	r.drawSegs(g, row, 0, start, left)
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
