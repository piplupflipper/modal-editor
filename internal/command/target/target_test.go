package target

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/piplupflipper/modal-editor/internal/buffer"
)

// parse は、"|" でカーソルの位置を示したテキストから、"|" を除いたバッファと、カーソルの位置を返す。
func parse(t *testing.T, s string) (*buffer.Buffer, buffer.Position) {
	t.Helper()
	before, after, ok := strings.Cut(s, "|")
	if !ok || strings.Contains(after, "|") {
		t.Fatalf("text %q must have exactly one cursor marker", s)
	}
	p := buffer.Position{Line: strings.Count(before, "\n"), Byte: len(before) - strings.LastIndexByte(before, '\n') - 1}
	return buffer.New(before + after), p
}

// mark は、バッファのテキストに、位置 p を表す "|" を入れて返す。
func mark(b *buffer.Buffer, p buffer.Position) string {
	lines := make([]string, b.LineCount())
	for i := range lines {
		lines[i] = b.Line(i)
	}
	lines[p.Line] = lines[p.Line][:p.Byte] + "|" + lines[p.Line][p.Byte:]
	return strings.Join(lines, "\n")
}

func TestLookupNames(t *testing.T) {
	// issue #19 の対象。#9 の裁定で増減しうる。
	wantMotions := strings.Fields("h j k l w b e W B E 0 ^ $ gg G f F t T ; , % { }")
	wantObjects := strings.Fields(`iw aw iW aW i" a" i' a' i( a( i[ a[ i{ a{ ip ap ib ab iB aB`)
	for _, name := range wantMotions {
		if _, ok := LookupMotion(name); !ok {
			t.Errorf("LookupMotion(%q) not found", name)
		}
	}
	for _, name := range wantObjects {
		if _, ok := LookupTextObject(name); !ok {
			t.Errorf("LookupTextObject(%q) not found", name)
		}
	}
	if _, ok := LookupMotion("x"); ok {
		t.Error(`LookupMotion("x") found`)
	}
	if _, ok := LookupTextObject("iz"); ok {
		t.Error(`LookupTextObject("iz") found`)
	}
	if got := MotionNames(); len(got) != len(wantMotions) || !slices.IsSorted(got) {
		t.Errorf("MotionNames() = %q", got)
	}
	if got := TextObjectNames(); !slices.IsSorted(got) {
		t.Errorf("TextObjectNames() = %q is not sorted", got)
	}
}

func TestInvalidRequest(t *testing.T) {
	b := buffer.New("あい\nb")
	w, _ := LookupMotion("w")
	iw, _ := LookupTextObject("iw")
	tests := []Request{
		{Cursor: buffer.Position{Line: 2}},
		{Cursor: buffer.Position{Line: -1}},
		{Cursor: buffer.Position{Line: 0, Byte: 1}}, // あ の途中
		{Cursor: buffer.Position{Line: 1, Byte: 2}},
		{Count: -1},
	}
	for _, req := range tests {
		if _, err := w(b, req); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("w(%+v) error = %v, want ErrInvalidRequest", req, err)
		}
		if _, err := iw(b, req); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("iw(%+v) error = %v, want ErrInvalidRequest", req, err)
		}
	}
}

func TestKindString(t *testing.T) {
	for k, want := range map[Kind]string{Exclusive: "exclusive", Inclusive: "inclusive", Linewise: "linewise", Kind(9): "Kind(9)"} {
		if got := k.String(); got != want {
			t.Errorf("Kind(%d).String() = %q, want %q", int(k), got, want)
		}
	}
}

func TestSpanRange(t *testing.T) {
	b := buffer.New("abc\nあい\n")
	tests := []struct {
		span Span
		want string
	}{
		{Span{pos(0, 1), pos(0, 2), Exclusive}, "b"},
		{Span{pos(0, 1), pos(0, 2), Inclusive}, "bc"},
		{Span{pos(0, 1), pos(0, 3), Inclusive}, "bc"}, // 行末の位置は改行を含めない
		{Span{pos(0, 2), pos(1, 3), Inclusive}, "c\nあい"},
		{Span{pos(0, 2), pos(1, 0), Linewise}, "abc\nあい"},
	}
	for _, tt := range tests {
		got, err := b.Slice(tt.span.Range(b))
		if err != nil || got != tt.want {
			t.Errorf("%+v.Range() text = %q, %v, want %q", tt.span, got, err, tt.want)
		}
	}
}

