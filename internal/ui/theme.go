package ui

import (
	"maps"
	"slices"
)

// 色の種類の名前（論理名）。名前から属性への対応は Theme が持つ。
//
// 構文の色の名前は、基本設計 6.1 の decoration/set の style の表（仮）と同じである。
// それ以外は、画面の部品と診断に付ける、このパッケージが定めた名前（仮）である。
const (
	StyleComment     = "comment"
	StyleKeyword     = "keyword"
	StyleString      = "string"
	StyleNumber      = "number"
	StyleConstant    = "constant"
	StyleFunction    = "function"
	StyleType        = "type"
	StyleVariable    = "variable"
	StyleOperator    = "operator"
	StylePunctuation = "punctuation"

	// StyleVisual は、ビジュアルモードの選択の範囲である。
	StyleVisual = "visual"
	// StyleStatusLine は、ステータス行である。
	StyleStatusLine = "statusline"
	// StyleNonText は、バッファの最終行より後の行の "~" と、横のスクロールで左の端が欠けた全角文字の "<" である。
	StyleNonText = "nontext"
	// StyleSpecialKey は、^A や <80> の形で出した文字である。
	StyleSpecialKey = "specialkey"
	// StyleLineNr は、行番号の列（Frame.LineNumbers。FR-135）である。数字と、右の空けた 1 列を含む列の全体に付ける（Vim の LineNr）。
	StyleLineNr = "linenr"
	// StyleSearch は、直前の検索のパターンに一致する範囲（Frame.Search。FR-137）である（Vim の Search）。
	// 構文の色、見比べの色、診断の上に重ね、選択の色（StyleVisual）の下に置く。
	StyleSearch = "search"
	// StyleIncSearch は、/ と ? でパターンを打っている間に、打つたびの検索で出す今の一致（Frame.Highlights の 1 つ。FR-153）である
	// （Vim と Neovim の IncSearch）。検索の一致の色（StyleSearch）とヤンクした範囲の色（StyleYank）の上に重ね、選択の色の下に置く。
	StyleIncSearch = "incsearch"
	// StyleYank は、ヤンクした範囲に一時的に付ける色（Frame.Highlights。FR-140）である。
	// nvim の vim.hl.on_yank が既定で使う IncSearch に当たる。検索の一致の色（StyleSearch）の上に重ね、選択の色の下に置く。
	// 選択と重なったところで選択の色が上に見えるよう、既定と同梱の配色では反転でなく背景の色で付ける。
	StyleYank = "yank"
	// StyleMessageInfo、StyleMessageWarning、StyleMessageError は、コマンドラインの行に出すメッセージである。
	StyleMessageInfo    = "message.info"
	StyleMessageWarning = "message.warning"
	StyleMessageError   = "message.error"
	// StyleDiagnosticError などは、診断の範囲である。名前は "diagnostic." と、診断の重要度（基本設計 6.7）をつないだもの。
	StyleDiagnosticError   = "diagnostic.error"
	StyleDiagnosticWarning = "diagnostic.warning"
	StyleDiagnosticInfo    = "diagnostic.info"
	StyleDiagnosticHint    = "diagnostic.hint"
	// StylePopup は、カーソルの近くに浮かべる popup/show の本文である（Vim の Pmenu に当たる）。
	StylePopup = "popup"
	// StylePopupSelected は、浮かべる一覧（Frame.List）の選んでいる項目である（Vim の PmenuSel に当たる）。
	StylePopupSelected = "popup.selected"
	// StyleStatusLineNC は、出力の欄の下の行（カーソルの無い窓のステータス行。Vim の StatusLineNC）である。
	StyleStatusLineNC = "statuslinenc"
	// StyleSign... は、印の列の印である。名前は "sign." と印の種類をつないだもの。診断の印は "sign.diagnostic." と重要度をつなぐ。
	StyleSignBreakpoint           = "sign.breakpoint"
	StyleSignBreakpointUnverified = "sign.breakpointUnverified"
	StyleSignExecutionPoint       = "sign.executionPoint"
	StyleSignDiagnosticError      = "sign.diagnostic.error"
	StyleSignDiagnosticWarning    = "sign.diagnostic.warning"
	StyleSignDiagnosticInfo       = "sign.diagnostic.info"
	StyleSignDiagnosticHint       = "sign.diagnostic.hint"
	// StylePopupBorder は、浮かべる窓（popup/show の本文と Frame.List）の枠線である（記号 SymbolPopupBorder を設定したときだけ描く）。
	// 表に無ければ StylePopup で描く。
	StylePopupBorder = "popup.border"
	// StyleFinderMatch は、ファインダー（FR-106）の一覧の、入力に一致した文字である（fzf の hl に当たる）。
	// 拡張機能が一覧の項目の highlights でこの名前を指す。
	StyleFinderMatch = "finder.match"
	// StylePreviewLine は、項目のプレビュー（Float.Preview。FR-149）の、項目の指す行（grep の一致した行など）である。
	// 行の全体に付け、構文の色はその上に重ねる（fzf-lua のプレビューの FzfLuaCursorLine に当たる）。
	StylePreviewLine = "preview.line"
	// StyleStatusLineMode などは、ステータス行の部品ごとの色である。名前は "statusline." と部品の ID（StatusPart...）をつないだもの。
	// 部品は StyleStatusLine の上に重ねて描く（Attr.Over）ので、文字の色だけを変えることもできる。既定の対応には無い。
	StyleStatusLineMode      = "statusline.mode"
	StyleStatusLineName      = "statusline.name"
	StyleStatusLineModified  = "statusline.modified"
	StyleStatusLineReadOnly  = "statusline.readonly"
	StyleStatusLineFileType  = "statusline.filetype"
	StyleStatusLineRecording = "statusline.recording"
	StyleStatusLineRuler     = "statusline.ruler"
	StyleStatusLinePosition  = "statusline.position"
	// StyleStatusLineExt は、拡張機能が足した部品（Frame.ExtParts）の色である。部品の style（ExtPart.Style）は、この上に重ねる。
	StyleStatusLineExt = "statusline.ext"
	// StyleStatusLineModeNormal などは、モードごとのモードの部品の色である。StyleStatusLineMode の上に重ねる。
	// ビジュアルモードは文字単位と行単位で同じ名前を使う。
	StyleStatusLineModeNormal      = "statusline.mode.normal"
	StyleStatusLineModeInsert      = "statusline.mode.insert"
	StyleStatusLineModeVisual      = "statusline.mode.visual"
	StyleStatusLineModeCommandLine = "statusline.mode.commandline"
	// StyleTabLine は、タブの行の、カーソルの無いタブの見出しである（FR-62。Vim の TabLine）。
	StyleTabLine = "tabline"
	// StyleTabLineSelected は、タブの行の、カーソルのあるタブの見出しである（Vim の TabLineSel）。
	StyleTabLineSelected = "tabline.selected"
	// StyleTabLineFill は、タブの行の、見出しの右の空いた列である（Vim の TabLineFill）。
	StyleTabLineFill = "tabline.fill"
)

