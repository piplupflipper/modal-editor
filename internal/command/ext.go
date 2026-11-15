package command

import (
	"errors"

	"github.com/piplupflipper/modal-editor/internal/buffer"
	"github.com/piplupflipper/modal-editor/internal/command/target"
	"github.com/piplupflipper/modal-editor/internal/input"
)

// ErrNoExtensions は、拡張機能の操作を実行しようとしたが、State.Extensions が nil であることを表す。
var ErrNoExtensions = errors.New("command: no extension host")

// Extensions は、拡張機能が提供する操作（提供元が拡張機能の input.Ref）を問い合わせる口である（基本設計 3 章、5.4）。
//
// 問い合わせの実装（exthost への RPC、期限の扱い、拡張機能の状態）は、呼び出し側（internal/editor）が差し込む。
// このパッケージは、結果が返るまで待つ同期の呼び出しとして扱う。
// 期限を超えた、拡張機能が動いていない、といった失敗はエラーで返す。その場合、操作は何もせずに失敗する（基本設計 8 章）。
//
// ドットリピートとマクロの再生では、記録した Ref と引数で、同じ拡張機能へその都度問い合わせ直す（FR-12）。
// このため、拡張機能の側に繰り返しのための処理は要らない。
type Extensions interface {
	// Motion は、モーションの移動先を問い合わせる（motion/resolve）。req は本体のモーションと同じ形で渡す。
	Motion(ref input.Ref, b *buffer.Buffer, req target.Request) (target.Result, error)
	// TextObject は、テキストオブジェクトの範囲を問い合わせる（textObject/resolve）。
	TextObject(ref input.Ref, b *buffer.Buffer, req target.Request) (target.Span, error)
	// Operator は、範囲に対するオペレーターの編集を問い合わせる（operator/apply）。
	// 返すトランザクションの BaseVersion は、問い合わせたときの b.Version() である必要がある（FR-15）。
	Operator(ref input.Ref, b *buffer.Buffer, q OperatorQuery) (buffer.Transaction, error)
	// Action は、対象を取らない操作を実行する（command/execute）。編集しない操作は、Edits が空のトランザクションを返す。
	Action(ref input.Ref, b *buffer.Buffer, q ActionQuery) (buffer.Transaction, error)
}

// OperatorQuery は、拡張機能のオペレーターへ渡す引数である。
type OperatorQuery struct {
	// Span は、オペレーターが働く範囲である。
	Span target.Span
	// Cursor は、オペレーターを入力したときのカーソルである。
	Cursor buffer.Position
	// Count は、回数である。
	Count int
	// Char は、対象の後に取った文字の引数である（surround の ysiw) の ')'）。取らなければ 0。
	Char rune
	// Register は、指定したレジスターの名前である。省略したときは 0。
	Register rune
}

// ActionQuery は、拡張機能の対象を取らない操作へ渡す引数である。
type ActionQuery struct {
	Cursor     buffer.Position
	Count      int
	CountGiven bool
	Char       rune
	Register   rune
}

// resolveMotion は、モーションを本体の表か拡張機能で解決する。f、F、t、T の検索は、失敗しても ; と , のために記録する。
func (s *State) resolveMotion(t input.Target, cmd input.Command, op target.Operator) (target.Result, error) {
	req := s.request(cmd, op)
	var (
		res target.Result
		err error
	)
	if t.Ref.Builtin() {
		f, ok := target.LookupMotion(t.Ref.ID)
		if !ok {
			return target.Result{}, failf("unknown motion %q", t.Ref.ID)
		}
		res, err = f(s.buf, req)
	} else {
		if s.Extensions == nil {
			return target.Result{}, ErrNoExtensions
		}
		res, err = s.Extensions.Motion(t.Ref, s.buf, req)
	}
	if res.Find.Char != 0 && t.Ref.ID != "find-repeat" && t.Ref.ID != "find-repeat-reverse" {
		s.lastFind = res.Find
	}
	if errors.Is(err, target.ErrNoTarget) {
		return res, failf("%v", err)
	}
	return res, err
}

// resolveTextObject は、テキストオブジェクトを本体の表か拡張機能で解決する。
func (s *State) resolveTextObject(t input.Target, cmd input.Command, from buffer.Position) (target.Span, error) {
	req := s.request(cmd, target.NoOperator)
	req.Cursor = from
	var (
		sp  target.Span
		err error
	)
	if t.Ref.Builtin() {
		f, ok := target.LookupTextObject(t.Ref.ID)
		if !ok {
			return target.Span{}, failf("unknown text object %q", t.Ref.ID)
		}
		sp, err = f(s.buf, req)
	} else {
		if s.Extensions == nil {
			return target.Span{}, ErrNoExtensions
		}
		sp, err = s.Extensions.TextObject(t.Ref, s.buf, req)
	}
	if errors.Is(err, target.ErrNoTarget) {
		return sp, failf("%v", err)
	}
	return sp, err
}

// request は、モーションとテキストオブジェクトへの入力を作る。
func (s *State) request(cmd input.Command, op target.Operator) target.Request {
	return target.Request{
		Cursor:     s.cursor,
		Count:      cmd.Count,
		CountGiven: cmd.CountGiven,
		Char:       cmd.Target.Char,
		Want:       s.want,
		Columns:    s.Columns,
		Last:       s.lastFind,
		Operator:   op,
	}
}
