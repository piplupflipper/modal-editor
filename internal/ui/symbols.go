package ui

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// 記号の名前（設定の symbols に書ける名前。基本設計 7.1。FR-93。名前は仮）。
// 印の列の印の名前は、その印の色の名前（StyleSign...）と同じにした。
const (
	SymbolSignExecutionPoint       = StyleSignExecutionPoint
	SymbolSignBreakpoint           = StyleSignBreakpoint
	SymbolSignBreakpointUnverified = StyleSignBreakpointUnverified
	SymbolSignDiagnosticError      = StyleSignDiagnosticError
	SymbolSignDiagnosticWarning    = StyleSignDiagnosticWarning
	SymbolSignDiagnosticInfo       = StyleSignDiagnosticInfo
	SymbolSignDiagnosticHint       = StyleSignDiagnosticHint
	// SymbolReadOnly は、ステータス行の編集の禁止の印（部品 StatusPartReadOnly。基本設計 4.6。FR-87）である。
	SymbolReadOnly = "readonly"
	// SymbolModified は、ステータス行の未保存の印（部品 StatusPartModified）である。
	SymbolModified = "modified"
	// SymbolPopupBorder は、浮かべる窓の枠線の 8 文字である。並びは左上、上、右上、右、右下、下、左下、左（Neovim の border の配列と同じ）。
	// 空なら枠線を描かない（既定）。
	SymbolPopupBorder = "popup.border"
)

// borderChars は、SymbolPopupBorder の文字の数である。
const borderChars = 8

// Symbols は、記号の名前から、描く文字列への対応である（FR-93）。
// Renderer は、表に無い名前に DefaultSymbols の値を使う。
type Symbols map[string]string

// defaultSymbols は、既定の記号である。段階 2 までと同じく ASCII にする（基本設計 6.7、7.1。端末の文字幅の設定で列がずれないようにするため）。
var defaultSymbols = Symbols{
	SymbolSignExecutionPoint:       "=>",
	SymbolSignBreakpoint:           "B",
	SymbolSignBreakpointUnverified: "b",
	SymbolSignDiagnosticError:      "E",
	SymbolSignDiagnosticWarning:    "W",
	SymbolSignDiagnosticInfo:       "I",
	SymbolSignDiagnosticHint:       "H",
	SymbolReadOnly:                 "[RO]",
	SymbolModified:                 "[+]",
	SymbolPopupBorder:              "",
}

// DefaultSymbols は、既定の記号の対応を返す。返す対応は呼び出しごとに作り直す。
func DefaultSymbols() Symbols {
	return maps.Clone(defaultSymbols)
}

// SymbolNames は、記号の名前を辞書順に返す。
func SymbolNames() []string {
	return slices.Sorted(maps.Keys(defaultSymbols))
}

// CheckSymbol は、記号の名前 name に value を使えるかを確かめ、使えなければ理由を返す（設定の symbols の検査。FR-93）。
//
//   - 印の列の印（"sign." で始まる名前）: 表示幅が 1 か 2 の文字列。印の列は 2 列なので、1 列の印は右に空白を置いて描く
//   - 枠線（SymbolPopupBorder）: 空か、表示幅が 1 の文字をちょうど 8 つ並べた文字列
//   - それ以外（SymbolReadOnly、SymbolModified）: 任意の文字列。空ならその部品を出さない
//
// どれも、タブと制御文字を含めない（描くと ^I の形になり、幅が変わるため）。
//
// req: FR-93
func CheckSymbol(name, value string) error {
	if _, ok := defaultSymbols[name]; !ok {
		return fmt.Errorf("未知の記号の名前 %s", name)
	}
	n, w := 0, 0
	for off := 0; off < len(value); {
		gl := nextGlyph(value, off, w, DefaultTabStop)
		if gl.kind != glyphText {
			return errors.New("タブと制御文字は使えない")
		}
		if name == SymbolPopupBorder && gl.width != 1 {
			return fmt.Errorf("枠線の文字 %q の表示幅が 1 でない", gl.text)
		}
		n++
		w += gl.width
		off += gl.size
	}
	switch {
	case name == SymbolPopupBorder:
		if n != 0 && n != borderChars {
			return fmt.Errorf("枠線は空か %d 文字（左上、上、右上、右、右下、下、左下、左）にする（%d 文字ある）", borderChars, n)
		}
	case isSignSymbol(name):
		if w < 1 || w > signColumnWidth {
			return fmt.Errorf("印の表示幅は 1 か 2 にする（%d ある）", w)
		}
	}
	return nil
}

// isSignSymbol は、name が印の列の印の記号かを返す。
func isSignSymbol(name string) bool {
	return strings.HasPrefix(name, "sign.")
}

// symbol は、記号の名前 name の文字列を返す。r.Symbols に無ければ既定の値を使う。
func (r *Renderer) symbol(name string) string {
	if s, ok := r.Symbols[name]; ok {
		return s
	}
	return defaultSymbols[name]
}

// border は、浮かべる窓の枠線の 8 文字を返す。枠線を描かないなら ok が false である。
// CheckSymbol を通っていない値（8 文字でない、幅が 1 でない文字を含む）なら、枠線を描かない。
func (r *Renderer) border() (chars [borderChars]string, ok bool) {
	s := r.symbol(SymbolPopupBorder)
	if s == "" || CheckSymbol(SymbolPopupBorder, s) != nil {
		return chars, false
	}
	i := 0
	for off := 0; off < len(s); i++ {
		gl := nextGlyph(s, off, 0, DefaultTabStop)
		chars[i] = gl.text
		off += gl.size
	}
	return chars, true
}
