package ext

import "errors"

// ファイルでないバッファの作成と、拡張機能から始める見比べ（基本設計 5.3 の段階 3 の表、6.8、6.9。FR-88、FR-99、FR-104）。
//
// git 連携の拡張機能は、バッファのファイルの HEAD の版を buffer/create でファイルでないバッファにし、
// diff/start でそのファイルのバッファと見比べさせる。
//
// メソッドと向き（名前は仮）:
//
//   - buffer/create（拡張機能 → 本体、リクエスト）: BufferCreateParams のテキストで、ファイルでないバッファを作る。
//     応答は BufferCreateResult。作ったバッファはどのタブにもしない。本体は応答より先に、稼働中の拡張機能へ buffer/open を送る
//   - diff/start（拡張機能 → 本体、リクエスト）: DiffStartParams の 2 つのバッファの見比べを始める。応答は DiffStartResult
const (
	// MethodBufferCreate は、拡張機能から本体へのリクエストである。params は BufferCreateParams、result は BufferCreateResult。
	MethodBufferCreate = "buffer/create"
	// MethodDiffStart は、拡張機能から本体へのリクエストである。params は DiffStartParams、result は DiffStartResult。
	MethodDiffStart = "diff/start"
)

// BufferCreateParams は、buffer/create の params である。
//
// 作るバッファは、ファイルを持たない（保存の先が無い）。
// FR-88 のとおり、既定では編集を禁止した状態で始める。Writable を true にしたときだけ編集できる状態で始める。
type BufferCreateParams struct {
	// Title は、バッファの名前である。タブの行、ステータス行、:ls にパスの代わりに出す。空にはできない。
	Title string `json:"title"`
	// Text は、バッファの中身である。位置を数えるもとのテキスト（基本設計 4.1）で、末尾の改行を含めない。
	Text string `json:"text"`
	// FileType は、ファイルの種類である（構文の色付けなどに使う）。省けば種類の無いバッファにする。
	FileType string `json:"fileType,omitempty"`
	// Writable は、true なら編集できる状態で始めることを表す。省けば編集を禁止した状態で始める（FR-88）。
	Writable bool `json:"writable,omitempty"`
}

// Validate は、Title が空でないことを検査する。本体は、誤りのある params のリクエストに JSON-RPC のエラーで応答する。
func (p *BufferCreateParams) Validate() error {
	if p.Title == "" {
		return errors.New("title が空")
	}
	return nil
}

// BufferCreateResult は、buffer/create の result である。
type BufferCreateResult struct {
	// Buffer は、作ったバッファの番号である。
	Buffer BufferID `json:"buffer"`
	// Version は、作った時点のバージョン番号である。
	Version Version `json:"version"`
}

// DiffStartParams は、diff/start の params である。
//
// 本体は、A と B の 2 つのバッファを、A を左（diff の A の側）にして見比べる（基本設計 6.8）。
// 2 つのバッファは別々の編集の client のタブでなければならない。
//
//   - どちらも別々の client のタブなら、その場で見比べを始める
//   - どちらも同じ client のタブなら、誤りにする（同じ client の 2 つのタブは同時に画面に出ない）
//   - 片方だけがタブなら、そのタブの client のペインを端末多重化ソフトの層で分割し、新しい編集の client を起動して、
//     もう片方のバッファをそのタブにしてから見比べを始める（:diffsplit と同じ）。応答は分割を頼んだ時点で返す
//   - どちらもタブでなければ、誤りにする
//
// 端末多重化ソフトの外（FR-60）では、分割できないので誤りにする。
type DiffStartParams struct {
	// A は、見比べの左の側のバッファの番号である。
	A BufferID `json:"a"`
	// B は、見比べの右の側のバッファの番号である。
	B BufferID `json:"b"`
}

// Validate は、A と B が違うバッファであることを検査する。
func (p *DiffStartParams) Validate() error {
	if p.A == p.B {
		return errors.New("a と b が同じバッファ")
	}
	return nil
}

// DiffStartResult は、diff/start の result である。
type DiffStartResult struct {
	// Split は、ペインを分割して新しい編集の client を起動したことを表す。
	// true のとき、見比べは新しい client が繋いだ後に始まる（繋がなければ始まらない）。false なら応答の時点で始まっている。
	Split bool `json:"split,omitempty"`
}
