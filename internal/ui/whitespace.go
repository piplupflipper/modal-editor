package ui

import "strings"

// 空白の記号（FR-170。基本設計の「空白の記号」）。
//
// Frame.Whitespace が true なら、タブ文字、空白、ノーブレークスペース、行末を、記号（設定の symbols の "whitespace." で始まる名前）で描く
// （Vim の 'list' と 'listchars'）。見せ方は Neovim 0.12.5 と Vim 9.1 に合わせた。2026-11-21 に tmux の上で両方を動かし、
// capture-pane -e で比べた（どちらも同じ形だった）。
//
//   - タブ文字は、タブ幅のセルを記号（whitespace.tab）で埋める（glyph.cell）。記号が空なら ^I の 2 列で描く（specialkey の色）
//   - 空白（U+0020）は、行頭の空白（行の先頭から続く空白とタブ文字の中の空白）に whitespace.lead、行末の空白（最後の空白でもタブ文字でもない
//     文字より後ろの空白）に whitespace.trail、ほかに whitespace.space を描く。lead と trail が空なら space を使い、space も空なら描かない。
//     空白とタブ文字だけの行には、lead を使わず trail を使う（Vim と同じ）
//   - ノーブレークスペース（U+00A0 と U+202F）は whitespace.nbsp で描く。行頭と行末の判定では空白として数えない（Vim と同じ）
//   - 行末の 1 セルに whitespace.eol を描く（nontext の色）。行を折り返すときは、行末の記号の 1 セルも見た目の行の数に数える
//     （ちょうど本文の幅で終わる行は、行末の記号だけの見た目の行を 1 行足す）
//   - ノーマルモードとビジュアルモードでも、タブ文字の上のカーソルはタブの最初の列に置き、望みの列とルーラーの列もその列にする
//
// 記号の色は ^A などの表記と同じく下地の色で、構文、診断、検索の一致、範囲の色、選択の色をその上に重ねる（layers.go）。
// 空白の記号のための重ね方は作らない。
// 文字を画面の列に並べる処理（本文、カーソルの列、折り返し、望みの列）は、どれも lineRule を通して数える。

// lineRule は、バッファの行の文字を画面の列に並べる規則である。タブ文字の幅（FR-161）と、空白の記号を描くときの違い（FR-170）を持つ。
// 描画（Renderer.rule）、望みの列（Columns）、折り返し（wrapper）が、同じ規則で数える。newLineRule で作る。
// ゼロ値は、タブ文字の幅を DefaultTabStop にし、空白の記号を描かない規則である（まだ描いていない Renderer の rule）。
type lineRule struct {
	// tabStop は、タブ文字の幅である。0 以下なら DefaultTabStop を使う。
	tabStop int
	// list は、空白の記号を描くか（Frame.Whitespace）である。
	list bool
	// tab は、list のときのタブ文字の記号（SymbolWhitespaceTab）である。空なら、タブ文字を ^I で描く。
	tab string
	// space、lead、trail、nbsp は、list のときの空白の記号である。lead と trail は、空なら space にしてある。
	space, lead, trail, nbsp string
	// eol は、list のときの行末の記号（SymbolWhitespaceEOL）である。空なら描かない。
	eol string
}

// newLineRule は、タブ文字の幅 tabStop（0 以下なら DefaultTabStop）と、空白の記号を描くか list で、規則を作る。
// symbol は記号の名前から文字列を引く関数で、list のときだけ呼ぶ。
func newLineRule(tabStop int, list bool, symbol func(string) string) lineRule {
	m := lineRule{tabStop: tabStop, list: list}
	if !list {
		return m
	}
	m.tab, m.eol, m.nbsp = symbol(SymbolWhitespaceTab), symbol(SymbolWhitespaceEOL), symbol(SymbolWhitespaceNbsp)
	m.space, m.lead, m.trail = symbol(SymbolWhitespaceSpace), symbol(SymbolWhitespaceLead), symbol(SymbolWhitespaceTrail)
	if m.lead == "" {
		m.lead = m.space
	}
	if m.trail == "" {
		m.trail = m.space
	}
	return m
}