// req: FR-03
func TestAdjustExclusive(t *testing.T) {
	tests := []struct {
		name string
		text string
		span Span
		want Span
	}{
		{
			name: "字下げの中から次の行の先頭までは行単位",
			text: "  a\nb\n",
			span: Span{pos(0, 1), pos(1, 0), Exclusive},
			want: Span{pos(0, 1), pos(0, 0), Linewise},
		},
		{
			name: "行の途中から次の行の先頭までは前の行の最後の文字まで",
			text: "a あ\nb",
			span: Span{pos(0, 1), pos(1, 0), Exclusive},
			want: Span{pos(0, 1), pos(0, 2), Inclusive},
		},
		{
			name: "前の行が空なら空の行の先頭まで",
			text: "a b\n\nc",
			span: Span{pos(0, 2), pos(2, 0), Exclusive},
			want: Span{pos(0, 2), pos(1, 0), Exclusive},
		},
		{
			name: "同じ行の中は変えない",
			text: "abc",
			span: Span{pos(0, 0), pos(0, 0), Exclusive},
			want: Span{pos(0, 0), pos(0, 0), Exclusive},
		},
		{
			name: "Inclusive は変えない",
			text: "a\nb",
			span: Span{pos(0, 0), pos(1, 0), Inclusive},
			want: Span{pos(0, 0), pos(1, 0), Inclusive},
		},
	}
	for _, tt := range tests {
		if got := AdjustExclusive(buffer.New(tt.text), tt.span); got != tt.want {
			t.Errorf("%s: AdjustExclusive(%+v) = %+v, want %+v", tt.name, tt.span, got, tt.want)
		}
	}
}

// req: FR-03
func TestOperatorSpan(t *testing.T) {
	tests := []struct {
		name   string
		motion string
		text   string
		op     Operator
		want   string // オペレーターが働く範囲のテキスト
		kind   Kind
	}{
		{"dw は次の単語の手前まで", "w", "|foo bar", AnyOperator, "foo ", Exclusive},
		{"dw は行末を越えない", "w", "foo |bar\nbaz", AnyOperator, "bar", Inclusive},
		{"dw はバッファの最後の単語を消せる", "w", "foo |bar", AnyOperator, "bar", Inclusive},
		{"dw は最後の 1 文字を消せる", "w", "ab|c", AnyOperator, "c", Inclusive},
		{"cw は ce と同じ", "w", "|foo bar", ChangeOperator, "foo", Inclusive},
		{"cw は単語の終わりではその 1 文字", "w", "fo|o bar", ChangeOperator, "o", Inclusive},
		{"cw は空白の上では dw と同じ", "w", "foo|  bar", ChangeOperator, "  ", Exclusive},
		{"db は後ろへ", "b", "foo |bar", AnyOperator, "foo ", Exclusive},
		{"dl は最後の文字を消せる", "l", "ab|c", AnyOperator, "c", Exclusive},
		{"d} は行単位", "}", "|a\nb\n\nc", AnyOperator, "a\nb", Linewise},
		{"d} は行の途中からなら前の行の終わりまで", "}", "x |a\nb\n\nc", AnyOperator, "a\nb", Inclusive},
		{"de", "e", "|foo bar", AnyOperator, "foo", Inclusive},
		{"de は最後の 1 文字を消せる", "e", "fo|o", AnyOperator, "o", Inclusive},
		{"d$", "$", "a|bc\nd", AnyOperator, "bc", Inclusive},
		{"dj は行単位", "j", "a|b\ncd\ne", AnyOperator, "ab\ncd", Linewise},
	}
	for _, tt := range tests {
		b, cur := parse(t, tt.text)
		f, _ := LookupMotion(tt.motion)
		res, err := f(b, Request{Cursor: cur, Operator: tt.op})
		if err != nil {
			t.Errorf("%s: %s error = %v", tt.name, tt.motion, err)
			continue
		}
		span := OperatorSpan(b, cur, res)
		got, err := b.Slice(span.Range(b))
		if err != nil || got != tt.want || span.Kind != tt.kind {
			t.Errorf("%s: text = %q (%v), %v, want %q (%v)", tt.name, got, span.Kind, err, tt.want, tt.kind)
		}
	}
}

func pos(line, b int) buffer.Position { return buffer.Position{Line: line, Byte: b} }
