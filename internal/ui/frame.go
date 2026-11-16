package ui

import "github.com/piplupflipper/modal-editor/internal/buffer"

// Text は、描画するバッファの内容を読むためのインターフェースである。*buffer.Buffer がこれを満たす。
// 描画は読むだけで、バッファを変更しない。
type Text interface {
	// LineCount は行数を返す。行は Vim と同じく数える（"a\nb\n" は 2 行）。空のバッファは 1 行である。
	LineCount() int
	// Line は、i 行目（0 から数える）を、行の区切りを含めずに返す。
	Line(i int) string
}

// Mode は、描画に使うモードである。
// internal/command の Mode と同じ並びだが、internal/command とは同じ層で import できないので、
// internal/editor が対応させて詰める。
type Mode int

// モード。
const (
	ModeNormal Mode = iota
	ModeInsert
	ModeVisual
	ModeVisualLine
	ModeCommandLine
)

// String は、ステータス行に出すモードの名前を返す。
func (m Mode) String() string {
	switch m {
	case ModeNormal:
		return "NORMAL"
	case ModeInsert:
		return "INSERT"
	case ModeVisual:
		return "VISUAL"
	case ModeVisualLine:
		return "VISUAL LINE"
	case ModeCommandLine:
		return "COMMAND"
	}
	return "NORMAL"
}

// Selection は、ビジュアルモードの選択である。internal/command の State.Selection の戻り値をそのまま詰める。
type Selection struct {
	// Active は、選択があるかどうかである。false なら他の欄を見ない。
	Active bool
	// Start と End は、選択の始まりと終わりの文字の位置で、どちらも含む。End が Start より前でもよい。
	Start, End buffer.Position
	// Linewise は、行単位の選択（V）かどうかである。
	Linewise bool
}

// Highlight は、色を付ける 1 つの範囲である。構文の色（decoration/set の 1 要素）に使う。
type Highlight struct {
	// Range は、色を付ける範囲である。位置は描画する時点のテキストを指す必要がある。
	// 編集に合わせて位置をずらすのは、表示のデータを持つ internal/editor の役目である（基本設計 6.1、6.7）。
	Range buffer.Range
	// Style は、色の種類の名前（StyleKeyword など）である。Theme に無い名前なら色を付けない。
	Style string
}

// Severity は、診断の重要度である。値は diagnostic/set の severity と同じ文字列である（基本設計 6.7）。
type Severity string

// 診断の重要度。
const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
	SeverityInfo    Severity = "info"
	SeverityHint    Severity = "hint"
)

// Diagnostic は、下線を引く 1 つの診断の範囲である。
type Diagnostic struct {
	// Range は、診断の範囲である。空の範囲は、その位置の 1 文字（行末なら行末の 1 セル）に下線を引く。
	Range    buffer.Range
	Severity Severity
}

// MessageLevel は、コマンドラインの行に出すメッセージの重要度である。
type MessageLevel int

// メッセージの重要度。
const (
	MessageInfo MessageLevel = iota
	MessageWarning
	MessageError
)

// Message は、コマンドラインの行に出すメッセージである。Text が空ならメッセージを出さない。
type Message struct {
	// Text は本文である。改行を含むときは、コマンドラインの行から上へ行を広げて出す（Vim のメッセージの行が広がるのと同じ）。
	// 広げるのは画面の半分まで（Frame.HitEnter の問いの行を除く）で、収まらない行は省いたことを最後の行に書く。
	Text  string
	Level MessageLevel
}

// SignKind は、行の印の種類である。値は sign/set の kind と同じ文字列である（基本設計 6.7）。
type SignKind string

// 行の印の種類。
const (
	SignBreakpoint           SignKind = "breakpoint"
	SignBreakpointUnverified SignKind = "breakpointUnverified"
	SignExecutionPoint       SignKind = "executionPoint"
)

// Sign は、印の列に出す 1 つの印である（sign/set の 1 要素）。
type Sign struct {
	// Line は、0 から数えた行の番号である。
	Line int
	// Kind は、印の種類である。知らない種類は描かない。
	Kind SignKind
}

// Output は、画面の下に開いた出力の欄である（output/append。基本設計 6.7）。
type Output struct {
	// Title は、欄の下の行に出す欄の名前である。
	Title string
	// Lines は、欄のテキストを行に分けたものである。描画は読むだけで変更しない。
	Lines []string
	// Bottom は、欄の最後の行に出す行の次の番号（Lines の添字）である。負なら最後の行まで出す（末尾に追従する）。
	// Bottom の手前の行で欄が埋まらなければ、先頭の行から欄が埋まるまで出す。
	Bottom int
	// Skip は、Bottom の手前の行を折り返した画面の行のうち、欄の下の端より下に隠す行数である。
	// スクロールの位置を画面の行で表すためのもので、Bottom が負なら使わない。
	Skip int
}

// Frame は、1 回の描画の入力である。
// internal/editor が、internal/command の State と、拡張機能から受け取った表示のデータから詰める。
// 描画は Frame を読むだけで、どの欄も変更しない。
type Frame struct {
	// Text は、表示するバッファの内容である。nil なら空のバッファとして描く。
	Text Text
	// Name は、ステータス行に出すバッファの名前（ファイルのパス）である。空なら "[No Name]" と出す。
	Name string
	// Modified は、バッファに保存していない変更があるかどうかである。
	Modified bool
	Mode     Mode
	// Cursor は、カーソルの位置である。範囲の外なら、最も近いバッファの中の位置として描く。
	Cursor    buffer.Position
	Selection Selection
	// CommandLine は、コマンドラインモードで入力中の文字列（先頭の ":" を除く）である。
	CommandLine string
	// OperatorPending は、オペレーターの後でモーションを待っているかどうかである。カーソルを下線の形にする。
	OperatorPending bool
	// Recording は、記録中のマクロのレジスターの名前である。0 なら記録していない。
	Recording rune
	// Message は、コマンドラインモードでないときにコマンドラインの行に出すメッセージである。
	Message Message
	// HitEnter が true で、Message が改行を含むなら、メッセージの下の最後の行に、続けるためのキーを促す問い（HitEnterPrompt）を出し、
	// カーソルを問いの末尾に置く（Vim の hit-enter の問い）。キーの扱いは internal/editor が行う。
	HitEnter bool
	// Decorations は、構文の色の範囲を行ごとに分けたもの（NewDecorations で作る）である。nil なら色を付けない。
	Decorations *Decorations
	// Diagnostics は、診断の範囲の一覧である。構文の色の上に重ねる。範囲の始まりの行には、印の列に診断の印も出す。
	Diagnostics []Diagnostic
	// Signs は、行の印の一覧である。1 つの行に複数の印（診断の印を含む）があれば、優先順位の最も高いものだけを出す（基本設計 6.7。順位は signMarks の doc）。
	Signs []Sign
	// Output は、開いている出力の欄である。nil なら欄を開いていない。
	Output *Output
	// Popup は、カーソルの近くに浮かべて出すテキスト（popup/show の本文）である。空なら出さない。
	// カーソルの下の行に、下に収まらなければ上の行に、バッファの表示領域の上に重ねて出す。行は折り返す。
	Popup string
}
