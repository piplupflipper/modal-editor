package command

import (
	"slices"

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
// モードは変えずに実行する（Vim 9.1 の <Cmd> の割り当てと同じ）。
//
//   - ノーマルモード: 回数とレジスターは使わない（書ける名前のコマンドは、どれも範囲を取らない）
//   - ビジュアルモード: 選択を残したまま、範囲を付けずに実行する。: で打ったときのように '<,'> は入れない
//     （書ける名前のコマンドはどれも範囲を取らず、Vim と同じく E481 になるため）
//   - インサートモード: インサートモードのまま実行し、入力を区切らない（ifoo<C-l>bar<Esc> の u は foobar を一度に戻し、. は foobar を入れる）
//
// コマンドが別のバッファへ移ったとき（:tab.new）のモードの扱いは、呼び出し側が SuspendMode と ResumeMode で行う。
// ドットリピートには記録しない（コマンドラインの Ex コマンドと、テキストを変えない操作と同じ）。
// マクロの記録は、操作と同じく打ったキーを記録し、再生ではキーから同じ割り当てを引き直して実行する。
//
// req: FR-154
func (s *State) keymapCommand(line string) {
	s.runCommandLine(line)
}

// ModeCarry は、別のバッファへ移るときに、移る前のバッファのインサートモードの入力を、移った先のバッファへ引き継ぐ値である
// （SuspendMode と ResumeMode。FR-154）。ゼロ値は「引き継ぐ入力が無い」を表す。
type ModeCarry struct {
	ok     bool
	cmd    editcmd.Command
	text   []editcmd.InsertInput
	repeat int
	moved  bool
}

// SuspendMode は、カーソルのあるバッファがこの State から別のバッファへ移るときに、このバッファのモードを終え、
// インサートモードの入力を移った先へ引き継ぐ値を返す（FR-154）。呼び出し側（internal/editor）は、移るたびに、
// TakeOver の前にこれを呼び、移った先の State の ResumeMode へ渡す。
// ノーマルモードとコマンドラインモードでは何もせずにゼロ値を返す。
//
// Vim 9.1 はモードをエディタ全体に 1 つ持ち、keymap の Ex コマンド（<Cmd> の割り当て）でタブを移ると次のようになる
// （2026-11-20 に vim -Nu NONE -n --not-a-term で確かめた）。
//
//   - インサートモード: 移った先のタブでインサートモードが続き、元のバッファはノーマルモードに戻る。
//     元のバッファの入力はアンドゥの 1 単位として閉じ、カーソルはノーマルモードの位置に丸めるだけにする（<Esc> と違って左へ 1 文字戻さない）。
//     回数による繰り返しとドットリピートの記録は、移った先で入力を終えたときに、移る前の入力と合わせて行う
//     （3ifoo<C-x>bar<Esc> は、元のバッファが fooabcd でカーソルは a、移った先が barfoobarfoobar、. のレジスターは foobar）
//   - ビジュアルモード: 選択を抜け（'< と '> を記録する）、移った先はノーマルモードである
//
// 同時の入力（FR-141）は、ここで終える。
//
// req: FR-154
func (s *State) SuspendMode() ModeCarry {
	switch {
	case s.mode.visual():
		s.exitVisual()
		return ModeCarry{}
	case s.mode != ModeInsert:
		return ModeCarry{}
	}
	s.endSites()
	in := s.insert
	for range in.extraGroups {
		s.buf.EndUndoGroup()
	}
	if !in.moved {
		s.buf.EndUndoGroup()
	}
	s.mode = ModeNormal
	s.insert = insertState{}
	s.setCursorNormal(s.cursor)
	return ModeCarry{ok: true, cmd: in.cmd, text: slices.Clone(in.text), repeat: in.repeat, moved: in.moved}
}

// ResumeMode は、SuspendMode で終えた入力 c を引き継いで、このバッファのカーソルの位置からインサートモードを続ける（FR-154）。
// c がゼロ値なら何もしない。ビジュアルモードなら選択を抜けてから続け、コマンドラインモードでは続けない。
// 入力を終えたときは、引き継いだ入力と、このバッファで入力したものを合わせて、回数の繰り返しとドットリピートの記録を行う。
//
// req: FR-154
func (s *State) ResumeMode(c ModeCarry) {
	if !c.ok {
		return
	}
	if s.mode.visual() {
		s.exitVisual()
	}
	if s.mode != ModeNormal {
		return
	}
	s.mode = ModeInsert
	s.cursor = s.clampPos(s.cursor)
	s.insert = insertState{cmd: c.cmd, text: c.text, repeat: c.repeat, moved: c.moved, lines: 1}
	if !c.moved {
		s.buf.BeginUndoGroup()
	}
}

// setCursorAfterEx は、Ex コマンドを実行した後に、カーソルを p に置く。
// インサートモードとビジュアルモード（keymap の Ex コマンドを呼んだとき。FR-154）では、カーソルを行末の位置にも置けるので、
// バッファの中に丸めるだけにする。それ以外のモードは setCursorNormal と同じである。
func (s *State) setCursorAfterEx(p buffer.Position) {
	if s.mode != ModeInsert && !s.mode.visual() {
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
