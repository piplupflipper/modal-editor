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

// Choice は、選択肢の問い（choice/ask）の表示である（Frame.Choice）。
type Choice struct {
	// Title は、問いの文である。改行を含んでよい。
	Title string
	// Labels は、選択肢の表示である。i 番目を "[i+1] ラベル" の形で 1 行に出す。番号のキーで選べるものだけを入れる。
	Labels []string
	// Omitted は、番号のキーが足りずに Labels に入れなかった選択肢の数である。0 より大きければ、その旨を選択肢の後の行に書く。
	Omitted int
	// Index は、続けて届いた問いのうち何件目か（1 から数える）である。0 より大きければ、選び方の案内の前に "問い 2: " の形で出す（#151）。
	// 問いを出す拡張機能が、前の答えを受け取ってから次の問いを送ることがあるので、全体の件数は出さない。
	Index int
	// Note は、問いの文の上に出す知らせ（前の問いへの答え、待たせている問いの数、捨てた入力の数）である。
	// 改行を含んでよい。空なら出さない（#151）。
	Note string
}

// List は、浮かべる窓に出す選択の一覧である（Frame.List。:registers の一覧。FR-54）。
type List struct {
	// Title は、窓の最初の行に出す見出し（選び方の案内）である。
	Title string
	// Items は、項目の表示である。1 項目を 1 行に出し、幅に収まらなければ右の端で切る。
	Items []string
	// Selected は、選んでいる項目の添字である。その行を StylePopupSelected で描く。
	Selected int
}

