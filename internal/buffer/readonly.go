package buffer

import "errors"

// ErrReadOnly は、編集を禁止したバッファ（ReadOnly）へ、利用者の操作か拡張機能の編集を適用しようとしたことを表す（FR-85、FR-118）。
//
// 文言は、利用者に 1 行で示すことを前提にした日本語にし、Vim の 'modifiable' が切れたときのエラー番号 E21 を付ける（基本設計 4.6）。
// 呼び出し側は、包まずにそのまま示してよい。
var ErrReadOnly = errors.New("E21: 編集を禁止したバッファは変更できません")

// ReadOnly は、編集を禁止しているかを返す（基本設計 4.6。FR-85）。
// New と Read で作ったバッファは禁止していない。
func (b *Buffer) ReadOnly() bool { return b.readOnly }

// SetReadOnly は、編集の禁止を切り替える（FR-85、FR-86）。
//
// 禁止は、モードではなくバッファごとの真偽値である。
// 禁止中は、利用者の操作と拡張機能の編集の入り口である Apply、Undo、Redo がバッファを変えずに断る。
// 本体がファイルを読み直す ApplyReload は断らない（外での変更の読み直しは、利用者の編集ではないため）。
// 保存（WriteTo、WriteFile、MarkSaved）も断らない（禁止するのは編集で、保存ではない）。
//
// 切り替えは、テキスト、バージョン番号、アンドゥの履歴、保存済みの記録を変えない。
// 禁止の前のアンドゥの履歴は残るので、禁止を解けばアンドゥで戻れる。
//
// req: FR-85
func (b *Buffer) SetReadOnly(on bool) { b.readOnly = on }
