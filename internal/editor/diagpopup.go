package editor

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/piplupflipper/vi-imitation/ext"
	"github.com/piplupflipper/vi-imitation/internal/option"
	"github.com/piplupflipper/vi-imitation/internal/ui"
)

// カーソルの行の診断のポップアップ（基本設計 6.7 の「診断のポップアップ」。FR-171）。
//
// 設定 diagnostic.popup が true なら、カーソルが止まった契機（cursorhold.go）で、カーソルの行の診断をすべて、
// ホバーと同じカーソルの近くの浮かべた窓（テキストのバッファの部品。popup.go の openLinesFloat）に出す。
// 中身と出し方は、オーナーの nvim の設定（vim.diagnostic.config の severity_sort = true と float = { source = true, format = … }、
// CursorHold の自動コマンドの vim.diagnostic.open_float(nil, { focusable = false, scope = "line" })）での
// Neovim 0.12.5 の open_float に合わせた（2026-11-21 に tmux の中の nvim --clean で確かめた）。
//
//   - 見出しは診断（Neovim の header の "Diagnostics:"）。窓の見出しとして描く（枠線があれば上の枠線に重ねる）
//   - 診断は重要度の重い順に並べ、同じ重要度なら範囲の始まり、終わりの順にする（severity_sort。同じ位置なら届いた順）
//   - 1 つの診断は「番号. 出どころ: 本文 (コード)」で、出どころとコードが無ければ省く。番号は 1 から数え、診断が 1 つでも付ける
//     （scope = "line" の既定の prefix）。本文が改行を含めば行に分け、続きの行は番号の幅だけ空白を置く（タブ文字は残す）
//   - 番号の後ろの本文、出どころ、コードは、重要度ごとの印の列の色（sign.diagnostic.<重要度>）で描く
//     （Neovim の DiagnosticFloating<重要度> は、印の DiagnosticSign<重要度> と同じく Diagnostic<重要度> の色である）
//   - 長い行は窓の幅で空白の位置で折り返す（ホバーと同じ。Neovim の float の wrap と linebreak）
//
// Neovim との違い:
//
//   - Neovim の既定の suffix は、コードを持つ診断の行末に " [コード]" も付ける。オーナーの format がコードを本文に付けるので、
//     Neovim では "本文 (コード) [コード]" と 2 回出る。#329 の決め（本文の形は「本文 (コード)」）に合わせて 1 回にした
//   - 窓の幅の上限と枠線は、ホバーと同じ部品の規則による（80 列まで。Neovim は画面の幅まで広げる）。折り返した続きの行は字下げしない
//     （Neovim は breakindent で字下げする）
//   - Neovim の窓は、診断が変わっても、インサートモードに入っても（文字を打つまで）閉じない。ここではホバーと同じく、次のキー、
//     カーソルの移動、タブの移動で閉じる（expireFloat、dismissFloat）。ペインの部品の client のキーでも閉じる（paneInput。
//     Neovim も別の窓へ移ると閉じる）。診断が変わっただけでは閉じない（Neovim と同じ）

// diagnosticPopupTitle は、診断のポップアップの見出しである（Neovim の open_float の header の "Diagnostics:"）。
const diagnosticPopupTitle = "診断"

// wantsDiagnosticPopup は、カーソルが止まった契機で診断のポップアップを出すか（設定 diagnostic.popup の今の値）を返す。
//
// req: FR-171
func (e *Editor) wantsDiagnosticPopup() bool {
	return e.options.Get(option.DiagnosticPopup).Bool()
}

// showDiagnosticPopup は、カーソルが止まったときに、読み込んでいる client のカーソルの行の診断を、カーソルの近くのポップアップに出す。
// 行に診断が無いとき、どれかのポップアップ（ホバー、ファインダー、前の診断のポップアップなど。フォーカスのあるものを含む）を
// 出しているときは出さない（#329 の決め。オーナーの自動コマンドも浮かべた窓の中では開かない）。
// インサートモードの間とキーの列の途中は、カーソルが止まった契機が来ない（holdReady）。
//
// req: FR-171
func (e *Editor) showDiagnosticPopup() {
	bs := e.cur
	if bs == nil || e.float != nil {
		return
	}
	ds := lineDiagnostics(bs, bs.state.Cursor().Line)
	if len(ds) == 0 {
		return
	}
	lines, decs := diagnosticPopupLines(ds, ui.PopupTextWidth(e.renderer.Width), e.scratchTabWidth())
	if f := e.openLinesFloat(diagnosticPopupTitle, lines, false); f != nil {
		f.diagnostic = true
		e.setPopupDecorations(f.bs, decs)
	}
}

// diagnosticSpan は、診断の範囲の向きをそろえたもの（r）と、その診断が掛かる行の始まりと終わり（first から last まで。0 から数える）である。
type diagnosticSpan struct {
	r           ext.Range
	first, last int
}

