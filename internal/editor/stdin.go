package editor

import "github.com/piplupflipper/vi-imitation/internal/buffer"

// newStdinBuffer は、client が起動の引数の - で標準入力から読んだ text を中身にする、名前の無いバッファを作る（FR-158）。
// 標準入力のためだけのバッファの種類は作らず、:tab.new などと同じ名前の無いバッファにする。
// :w ファイル で名前を付けて保存でき、:set filetype= で種類を決められる。
//
// 中身は、ファイルを読むときと同じく buffer.New で行に分ける。末尾の改行の有り無しを記録し、正しくない UTF-8 のバイトはそのまま持つ（基本設計 4.1）。
// readOnly（-R）なら編集を禁止する（FR-119）。
//
// Vim 9.1 と同じく、編集を禁止せず、中身が空（行が 1 つで、その行が空）でなければ、未保存の変更がある扱いにする（:q が E37 で断られる）。
// -R のときと空のときは、Vim 9.1 と同じく変更の無い扱いにする（2026-11-20 に vim -Nu NONE -n で、-R - と空の入力と改行 1 つの入力の &modified が 0 であることを確かめた）。
// 未保存の変更はアンドゥの履歴に足さないので、u で読んだ中身は消えない（Vim 9.1 も "Already at oldest change"）。
//
// req: FR-158
func (e *Editor) newStdinBuffer(text string, readOnly bool) *bufferState {
	b := buffer.New(text)
	b.SetFixEndOfLine(e.fixEOL)
	switch {
	case readOnly:
		b.SetReadOnly(true)
	case b.LineCount() > 1 || b.Line(0) != "":
		b.MarkUnsaved()
	}
	return e.newBuffer(b, "", "")
}

// OpenStdin は、起動の引数の - で標準入力から読んだ text を、名前の無いバッファとして開き、カーソルのあるバッファにする（FR-158。newStdinBuffer）。
// readOnly なら編集を禁止した状態で開く（FR-119）。
// OpenWith と同じく、ジャンプリストと裏のバッファには記録しない。Run の前に呼ぶ。
//
// req: FR-158
func (e *Editor) OpenStdin(text string, readOnly bool) {
	e.enter(e.newStdinBuffer(text, readOnly))
	e.jumps, e.jumpIdx, e.alt = nil, 0, nil
}
