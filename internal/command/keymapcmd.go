package command

import (
	"github.com/piplupflipper/vi-imitation/internal/buffer"
	"github.com/piplupflipper/vi-imitation/internal/editcmd"
)

// ExCommandExtension は、設定の keymap の右辺に書いた Ex コマンド（{ "command": "<正規の名前>", "args": "<引数>" }。FR-154）を
// 割り当てた操作の Ref.Extension に入れる値である。Ref.ID には、実行するコマンドラインの文字列（ex.KeymapCommandLine が作る）を入れる。
// 呼び出し側（internal/editor）が、この Ref の操作を操作の表（input.Keymap）に登録し、State はキーで呼ばれたときに keymapCommand で実行する。
// 値は空白を含むので、宣言ファイルの拡張機能の ID（空白を含めない。ext.Manifest.Validate）と editcmd.ProviderExtension と重ならない。
const ExCommandExtension = " excommand"

// isKeymapCommand は、ref が設定の keymap に書いた Ex コマンドの操作であるかを返す。
func isKeymapCommand(ref editcmd.Ref) bool { return ref.Extension == ExCommandExtension }

// keymapCommand は、設定の keymap に書いた Ex コマンドのコマンドライン line を実行する（FR-154）。
//
// コマンドラインで打ったときと同じ口（runCommandLine。ex.Execute と applyExResult）を通す。
// 違うのは、コマンドラインモードへ移らず（コマンドラインを出さない）、打った行として残さないことだけである
// （本体にはまだコマンドの履歴が無いので、残す先も無い）。
// 誤りは、: で打ったときと同じ文言で Result.Err に入り、メッセージの行に出る。
//
// モードごとの扱いは次のとおりにする。
//
//   - ノーマルモード: 回数とレジスターは使わない（書ける名前のコマンドは、どれも範囲を取らない）
//   - ビジュアルモード: 選択を抜けてから（'< と '> は記録する）、範囲を付けずに実行する。
//     Vim の :'<,'> の形（: が範囲を入れる）にしないのは、書ける名前のコマンドがどれも範囲を取らず、
//     Vim と同じく E481 になって、ビジュアルモードの割り当てが使えないためである
//   - インサートモード: インサートモードのまま実行し、入力を区切らない（Vim 9.1 の <Cmd> の割り当てと同じく、
//     ifoo<C-l>bar<Esc> の u は foobar を一度に戻し、. は foobar を入れる）
//
// ドットリピートには記録しない（コマンドラインの Ex コマンドと、テキストを変えない操作と同じ）。
// マクロの記録は、操作と同じく打ったキーを記録し、再生ではキーから同じ割り当てを引き直して実行する。
//
// req: FR-154
func (s *State) keymapCommand(line string) {
	if s.mode.visual() {
		s.exitVisual()
	}
	s.runCommandLine(line)
}

// setCursorAfterEx は、Ex コマンドを実行した後に、カーソルを p に置く。
// インサートモード（keymap の Ex コマンドをインサートモードで呼んだとき。FR-154）では、カーソルを行末の位置にも置けるので、
// バッファの中に丸めるだけにする。それ以外のモードは setCursorNormal と同じである。
func (s *State) setCursorAfterEx(p buffer.Position) {
	if s.mode != ModeInsert {
		s.setCursorNormal(p)
		return
	}
	old := s.cursor
	s.cursor = s.clampPos(p)
	if s.cursor != old {
		s.followCursor()
		s.updateWant()
	}
}