// diagnosticSpanOf は、診断の範囲 r の始まりと終わりが逆なら入れ替え、掛かる行を決める。
// 診断の範囲の向きと掛かる行は、この関数だけで決める（ポップアップ、メッセージの行の 1 行の表示、次と前の診断へ移る操作が使う。
// 後で右端の列に診断の位置を示すときも、全部の診断を 1 回走査してここで行に振り分ける。#317 の第 2 弾）。
//
// 掛かる行は Neovim 0.12.5 の open_float の scope = "line" と同じく決める。範囲の始まりの行から終わりの行までに掛かるが、
// 複数の行にまたがる範囲が行の先頭（バイト 0）で終わるなら、終わりの行には掛からないとする。
//
// req: FR-171
func diagnosticSpanOf(r ext.Range) diagnosticSpan {
	s, end := r.Start, r.End
	if end.Line < s.Line || (end.Line == s.Line && end.Byte < s.Byte) {
		s, end = end, s
	}
	last := end.Line
	if s.Line != end.Line && end.Byte == 0 {
		last--
	}
	return diagnosticSpan{r: ext.Range{Start: s, End: end}, first: s.Line, last: last}
}

// lineDiagnostics は、バッファ bs の行 line（0 から）に掛かる診断（diagnosticSpanOf）を、ポップアップに並べる順にして返す。
// 返す診断の範囲は、向きをそろえたものである。
// 並びは重要度の重い順で、同じ重要度なら範囲の始まり（行、列）、終わり（行、列）の順にする（Neovim の severity_sort）。
// それも同じなら、送った拡張機能の ID の順と、拡張機能が送った順にする。
//
// req: FR-171
func lineDiagnostics(bs *bufferState, line int) []ext.Diagnostic {
	var out []ext.Diagnostic
	for _, id := range slices.Sorted(maps.Keys(bs.diagnostics)) {
		for _, d := range bs.diagnostics[id] {
			span := diagnosticSpanOf(d.Range)
			if line < span.first || line > span.last {
				continue
			}
			d.Range = span.r
			out = append(out, d)
		}
	}
	slices.SortStableFunc(out, func(a, b ext.Diagnostic) int {
		return cmp.Or(
			cmp.Compare(severityRank[b.Severity], severityRank[a.Severity]),
			cmp.Compare(a.Range.Start.Line, b.Range.Start.Line),
			cmp.Compare(a.Range.Start.Byte, b.Range.Start.Byte),
			cmp.Compare(a.Range.End.Line, b.Range.End.Line),
			cmp.Compare(a.Range.End.Byte, b.Range.End.Byte),
		)
	})
	return out
}

// diagnosticSignStyle は、重要度ごとの、ポップアップの診断の本文の色の名前（印の列の印と同じ色）である。
var diagnosticSignStyle = map[ext.DiagnosticSeverity]string{
	ext.SeverityError:   ui.StyleSignDiagnosticError,
	ext.SeverityWarning: ui.StyleSignDiagnosticWarning,
	ext.SeverityInfo:    ui.StyleSignDiagnosticInfo,
	ext.SeverityHint:    ui.StyleSignDiagnosticHint,
}

// diagnosticPopupLines は、並べた診断 ds を、ポップアップのバッファの行と、その色を付ける範囲にする（このファイルの doc の形）。
// 行は幅 width で空白の位置で折り返す（ui.WrapText。タブ文字の幅は tabStop）。色は、各診断の最初の行の番号を除いた全体に付ける。
//
// req: FR-171
func diagnosticPopupLines(ds []ext.Diagnostic, width, tabStop int) (lines []string, decs []ext.Decoration) {
	for i, d := range ds {
		text := d.Message
		if d.Code != "" {
			text += " (" + d.Code + ")"
		}
		if d.Source != "" {
			text = d.Source + ": " + text
		}
		prefix := fmt.Sprintf("%d. ", i+1)
		style := diagnosticSignStyle[d.Severity]
		for j, l := range strings.Split(text, "\n") {
			pre := prefix
			if j > 0 {
				pre = strings.Repeat(" ", len(prefix))
			}
			pieces := ui.WrapText(pre+l, width, tabStop)
			if len(pieces) == 0 {
				pieces = []string{""}
			}
			for k, p := range pieces {
				start := 0
				if j == 0 && k == 0 {
					start = min(len(prefix), len(p))
				}
				if style != "" && start < len(p) {
					row := len(lines)
					decs = append(decs, ext.Decoration{
						Range: ext.Range{Start: ext.Position{Line: row, Byte: start}, End: ext.Position{Line: row, Byte: len(p)}},
						Style: style,
					})
				}
				lines = append(lines, p)
			}
		}
	}
	// Neovim も、窓の中身の末尾の空の行を除く（open_floating_preview の trimempty）。
	for len(lines) > 1 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines, decs
}