// Float は、ポップアップの窓 1 つである（Frame.Float）。中身は、本体の表示の部品をバッファにしたもので（internal/editor）、
// ui はテキストのバッファとして描く。一覧と木は 1 項目を 1 行にしたバッファで、CursorLine でカーソルの行を選んでいる項目として描く。
type Float struct {
	// ID は、窓を見分ける番号である。替わったら窓の中のスクロールの位置を決め直す。0 も 1 つの窓として扱う。
	ID uint64
	// View は、窓の中に描くバッファの Frame である。Text、Cursor、Mode、Selection、Decorations、Diagnostics を読み、
	// Tabs、Output、List、Choice、Float、Diff、Message は読まない。Text が nil なら窓を出さない。
	View *Frame
	// Title は、窓の見出しである。空なら出さない。
	Title string
	// Center は、窓をカーソルの近くでなく、バッファの表示領域の中央に置くことを表す。
	Center bool
	// Focused は、窓にフォーカスがある（キーが窓のバッファへ入る）ことを表す。端末のカーソルを窓の中のカーソルに置く。
	Focused bool
	// CursorLine は、カーソルの行を StylePopupSelected で描くことを表す（一覧と木）。
	CursorLine bool
	// Wide は、窓の幅を中身に依らず上限の幅にすることを表す（入力の行。打つたびに窓が広がったり、
	// 行末のカーソルの 1 列が足りずに先頭が横に隠れたりしないため。PR #242 の再レビューの推奨 2）。
	Wide bool
	// Fill は、窓をバッファの表示領域の全体に広げ、枠線を描かずに見出しを最初の行に描くことを表す。
	// 端末多重化ソフトのポップアップとペインの中の部品の client（基本設計 1.2）の画面に使う。
	// 端末多重化ソフトが枠線を描くので、二重にしないためである。Center と幅と行数の上限は読まない。
	Fill bool
	// List は、入力の行と一覧の組（ファインダー。FR-106）の一覧のバッファの Frame である。nil でなければ、View を入力の行として
	// 窓の最初の行に、List を区切りの行の下に描く（drawFinder）。Text、Cursor、Decorations を読む。
	// 一覧のカーソルの行は StylePopupSelected で描き、端末のカーソルは入力の行に置く。
	List *Frame
	// ListCount は、List の項目の数である。0 なら一覧の場所に空であることを書く。
	ListCount int
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
	// ReadOnly は、バッファの編集を禁止しているかどうかである（基本設計 4.6）。true ならステータス行に編集の禁止の印を出す（FR-87）。
	ReadOnly bool
	// FileType は、バッファのファイルの種類の名前（基本設計 6.7）である。ステータス行の部品 StatusPartFileType に出す。空なら出さない。
	FileType string
	Mode     Mode
	// Cursor は、カーソルの位置である。範囲の外なら、最も近いバッファの中の位置として描く。
	Cursor    buffer.Position
	Selection Selection
	// CommandLine は、コマンドラインモードで入力中の文字列（先頭の ":" を除く）である。
	CommandLine string
	// CommandLineType は、コマンドラインの行の先頭に出す文字である（Ex コマンドは ':'、検索は '/' か '?'）。0 なら ':' にする。
	CommandLineType rune
	// OperatorPending は、オペレーターの後でモーションを待っているかどうかである。カーソルを下線の形にする。
	OperatorPending bool
	// Recording は、記録中のマクロのレジスターの名前である。0 なら記録していない。
	Recording rune
	// Message は、コマンドラインモードでないときにコマンドラインの行に出すメッセージである。
	Message Message
	// HitEnter が true で、Message が改行を含むなら、メッセージの下の最後の行に、続けるためのキーを促す問い（HitEnterPrompt）を出し、
	// カーソルを問いの末尾に置く（Vim の hit-enter の問い）。キーの扱いは internal/editor が行う。
	HitEnter bool
	// Choice は、出している選択肢の問い（choice/ask）である。nil なら問いを出していない。
	// nil でなければ、Message とコマンドラインの代わりに、画面の下から上へ問いの文と選択肢を 1 つずつ行に分けて描き、
	// 最後の行に選び方の案内（ChoicePrompt）を出してカーソルをその末尾に置く。キーの扱いは internal/editor が行う。
	Choice *Choice
	// Decorations は、構文の色の範囲を行ごとに分けたもの（NewDecorations で作る）である。nil なら色を付けない。
	Decorations *Decorations
	// Diagnostics は、診断の範囲の一覧である。構文の色の上に重ねる。範囲の始まりの行には、印の列に診断の印も出す。
	Diagnostics []Diagnostic
	// Signs は、行の印の一覧である。1 つの行に複数の印（診断の印を含む）があれば、優先順位の最も高いものだけを出す（基本設計 6.7。順位は signMarks の doc）。
	Signs []Sign
	// Output は、開いている出力の欄である。nil なら欄を開いていない。
	Output *Output
	// Float は、バッファの表示領域の上に浮かべて出すポップアップである（基本設計 1.4。FR-68）。nil なら出さない。
	// 描き方は drawFloat の doc にある。
	Float *Float
	// List は、浮かべる窓に出す選択の一覧（:registers の一覧。FR-54）である。nil なら出さない。
	// 出すときは Float を出さず、カーソルを選んでいる項目の行に置く。キーの扱いは internal/editor が行う。
	List *List
	// Tabs は、client のタブの並びである（FR-62）。Renderer.TabLine の場合に、画面の一番上の行にタブの行として出す。
	Tabs []Tab
	// Diff は、見比べている間の差分の色と詰め物の行である（FR-99。diff.go）。nil なら見比べていない。
	Diff *Diff
	// ExtParts は、拡張機能が足した部品の ID から中身への対応である（statusline/set。FR-92）。
	// ステータス行とタブ行の並び（Renderer.StatusLine、Renderer.TabLineParts）に書いた ID のうち、本体の部品でないものをここで引く。
	// 対応に無い部品と、中身の Text が空の部品は出さない。
	ExtParts map[string]ExtPart
}

// ExtPart は、拡張機能が足した部品の中身である（ext.StatusLineSetParams）。
type ExtPart struct {
	// Text は、部品に出す 1 行の文字列である。
	Text string
	// Style は、部品の色の名前である。StyleStatusLineExt の上に重ねる。空なら重ねない。
	Style string
}
