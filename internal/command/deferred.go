package command

import (
	"errors"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
	"github.com/piplupflipper/vi-imitation/internal/command/ex"
	"github.com/piplupflipper/vi-imitation/internal/command/target"
	"github.com/piplupflipper/vi-imitation/internal/editcmd"
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

	// mode と anchor と head は、値を実行する前のモードとビジュアルモードの選択の両端である。
	// ビジュアルモードのオペレーターは、選択を抜けてから問い合わせるので、適用のときに選択を戻す。
	mode         Mode
	anchor, head buffer.Position
	want         int
	// exCommand は、コマンドラインから呼んだ拡張機能のコマンドを保留したときの動作である。このとき Command は使わない。
	exCommand *ex.Action
	// replay は、再生中の問い合わせの結果を待つために止めた（ErrQueryPending）ときの、続けるための控えである（ResumeReplay）。
	replay *replaySnapshot
}

// replaySnapshot は、再生を止めたときの控えである（suspendQuery が作り、ResumeReplay が使う）。
type replaySnapshot struct {
	// undoTip は、止めたときのアンドゥの履歴の末尾の識別子である。止める前の再生が単位を作っていなければ 0 で、つながない。
	// undoStart は、その再生を始めたときの末尾の識別子である（State.playUndoStart）。
	undoTip, undoStart uint64
	// repeat は、止めたときのドットリピートの値などである。
	repeat repeatState
}

// repeatState は、client の間で引き継ぐ状態（TakeOverShared）のうち、レジスターを除いたものである。
// 再生を止めている間に別の client の操作で替わっても、再生の続きは止めたときの値で続ける（ResumeReplay）。
// レジスターは、client の間で共有するもの（FR-58）として引き継いだ値のまま使う。
type repeatState struct {
	lastPattern    string
	searchBackward bool
	lastSubstitute *ex.Substitution
	lastFind       target.Find
	dot            *editcmd.Command
	lastChange     *editcmd.Command
	dotVisual      visualExtent
	lastPlayed     rune
}

// repeatState は、s の repeatState を返す。
func (s *State) repeatState() repeatState {
	return repeatState{
		lastPattern: s.Ex.LastPattern, searchBackward: s.searchBackward, lastSubstitute: s.Ex.LastSubstitute,
		lastFind: s.lastFind, dot: s.dot, lastChange: s.lastChange, dotVisual: s.dotVisual, lastPlayed: s.lastPlayed,
	}
}

// setRepeatState は、r を s に戻す。
func (s *State) setRepeatState(r repeatState) {
	s.Ex.LastPattern, s.searchBackward, s.Ex.LastSubstitute = r.lastPattern, r.searchBackward, r.lastSubstitute
	s.lastFind, s.dot, s.lastChange, s.dotVisual, s.lastPlayed = r.lastFind, r.dot, r.lastChange, r.dotVisual, r.lastPlayed
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
	if !s.deferredReady(d) {
		s.fail(ErrStale)
		return s.take()
	}
	saved := s.Extensions
	s.Extensions = ext
	defer func() { s.Extensions = saved }()
	s.rerunDeferred(d)
	return s.take()
}

// deferredReady は、保留した操作 d を実行し直せるか（バッファのバージョン番号、カーソル、モードが保留したときと同じか）を返す。
func (s *State) deferredReady(d Deferred) bool {
	if s.buf.Version() != d.Version || s.cursor != d.Cursor {
		return false
	}
	switch {
	case d.visualOperator():
		// ビジュアルモードのオペレーターは、問い合わせる前に選択を抜けている。
		return s.mode == ModeNormal
	case d.mode.visual():
		return s.mode == d.mode && s.visual.anchor == d.anchor && s.visual.head == d.head
	default:
		return s.mode == ModeNormal
	}
}

// visualOperator は、d がビジュアルモードのオペレーターであるかを返す。
func (d Deferred) visualOperator() bool {
	return d.mode.visual() && d.Command.Kind == editcmd.KindOperator
}

// rerunDeferred は、保留した操作 d を、State.Extensions で実行し直す。deferredReady を確かめてから呼ぶ。
// ビジュアルモードのオペレーターは、選択を戻してから実行する。
func (s *State) rerunDeferred(d Deferred) {
	if d.visualOperator() {
		s.mode = d.mode
		s.visual.anchor, s.visual.head = d.anchor, d.head
		s.want = d.want
		s.parser.SetVisual(true)
	}
	if d.exCommand != nil {
		s.runExtCommand(*d.exCommand)
	} else {
		s.run(d.Command)
	}
}
