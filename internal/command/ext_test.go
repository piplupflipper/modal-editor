package command

import (
	"errors"
	"strings"
	"testing"

	"github.com/piplupflipper/modal-editor/internal/buffer"
	"github.com/piplupflipper/modal-editor/internal/command/target"
	"github.com/piplupflipper/modal-editor/internal/input"
)

// fakeExt は、拡張機能の問い合わせを記録し、決まった結果を返す。
//
//	surround.add（オペレーター）  範囲を、対象の後の文字の括弧で囲む
//	jump.x（モーション）          次の 'x' へ移る
//	line.inner（テキストオブジェクト）行の内容全体
//	dup.line（操作）              行を下に複製する
type fakeExt struct {
	calls   []string
	stale   bool // true なら古いバージョン番号のトランザクションを返す
	failing bool
}

func (f *fakeExt) Motion(ref input.Ref, b *buffer.Buffer, req target.Request) (target.Result, error) {
	f.calls = append(f.calls, "motion "+ref.ID)
	line := b.Line(req.Cursor.Line)
	i := strings.IndexByte(line[req.Cursor.Byte+1:], 'x')
	if i < 0 {
		return target.Result{}, target.ErrNoTarget
	}
	return target.Result{Pos: pos(req.Cursor.Line, req.Cursor.Byte+1+i), Kind: target.Inclusive}, nil
}

func (f *fakeExt) TextObject(ref input.Ref, b *buffer.Buffer, req target.Request) (target.Span, error) {
	f.calls = append(f.calls, "textObject "+ref.ID)
	n := len(b.Line(req.Cursor.Line))
	return target.Span{Start: pos(req.Cursor.Line, 0), End: pos(req.Cursor.Line, n), Kind: target.Exclusive}, nil
}

func (f *fakeExt) Operator(ref input.Ref, b *buffer.Buffer, q OperatorQuery) (buffer.Transaction, error) {
	f.calls = append(f.calls, "operator "+ref.ID+" "+string(q.Char))
	if f.failing {
		return buffer.Transaction{}, errors.New("timeout")
	}
	r := q.Span.Range(b)
	open := map[rune]string{')': "(", ']': "["}[q.Char]
	tx := buffer.Transaction{BaseVersion: b.Version(), Edits: []buffer.Edit{
		{Range: emptyRange(r.End), Text: string(q.Char)},
		{Range: emptyRange(r.Start), Text: open},
	}}
	if f.stale {
		tx.BaseVersion--
	}
	return tx, nil
}

func (f *fakeExt) Action(ref input.Ref, b *buffer.Buffer, q ActionQuery) (buffer.Transaction, error) {
	f.calls = append(f.calls, "action "+ref.ID)
	l := q.Cursor.Line
	return buffer.Transaction{BaseVersion: b.Version(), Edits: []buffer.Edit{
		{Range: emptyRange(pos(l, len(b.Line(l)))), Text: "\n" + b.Line(l)},
	}}, nil
}

// extKeymap は、fakeExt の操作を登録したキーマップを返す。
func extKeymap(t *testing.T) *input.Keymap {
	t.Helper()
	km := input.DefaultKeymap()
	for _, b := range []input.Binding{
		{Kind: input.BindOperator, Keys: input.MustParseKeys("ys"), Ref: input.Ref{Extension: "surround", ID: "add"}, TakesChar: true},
		{Kind: input.BindMotion, Keys: input.MustParseKeys("gx"), Ref: input.Ref{Extension: "jump", ID: "x"}},
		{Kind: input.BindTextObject, Keys: input.MustParseKeys("il"), Ref: input.Ref{Extension: "line", ID: "inner"}},
		{Kind: input.BindAction, Keys: input.MustParseKeys("gD"), Ref: input.Ref{Extension: "dup", ID: "line"}},
	} {
		if err := km.Bind(b); err != nil {
			t.Fatal(err)
		}
	}
	return km
}

// 拡張機能の操作は、ドットリピートとマクロの再生のたびに、同じ拡張機能へ同じ引数で問い合わせ直す。
//
// req: FR-06, FR-12
func TestExtensionRepeat(t *testing.T) {
	tests := []struct {
		keys  string
		want  string
		calls []string
	}{
		{"ysiw)W.", "(ab) (cd) ex f", []string{"operator add )", "operator add )"}},
		{"ysgx]", "[ab cd ex] f", []string{"motion x", "operator add ]"}},
		{"ysil)", "(ab cd ex f)", []string{"textObject inner", "operator add )"}},
		{"qaysiw)Wq@a", "(ab) (cd) ex f", []string{"operator add )", "operator add )"}},
		{"gDj.", "ab cd ex f\nab cd ex f\nab cd ex f", []string{"action line", "action line"}},
		{"ysiw)u", "ab cd ex f", []string{"operator add )"}},
	}
	for _, tt := range tests {
		ext := &fakeExt{}
		s := New(newBuffer("ab cd ex f"), extKeymap(t))
		s.Extensions = ext
		if err := errOf(feedKeys(s, tt.keys)); err != nil {
			t.Errorf("%s: err = %v", tt.keys, err)
		}
		if got := s.buf.Text(); got != tt.want {
			t.Errorf("%s: text = %q, want %q", tt.keys, got, tt.want)
		}
		if strings.Join(ext.calls, ",") != strings.Join(tt.calls, ",") {
			t.Errorf("%s: calls = %q, want %q", tt.keys, ext.calls, tt.calls)
		}
	}
}

// 拡張機能の問い合わせの失敗、古いバージョン番号、問い合わせ先が無い場合は、バッファを変えずに失敗する。
//
// req: FR-12
func TestExtensionFailure(t *testing.T) {
	for _, tt := range []struct {
		name string
		ext  Extensions
		want error
	}{
		{"失敗", &fakeExt{failing: true}, nil},
		{"古いバージョン番号", &fakeExt{stale: true}, buffer.ErrVersionMismatch},
		{"問い合わせ先が無い", nil, ErrNoExtensions},
	} {
		s := New(newBuffer("ab cd"), extKeymap(t))
		s.Extensions = tt.ext
		err := errOf(feedKeys(s, "ysiw)"))
		if err == nil || tt.want != nil && !errors.Is(err, tt.want) {
			t.Errorf("%s: err = %v, want %v", tt.name, err, tt.want)
		}
		if got := s.buf.Text(); got != "ab cd" {
			t.Errorf("%s: text = %q", tt.name, got)
		}
		// 失敗した操作はドットリピートに残らない。
		if err := errOf(feedKeys(s, ".")); !errors.Is(err, ErrFailed) {
			t.Errorf("%s: . err = %v", tt.name, err)
		}
	}
}
