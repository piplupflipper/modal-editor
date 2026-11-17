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
		// Markdown のコードの囲みの行は出さず、中身は残す。plaintext ではそのまま出す（#151）。
		{"Markdown の囲みを除く", Frame{Text: b, Cursor: pos(0, 0), Popup: "```go\nfunc f()\n```\n\ndoc", PopupMarkdown: true}},
		{"plaintext の囲みは残す", Frame{Text: b, Cursor: pos(0, 0), Popup: "```go\nfunc f()\n```"}},
		// 英語の文は空白の位置で折り返し、単語の途中で切らない（#155）。
		{"空白の位置で折り返す", Frame{Text: b, Cursor: pos(0, 0), Popup: "writes to standard output"}},
	}))
}

// 選択の一覧（:registers）は、表示領域の上の端の横の中央に浮かべ、選んでいる項目の行を別の色で描いてカーソルを置く。
// 項目が収まらなければ、選んでいる項目が見えるまでずらす。長い項目は右の端で切る。
//
// req: FR-54
func TestGoldenList(t *testing.T) {
	b := buffer.New("line1\nline2\nline3\nline4\nline5\nline6\n")
	theme := testTheme()
	theme[StylePopup] = Attr{Underline: true}
	theme[StylePopupSelected] = Attr{Reverse: true}
	r := &Renderer{Width: 20, Height: 7, Theme: theme}
	items := []string{" 1  c  abc", " 2  l  あいう（他 2 行）", `"a  c  x`, `"b  c  a very long register text`, `"c  c  last`}
	golden(t, "list", renderSteps(r, []step{
		{"最初の項目", Frame{Text: b, Cursor: pos(0, 0), List: &List{Title: "reg", Items: items[:2]}}},
		{"選んだ項目", Frame{Text: b, Cursor: pos(0, 0), List: &List{Title: "reg", Items: items[:3], Selected: 1}}},
		{"収まらなければずらす", Frame{Text: b, Cursor: pos(0, 0), List: &List{Title: "reg", Items: items, Selected: 4}}},
		{"ポップアップより一覧を出す", Frame{Text: b, Cursor: pos(0, 0), Popup: "hover", List: &List{Title: "reg", Items: items[:1]}}},
	}))
}

// wrapWords は、空白の位置で折り返す。空白の無い長い単語と日本語の文は幅で切る。行末からはみ出す空白は次の行へ送らない（#155）。
func TestWrapWords(t *testing.T) {
	tests := []struct {
		s     string
		width int
		want  []string
	}{
		{"", 5, []string{""}},
		{"abc def", 7, []string{"abc def"}},
		{"writes to standard output", 14, []string{"writes to ", "standard ", "output"}},
		{"ab cd", 2, []string{"ab", "cd"}},
		{"ab   cd", 3, []string{"ab ", "cd"}},
		{"abcdefgh ij", 3, []string{"abc", "def", "gh ", "ij"}},
		{"a abcdefgh", 4, []string{"a ", "abcd", "efgh"}},
		{"あいうえお", 4, []string{"あい", "うえ", "お"}},
		{"ab\tcd ef", 10, []string{"ab\tcd", "ef"}},
		{"ab\tcd ef", 4, []string{"ab", "cd ", "ef"}},
		{"ab ", 2, []string{"ab"}},
	}
	for _, tt := range tests {
		var got []string
		for _, row := range wrapWords(0, tt.s, tt.width, 8) {
			got = append(got, tt.s[row.start:row.end])
		}
		if strings.Join(got, "|") != strings.Join(tt.want, "|") {
			t.Errorf("wrapWords(%q, %d) = %q, want %q", tt.s, tt.width, got, tt.want)
		}
	}
}

// stripCodeFences は、行頭の 3 つまでの空白の後に ``` か ~~~ が続く行だけを除く（#151）。
func TestStripCodeFences(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"```go\nfunc add(a, b int) int\n```", "func add(a, b int) int"},
		{"```go\nfunc f()\n```\n\n---\n\ndoc `x`", "func f()\n\n---\n\ndoc `x`"},
		{"~~~\nx\n~~~", "x"},
		{"   ```\nx\n    ```", "x\n    ```"},
		{"a ``` b", "a ``` b"},
		{"", ""},
	} {
		if got := stripCodeFences(tc.in); got != tc.want {
			t.Errorf("stripCodeFences(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
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

// 選択肢の問いは、問いの文と選択肢を 1 つずつ行に分けて下から上へ広げ、最後の行の選び方の案内の末尾にカーソルを置く。
// 長い行は折り返す。画面の半分を超えても広げ、収まらなければ問いの文を先に減らし、それでも収まらなければ選択肢を減らす。
// コマンドラインモードとメッセージより先に出す（#144）。
func TestGoldenChoice(t *testing.T) {
	b := buffer.New("x\n")
	r := &Renderer{Width: 40, Height: 8, Theme: testTheme()}
	two := []string{"Allow once", "Reject"}
	golden(t, "choice", renderSteps(r, []step{
		{"2 つの選択肢", Frame{Text: b, Choice: &Choice{Title: "エージェントが権限を求めている: Edit main.go", Labels: two}}},
		{"画面の半分を超える", Frame{Text: b, Choice: &Choice{Title: "どれ?", Labels: []string{"a", "b", "c", "d", "e"}}}},
		{"問いの文を減らす", Frame{Text: b, Choice: &Choice{Title: "1\n2\n3\n4\n5", Labels: []string{"a", "b", "c", "d"}}}},
		{"選択肢を減らす", Frame{Text: b, Choice: &Choice{Title: "t", Labels: []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}, Omitted: 3}}},
		{"ラベルの改行と長いラベル", Frame{Text: b, Choice: &Choice{Title: "t", Labels: []string{"two\nlines", strings.Repeat("long ", 10)}}}},
		{"コマンドラインモードとメッセージより先に出す", Frame{Text: b, Mode: ModeCommandLine, CommandLine: "w", Message: Message{Text: "m"}, Choice: &Choice{Title: "t", Labels: []string{"only"}}}},
		{"選択肢が無い", Frame{Text: b, Choice: &Choice{Title: "t"}}},
		// 続けて届いた問いは、案内の前に何件目かを出し、知らせの行を問いの文の上に出す（#151）。
		{"続けて届いた問いと知らせ", Frame{Text: b, Choice: &Choice{Title: "t", Labels: two, Index: 2, Note: "問い 1 に「Allow once」と答えました"}}},
		{"知らせの行も問いの文と一緒に減らす", Frame{Text: b, Choice: &Choice{Title: "1\n2", Labels: []string{"a", "b", "c", "d"}, Note: "n1\nn2"}}},
	}))
	narrow := &Renderer{Width: 20, Height: 1, Theme: testTheme()}
	golden(t, "choice-narrow", renderSteps(narrow, []step{
		{"案内の行だけ", Frame{Text: b, Choice: &Choice{Title: "t", Labels: two}}},
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