// glyph は、nextGlyph と同じく、行 s の off から始まる 1 文字を列 col に置いた配置を返す。
// 空白の記号を描くとき（m.list）は、タブ文字を、記号で描く glyphWhitespace（幅はタブ幅のまま）にするか、記号が空なら ^I の glyphSpecial（2 列）にする。
// どちらも glyphTab ではないので、カーソルと望みの列はタブの最初の列になる（Vim 9.1 の getvcol が 'list' のときに行う扱い）。
func (m lineRule) glyph(s string, off, col int) glyph {
	g := nextGlyph(s, off, col, tabStopOr(m.tabStop))
	if g.kind == glyphTab && m.list {
		if m.tab == "" {
			g.kind, g.text, g.width = glyphSpecial, "^I", 2
		} else {
			g.kind, g.text = glyphWhitespace, m.tab
		}
	}
	return g
}

// eolCell は、行を折り返すときに、行末の 1 セルを見た目の行の数に数えるかである（Vim の plines_win_nofold の 'list' の扱い）。
func (m lineRule) eolCell() bool {
	return m.eol != ""
}

// lineSpaces は、1 行の中の、行頭の空白と行末の空白の範囲である（drawer.look）。
// バイトの位置が leadEnd より前の空白は行頭の空白、trailStart 以降の空白は行末の空白である。
type lineSpaces struct {
	leadEnd, trailStart int
}

// spacesOf は、行 s の行頭の空白と行末の空白の範囲を返す。空白とタブ文字だけの行は、全体を行末の空白にする（leadEnd を 0 にする。Vim と同じ）。
func spacesOf(s string) lineSpaces {
	body := strings.TrimLeft(s, " \t")
	if body == "" {
		return lineSpaces{leadEnd: 0, trailStart: 0}
	}
	return lineSpaces{leadEnd: len(s) - len(body), trailStart: len(strings.TrimRight(s, " \t"))}
}

// isNbsp は、文字の text（glyph.text）がノーブレークスペース（U+00A0 か U+202F。後に結合文字が続いてもよい）かを返す。
func isNbsp(text string) bool {
	return strings.HasPrefix(text, " ") || strings.HasPrefix(text, " ")
}

// look は、drawLine と drawWrapped が描く文字 gl の、描く形と下地の色を返す。
// ^A などの表記の下地は specialkey、空白の記号（glyphWhitespace）の下地は whitespace の色である。
// 空白の記号を描くときは、空白とノーブレークスペースを、行の中の位置（d.spaces）で決まる記号の glyphWhitespace にする。記号が空なら替えない。
//
// req: FR-170
func (d *drawer) look(gl glyph) (glyph, Attr) {
	m := &d.r.rule
	if m.list && gl.kind == glyphText {
		sym := ""
		switch {
		case gl.text == " " && gl.off < d.spaces.leadEnd:
			sym = m.lead
		case gl.text == " " && gl.off >= d.spaces.trailStart:
			sym = m.trail
		case gl.text == " ":
			sym = m.space
		case isNbsp(gl.text):
			sym = m.nbsp
		}
		if sym != "" {
			gl.kind, gl.text = glyphWhitespace, sym
		}
	}
	switch gl.kind {
	case glyphSpecial:
		return gl, d.theme.attr(StyleSpecialKey)
	case glyphWhitespace:
		return gl, d.theme.attr(StyleWhitespace)
	case glyphText, glyphTab:
	}
	return gl, Attr{}
}

// eolCell は、表示領域の idx 行目（バッファの line 行目、長さ n）の行末のセル（行の区切りを表すセル）に描くセルを返す。描かなければ ok は false である。
// 行末に掛かる範囲（選択、検索の一致）があれば、その色の空白を描く。空白の記号を描くときは、行末の記号（whitespace.eol）を nontext の色を下地にして描く。
//
// req: FR-170
func (d *drawer) eolCell(idx, line, n int) (Cell, bool) {
	text, base := " ", Attr{}
	if eol := d.r.rule.eol; eol != "" {
		text, base = eol, d.theme.attr(StyleNonText)
	}
	a := d.attrAt(idx, line, n, 0, base)
	if text == " " && a == (Attr{}) {
		return Cell{}, false
	}
	return Cell{Text: text, Width: 1, Attr: a}, true
}
