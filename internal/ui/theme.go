package ui

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
	// StyleMessageInfo、StyleMessageWarning、StyleMessageError は、コマンドラインの行に出すメッセージである。
	StyleMessageInfo    = "message.info"
	StyleMessageWarning = "message.warning"
	StyleMessageError   = "message.error"
	// StyleDiagnosticError などは、診断の範囲である。名前は "diagnostic." と、診断の重要度（基本設計 6.7）をつないだもの。
	StyleDiagnosticError   = "diagnostic.error"
	StyleDiagnosticWarning = "diagnostic.warning"
	StyleDiagnosticInfo    = "diagnostic.info"
	StyleDiagnosticHint    = "diagnostic.hint"
)

// Theme は、色の種類の名前から属性への対応である。
// 表に無い名前の範囲には色を付けない（基本設計 6.1）。
type Theme map[string]Attr

// DefaultTheme は、既定の対応（仮）を返す。色は端末の 16 色の番号で、Vim の暗い背景の既定の色に近いものを選んだ。
// 設定から差し替える仕組みは、設定（internal/config）を作るときに足す。
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

		StyleVisual:         {Reverse: true},
		StyleStatusLine:     {Reverse: true, Bold: true},
		StyleNonText:        {Fg: Indexed(12)},
		StyleSpecialKey:     {Fg: Indexed(12)},
		StyleMessageWarning: {Fg: Indexed(11)},
		StyleMessageError:   {Fg: Indexed(15), Bg: Indexed(1)},

		StyleDiagnosticError:   {Undercurl: true, Sp: Indexed(9)},
		StyleDiagnosticWarning: {Undercurl: true, Sp: Indexed(11)},
		StyleDiagnosticInfo:    {Underline: true, Sp: Indexed(12)},
		StyleDiagnosticHint:    {Underline: true, Sp: Indexed(8)},
	}
}

// attr は、名前 name の属性を返す。表に無い名前ならゼロ値（飾りなし）を返す。
func (t Theme) attr(name string) Attr {
	return t[name]
}