// styleNames は、本体が色を付ける対象の名前の全部である（StyleNames）。
var styleNames = []string{
	StyleComment, StyleKeyword, StyleString, StyleNumber, StyleConstant, StyleFunction, StyleType, StyleVariable,
	StyleOperator, StylePunctuation,
	StyleVisual, StyleStatusLine, StyleStatusLineNC, StyleNonText, StyleSpecialKey, StyleLineNr, StyleSearch, StyleIncSearch, StyleYank,
	StyleMessageInfo, StyleMessageWarning, StyleMessageError,
	StyleDiagnosticError, StyleDiagnosticWarning, StyleDiagnosticInfo, StyleDiagnosticHint,
	StylePopup, StylePopupSelected, StylePopupBorder, StyleFinderMatch, StylePreviewLine,
	StyleSignBreakpoint, StyleSignBreakpointUnverified, StyleSignExecutionPoint,
	StyleSignDiagnosticError, StyleSignDiagnosticWarning, StyleSignDiagnosticInfo, StyleSignDiagnosticHint,
	StyleStatusLineMode, StyleStatusLineName, StyleStatusLineModified, StyleStatusLineReadOnly, StyleStatusLineFileType,
	StyleStatusLineRecording, StyleStatusLineRuler, StyleStatusLinePosition, StyleStatusLineExt,
	StyleStatusLineModeNormal, StyleStatusLineModeInsert, StyleStatusLineModeVisual, StyleStatusLineModeCommandLine,
	StyleTabLine, StyleTabLineSelected, StyleTabLineFill,
	StyleDiffAdd, StyleDiffChange, StyleDiffDelete, StyleDiffText,
}

// StyleNames は、本体が色を付ける対象の名前（設定の colors に書ける名前。FR-91）を、定義の順に返す。
// 拡張機能の decoration/set の style も、この名前から選ぶ（基本設計 6.1、7.1）。返す列は呼び出しごとに作り直す。
func StyleNames() []string {
	return slices.Clone(styleNames)
}

// IsStyleName は、name が本体の色を付ける対象の名前（StyleNames）かを返す。
func IsStyleName(name string) bool {
	return slices.Contains(styleNames, name)
}

// Theme は、色の種類の名前から属性への対応である。
// 表に無い名前の範囲には色を付けない（基本設計 6.1）。
type Theme map[string]Attr

// WithColors は、t を写したものに、colors の名前ごとの属性を上書きした対応を返す（設定の colors。FR-91）。
// 上書きは名前ごとの置き換えで、既定の属性と混ぜない（文字の色だけを書いた名前は、既定の背景の色と飾りを持たない）。
// t は変更しない。
//
// req: FR-91
func (t Theme) WithColors(colors map[string]Attr) Theme {
	out := make(Theme, len(t)+len(colors))
	maps.Copy(out, t)
	maps.Copy(out, colors)
	return out
}

