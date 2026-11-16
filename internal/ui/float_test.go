package ui

import (
	"strings"
	"testing"

	"github.com/piplupflipper/modal-editor/internal/buffer"
)

// popup/show の本文は、カーソルの下（収まらなければ上）に浮かべて描き、長い行は折り返し、
// 収まらない行は最後の行に残りの行数を書く。全角文字の半分に掛かる窓の端は、その文字を空白にする。
func TestGoldenPopup(t *testing.T) {
	b := buffer.New("あいうえおかきくけこ\nline1\nline2\nline3\nline4\nline5\nline6\n")
	theme := testTheme()
	theme[StylePopup] = Attr{Reverse: true}
	r := &Renderer{Width: 16, Height: 8, Theme: theme}
	golden(t, "popup", renderSteps(r, []step{
		{"下に置く", Frame{Text: b, Cursor: pos(0, 3), Popup: "func f()\n\nlong line that wraps\n"}},
		{"下に収まらなければ上に置く", Frame{Text: b, Cursor: pos(5, 2), Popup: "a\nb\nc\nd"}},
		{"行が収まらなければ残りの行数", Frame{Text: b, Cursor: pos(1, 0), Popup: "1\n2\n3\n4\n5\n6\n7"}},
		{"右の端で左へ寄せる", Frame{Text: b, Cursor: pos(2, 4), Popup: "abcdefghij"}},
		{"全角文字の半分に掛かる端", Frame{Text: buffer.New("xyz\nあいうえ\n"), Cursor: pos(0, 1), Popup: "q"}},
		{"本文が空なら出さない", Frame{Text: b, Cursor: pos(0, 0), Popup: "\n\n"}},
	}))
}

// 改行を含むメッセージは、コマンドラインの行から上へ広げて描き、画面の半分を超える行は省いたことを書く。
func TestGoldenMessageLines(t *testing.T) {
	b := buffer.New("x\n")
	r := &Renderer{Width: 16, Height: 6, Theme: testTheme()}
	golden(t, "messagelines", renderSteps(r, []step{
		{"2 行", Frame{Text: b, Message: Message{Text: "first\nsecond\n", Level: MessageWarning}}},
		{"画面の半分を超える", Frame{Text: b, Message: Message{Text: strings.Repeat("m\n", 5)}}},
	}))
}

// HitEnter なら、改行を含むメッセージの下の最後の行に hit-enter の問いを出し、カーソルを問いの末尾に置く。
// 問いの行は画面の半分に数えない。1 行のメッセージには問いを出さない。幅に収まらない問いは右の端で切り、カーソルは最後の列に置く（#135）。
func TestGoldenHitEnter(t *testing.T) {
	b := buffer.New("x\n")
	r := &Renderer{Width: 56, Height: 6, Theme: testTheme()}
	golden(t, "hitenter", renderSteps(r, []step{
		{"2 行", Frame{Text: b, HitEnter: true, Message: Message{Text: "first\nsecond\n", Level: MessageError}}},
		{"画面の半分を超える", Frame{Text: b, HitEnter: true, Message: Message{Text: strings.Repeat("m\n", 5)}}},
		{"1 行には出さない", Frame{Text: b, HitEnter: true, Message: Message{Text: "one\n"}}},
	}))
	narrow := &Renderer{Width: 16, Height: 2, Theme: testTheme()}
	golden(t, "hitenter-narrow", renderSteps(narrow, []step{
		{"幅と高さに収まらない", Frame{Text: b, HitEnter: true, Message: Message{Text: "first\nsecond\n"}}},
	}))
}
