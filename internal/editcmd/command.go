package editcmd

// Kind は、編集コマンドの値の種類を表す。
type Kind int

// 編集コマンドの値の種類。
const (
	// KindMotion は、モーションだけの移動である（例: 3j）。Target に移動先が入る。
	KindMotion Kind = iota + 1
	// KindOperator は、オペレーターと対象の組である（例: d2w、ci"、dd）。
	KindOperator
	// KindAction は、対象を取らない操作である（例: x、p、rx、i、:）。
	KindAction
	// KindRecordStart は、マクロの記録の開始（q{reg}）である。Char にレジスターの名前が入る。
	KindRecordStart
	// KindRecordStop は、マクロの記録の停止（記録中の q）である。
	// Parser は記録中かを知らないので、呼び出し元が Parser.SetRecording で知らせる。
	KindRecordStop
	// KindPlay は、マクロの再生（@{reg}）である。Char にレジスターの名前が入り、@@ では '@' が入る。
	KindPlay
	// KindRepeat は、ドットリピート（.）である。
	KindRepeat
)

// TargetKind は、オペレーターやモーションの対象の種類を表す。
type TargetKind int

// 対象の種類。
const (
	TargetNone       TargetKind = iota // 対象なし（KindAction）
	TargetMotion                       // モーション
	TargetTextObject                   // テキストオブジェクト
	TargetLine                         // オペレーターの重ね押し（dd、yy）による行単位の対象
)

// Mode は、コマンドの実行の後に移るモードを表す。
// モードの切り替えの実行は internal/command の担当で、この値はその指示である。
type Mode int

// 実行の後のモード。
const (
	ModeNormal      Mode = iota // ノーマルモードのまま
	ModeInsert                  // インサートモード（i、a、c など）
	ModeCommandLine             // コマンドラインモード（:）
)

// Ref は、操作（モーション、オペレーター、テキストオブジェクト、対象を取らない操作）を指す。
// Extension が空なら本体の操作で、ID は本体の中の名前である。
// 空でなければ拡張機能の ID で、ID はその拡張機能のコマンドの ID である。
// ドットリピートとマクロの再実行では、本体はこの組で同じ拡張機能へ問い合わせ直す。
//
// req: FR-12
type Ref struct {
	ID        string
	Extension string
}

// Builtin は、本体の操作であるかを返す。
func (r Ref) Builtin() bool { return r.Extension == "" }

// ProviderExtension は、提供者へ振り分ける本体の操作（LSP と DAP の操作。基本設計 5.5、FR-94）の Ref.Extension に入れる値である。
// 実行の側（internal/command）からは拡張機能の操作に見え、問い合わせる側（internal/editor）が呼ばれた時点の提供者に置き換える。
// 値は空白を含むので、宣言ファイルの拡張機能の ID（空白を含めない。ext.Manifest.Validate）と重ならない。
const ProviderExtension = " provider"

// Provider は、提供者へ振り分ける本体の操作であるかを返す。
func (r Ref) Provider() bool { return r.Extension == ProviderExtension }

// Target は、オペレーターまたはモーションの対象である。
type Target struct {
	Kind TargetKind
	Ref  Ref  // TargetLine と TargetNone では空
	Char rune // f、F、t、T のような文字の引数。<CR> は '\n'、<Tab> は '\t'。引数を取らない対象では 0
	// Pattern は、検索のモーション（/ と ?）のパターンである。Parser は埋めない。
	// 空のまま実行すると、internal/command がコマンドラインでパターンを受け取り、<CR> の時点で埋めてから実行する。
	// ドットリピートとマクロの再実行では、埋めた値で同じパターンを検索し直す（d/foo<CR> の後の . は foo を検索する）。
	Pattern string
}

// Command は、キーの列から解釈した編集コマンドの値である（基本設計 3 章の表）。
// ドットリピートはこの値を記録し、もう一度実行へ渡す。
//
// 基本設計の表の「提供元」は、値全体で 1 つではなく、Operator、Target.Ref、Action の
// それぞれの Ref に持つ。本体のオペレーターと拡張機能のテキストオブジェクトの組み合わせを表すためである。
//
// req: FR-02, FR-06
type Command struct {
	Kind Kind

	// Register は、"a のように指定したレジスターの名前。省略した場合は 0 で、既定のレジスターを表す。
	Register rune

	// Count は回数で、省略した場合は 1。オペレーターの前と後の回数の積である（2d3w は 6）。
	// KindRepeat で CountGiven が true のときは、記録した値の回数をこの回数で置き換える（Vim と同じく、
	// 2d3w の後の 3. は d3w ではなく 3 回の dw に当たる。積は取らない）。置き換えは実行の側が行う。
	Count int
	// CountGiven は、回数を明示したかを表す。G や :（範囲）のように、省略と 1 で意味が違う操作のために持つ。
	CountGiven bool

	// Operator は、KindOperator のときのオペレーター。
	Operator Ref
	// Target は、KindMotion と KindOperator のときの対象。
	Target Target
	// Action は、KindAction のときの操作。
	Action Ref
	// Char は、操作の後に取る文字の引数である。
	// KindAction では r の文字、KindOperator では、対象の後に文字を取るオペレーター（拡張機能の
	// surround の ysiw) の ')' など）の文字、KindRecordStart と KindPlay ではレジスターの名前が入る。
	// <CR> は '\n'、<Tab> は '\t' として入る。引数を取らなければ 0。
	// ドットリピートとマクロの再実行で、拡張機能の操作に同じ引数を渡すために値に残す（FR-12）。
	Char rune
	// Char2 は、文字の引数を 2 つ取る操作（本体の sr の、後の文字）の 2 つ目の文字である。
	// Char と同じく <CR> は '\n'、<Tab> は '\t' として入る。2 つ目を取らなければ 0。
	Char2 rune

	// Mode は、実行の後に移るモード。
	Mode Mode

	// Text は、c や i のように挿入を伴うコマンドで入力された文字列である。
	// Parser は埋めない。インサートモードを抜けた時点で internal/command が埋め、ドットリピートに使う。
	Text string
}

// Status は、キーの列の解釈（internal/input の Parser）が返す結果の種類を表す。
type Status int

// 結果の種類。
const (
	// StatusDone は、編集コマンドの値が 1 つ完成したことを表す。
	StatusDone Status = iota + 1
	// StatusInvalid は、どの操作にも当たらない列（dq など）を受け取り、解釈を捨てて初期状態に戻ったことを表す。
	StatusInvalid
	// StatusCanceled は、Esc で解釈を捨てて初期状態に戻ったことを表す。
	StatusCanceled
)

// Result は、Parser の結果の 1 件である。Command は Status が StatusDone のときだけ意味を持つ。
type Result struct {
	Command Command
	Status  Status
}