// DefaultTheme は、既定の対応（仮）を返す。色は端末の 16 色の番号で、Vim の暗い背景の既定の色に近いものを選んだ。
// 設定の colorscheme で選んだ配色（FR-125）と設定の colors（FR-91）は、この順に WithColors で名前ごとに上書きする。
func DefaultTheme() Theme {
	return Theme{
		StyleComment:  {Fg: Indexed(14)},
		StyleKeyword:  {Fg: Indexed(11)},
		StyleString:   {Fg: Indexed(13)},
		StyleNumber:   {Fg: Indexed(13)},
		StyleConstant: {Fg: Indexed(13)},
		StyleFunction: {Fg: Indexed(14)},
		StyleType:     {Fg: Indexed(10)},
		StyleOperator: {Fg: Indexed(11)},

		StyleVisual:     {Reverse: true},
		StyleStatusLine: {Reverse: true, Bold: true},
		StyleNonText:    {Fg: Indexed(12)},
		StyleSpecialKey: {Fg: Indexed(12)},
		// 行番号は、Vim 9.1 の暗い背景の既定（2026-11-20 に TERM=xterm-256color、background=dark の :hi LineNr で確かめた ctermfg=11）と同じ番号にした。
		StyleLineNr: {Fg: Indexed(11)},
		// 検索の一致は、Vim 9.1 の暗い背景の既定（2026-11-20 に同じ起動の :hi Search で確かめた ctermfg=0 ctermbg=11）と同じ番号にした。
		StyleSearch: {Fg: Indexed(0), Bg: Indexed(11)},
		// 打つたびの検索の今の一致は、Vim 9.1 の暗い背景の既定（2026-11-20 に同じ起動の :hi IncSearch で確かめた cterm=reverse）と同じ反転にした。
		StyleIncSearch: {Reverse: true},
		// ヤンクした範囲は、背景の色だけを持つ。nvim の vim.hl.on_yank の既定の IncSearch（Vim 9.1 の暗い背景の既定は cterm=reverse）に
		// すると、既定の選択（StyleVisual）も反転なので、重なったところと選択だけのところを見分けられない（PR #287 のレビューの推奨 1）。
		// 番号は、Vim 9.1 の暗い背景の MatchParen の既定（2026-11-20 に同じ起動の :hi MatchParen で確かめた ctermbg=6）を借りた。
		// 選択と重なったところは、反転でこの背景の色が文字の色になるので、選択だけのところとも、ヤンクの色だけのところとも違って見える。
		StyleYank:           {Bg: Indexed(6)},
		StyleMessageWarning: {Fg: Indexed(11)},
		StyleMessageError:   {Fg: Indexed(15), Bg: Indexed(1)},

		StyleDiagnosticError:   {Undercurl: true, Sp: Indexed(9)},
		StyleDiagnosticWarning: {Undercurl: true, Sp: Indexed(11)},
		StyleDiagnosticInfo:    {Underline: true, Sp: Indexed(12)},
		StyleDiagnosticHint:    {Underline: true, Sp: Indexed(8)},

		StyleStatusLineNC:  {Reverse: true},
		StylePopup:         {Fg: Indexed(15), Bg: Indexed(8)},
		StylePopupSelected: {Fg: Indexed(0), Bg: Indexed(14)},
		StyleFinderMatch:   {Bold: true, Underline: true},
		// プレビューの項目の指す行は、窓の色（StylePopup の背景の 8 番）の上で見分けられ、構文の色（文字の色）を消さないよう、
		// 背景の色だけを持つ。
		StylePreviewLine:              {Bg: Indexed(0)},
		StyleSignBreakpoint:           {Fg: Indexed(9), Bold: true},
		StyleSignBreakpointUnverified: {Fg: Indexed(8)},
		StyleSignExecutionPoint:       {Fg: Indexed(11), Bold: true},
		StyleSignDiagnosticError:      {Fg: Indexed(9)},
		StyleSignDiagnosticWarning:    {Fg: Indexed(11)},
		StyleSignDiagnosticInfo:       {Fg: Indexed(12)},
		StyleSignDiagnosticHint:       {Fg: Indexed(8)},
		// タブの行は Vim 9.1 の暗い背景の既定（TabLine は下線、TabLineSel は太字、TabLineFill は反転）に近づけた。
		StyleTabLine:         {Underline: true, Fg: Indexed(15), Bg: Indexed(8)},
		StyleTabLineSelected: {Bold: true},
		StyleTabLineFill:     {Reverse: true},
		// 差分の色は、Vim 9.1 の暗い背景の既定と同じ番号にした（2026-11-19 に TERM=xterm-256color、background=dark の :hi で確かめた。
		// DiffAdd は ctermbg=4、DiffChange は ctermbg=5、DiffDelete は ctermfg=12 ctermbg=6、DiffText は cterm=bold ctermbg=9）。
		StyleDiffAdd:    {Bg: Indexed(4)},
		StyleDiffChange: {Bg: Indexed(5)},
		StyleDiffDelete: {Fg: Indexed(12), Bg: Indexed(6)},
		StyleDiffText:   {Bg: Indexed(9), Bold: true},
	}
}

// attr は、名前 name の属性を返す。表に無い名前ならゼロ値（飾りなし）を返す。
func (t Theme) attr(name string) Attr {
	return t[name]
}
