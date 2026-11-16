package command

import (
	"errors"

	"github.com/piplupflipper/modal-editor/internal/buffer"
	"github.com/piplupflipper/modal-editor/internal/command/ex"
	"github.com/piplupflipper/modal-editor/internal/editcmd"
)

// ErrStale は、保留した操作（Deferred）を適用しようとしたが、問い合わせたときからバッファかカーソルかモードが変わっていたので、
// 破棄したことを表す（FR-18）。
var ErrStale = errors.New("command: deferred result is stale")

// Deferred は、拡張機能が結果を保留した（ErrDeferred）操作である（FR-18）。
// 実行しようとした値と、問い合わせたときのバージョン番号とカーソルを持つ。
type Deferred struct {
	// Command は、実行しようとした編集コマンドの値である。
	Command editcmd.Command
	// Version は、問い合わせたときのバッファのバージョン番号である。
	Version uint64
	// Cursor は、問い合わせたとき（値を実行する前）のカーソルである。
	Cursor buffer.Position

	// mode と anchor は、値を実行する前のモードとビジュアルモードの選択の端である。
	// ビジュアルモードのオペレーターは、選択を抜けてから問い合わせるので、適用のときに選択を戻す。
	mode   Mode
	anchor buffer.Position
	want   int
	// exCommand は、コマンドラインから呼んだ拡張機能のコマンドを保留したときの動作である。このとき Command は使わない。
	exCommand *ex.Action
}

// ApplyDeferred は、保留した操作 d を、届いた結果で実行し直す（FR-18、基本設計 5.4 の「期限が 0」）。
//
// 適用するのは、バッファのバージョン番号、カーソル、モードが、問い合わせたときと同じ場合だけである。
// 違えば何もせずに ErrStale を Result.Err で返す（結果を破棄する）。
// ビジュアルモードのオペレーターは、問い合わせの後にノーマルモードへ戻っているので、ノーマルモードであることを確かめ、選択を戻してから実行する。
//
// 実行は Feed と同じ経路を通るので、ドットリピートへの記録、オペレーターの後のカーソル、インサートモードへの移行も同じになる。
// ext は、この実行の間だけ State.Extensions の代わりに使う問い合わせ先で、保留した問い合わせには届いた結果を返す。
// 他の問い合わせ（拡張機能のテキストオブジェクトと拡張機能のオペレーターの組で、後の方）は、ふだんの実装へ回してよい。
// ext がまた ErrDeferred を返した場合は、新しい Deferred が Result に入る。
//
// req: FR-18
func (s *State) ApplyDeferred(d Deferred, ext Extensions) Result {
	s.res = Result{}
	visualOp := d.mode.visual() && d.Command.Kind == editcmd.KindOperator
	ok := s.buf.Version() == d.Version && s.cursor == d.Cursor
	switch {
	case !ok:
	case visualOp:
		ok = s.mode == ModeNormal
	case d.mode.visual():
		ok = s.mode == d.mode && s.visual.anchor == d.anchor
	default:
		ok = s.mode == ModeNormal
	}
	if !ok {
		s.fail(ErrStale)
		return s.take()
	}
	if visualOp {
		s.mode = d.mode
		s.visual.anchor = d.anchor
		s.want = d.want
		s.parser.SetVisual(true)
	}
	saved := s.Extensions
	s.Extensions = ext
	defer func() { s.Extensions = saved }()
	if d.exCommand != nil {
		s.runExtCommand(*d.exCommand)
	} else {
		s.run(d.Command)
	}
	return s.take()
}
