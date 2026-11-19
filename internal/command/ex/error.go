package ex

import "fmt"

// Error は、Ex コマンドの解析か実行の誤りである。
// 文言は利用者に表示するので日本語で書き、先頭に Vim のエラー番号を付ける。
type Error struct {
	// Code は、Vim のエラー番号（"E37" など）である。Vim で番号の無い通知に当たるものは空である。
	Code string
	// Pos は、誤りを見つけた位置の、コマンドラインの中のバイト位置（0 始まり）である。
	// 位置に結びつかない誤り（未保存の変更など）では、コマンド名の位置を指す。
	Pos int
	// Msg は、誤りの内容である。
	Msg string
}

// Error は、"E37: 最後の変更が保存されていません" の形で誤りを返す。
func (e *Error) Error() string {
	if e.Code == "" {
		return e.Msg
	}
	return e.Code + ": " + e.Msg
}

// Vim のエラー番号。
const (
	codeNoWrite          = "E37"  // 未保存の変更がある
	codeMarkNotSet       = "E20"  // マークが設定されていない
	codeReadOnly         = "E21"  // 編集を禁止したバッファを変えようとした（Vim の 'modifiable' が切れているとき）
	codeNoPrevRegexp     = "E35"  // 直前のパターンが無い
	codeInvalidRange     = "E16"  // 範囲がバッファの外にある
	codeNoNestedGlobal   = "E147" // :g の入れ子
	codeBadDelimiter     = "E146" // 区切り文字に英数字などを使った
	codeNotAvailable     = "E319" // このエディタでは使えない機能
	codeInvalidPattern   = "E383" // 正規表現が正しくない
	codeNoBang           = "E477" // ! を付けられないコマンド
	codeNoRange          = "E481" // 範囲を付けられないコマンド
	codeTrailing         = "E488" // 余計な文字が続く
	codeNotFound         = "E486" // パターンが見つからない
	codeNotEditorCommand = "E492" // コマンドではない
	codeBackwardsRange   = "E493" // 逆順の範囲
	codePositiveCount    = "E939" // 回数が 1 以上でない
	codeInvalidArgument  = "E475" // 引数が正しくない
	codeArgRequired      = "E471" // 引数が要る
)

// CodeNoWrite は、未保存の変更があるので :q や :e を実行できないことを表すエラー番号である。
// 呼び出し側は、errors.As で *Error を取り出し、Code と比べて判定できる。
const CodeNoWrite = codeNoWrite

// CodeBackwardsRange は、逆順の範囲（:4,2d）で確認が要ることを表すエラー番号である。
// このとき Result.Confirm が nil でなければ、確認を得て Confirm.Resume で続きを実行できる。
const CodeBackwardsRange = codeBackwardsRange

// msgReadOnly は、E21 の文言である。buffer.ErrReadOnly の文言のエラー番号の後と同じにする（基本設計 4.6）。
const msgReadOnly = "編集を禁止したバッファは変更できません"

func errAt(pos int, code, format string, args ...any) *Error {
	return &Error{Code: code, Pos: pos, Msg: fmt.Sprintf(format, args...)}
}
