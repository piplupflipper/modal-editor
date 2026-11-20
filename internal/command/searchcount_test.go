package command

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/piplupflipper/vi-imitation/internal/editcmd"
	"github.com/piplupflipper/vi-imitation/internal/option"
)

// searchCountText は、Neovim 0.12.5 で検索の件数を確かめたファイル（foo の一致が 4 つ）の中身である。
const searchCountText = "foo bar\nbaz foo\nqux\nfoo foo\nend"

// setupSearchCount は、text（"|" がカーソル）を開き、設定の search.showCount を show にした State を返す。
func setupSearchCount(t *testing.T, text string, show bool) *State {
	t.Helper()
	s := setup(t, text)
	s.Ex.Options = &toggleOptions{vals: map[string]option.Value{option.SearchShowCount: option.BoolValue(show)}}
	return s
}

// lastMessage は、keys を打った結果のうち、最後に通知のあった結果の通知を返す。
func lastMessage(s *State, keys string) Message {
	var m Message
	for _, r := range feedKeys(s, keys) {
		if r.Message.Text != "" {
			m = r.Message
		}
	}
	return m
}

// 設定の search.showCount が真なら、検索で動いたときに、パターンと一緒に一致が何件目かと件数を返す（Message.Count）。
// バッファの端で折り返したら、折り返しの警告の文を出さずに、件数の前に W を付けてパターンを出す。
// 期待値は、2026-11-21 に nvim --clean（0.12.5）を tmux で動かし、同じ中身のファイルの同じ位置から同じキーを打って、
// メッセージの行に出た文字列である（* と # は、FR-139 でカーソルを動かさないので Neovim と違う。その行は注に書いた）。
//
// req: FR-167
func TestSearchCount(t *testing.T) {
	for _, tc := range []struct {
		name, text, keys string
		want             Message
	}{
		{"/", "|" + searchCountText, "/foo<CR>", Message{Text: "/foo", Count: "[2/4]"}},
		{"n", "|" + searchCountText, "/foo<CR>n", Message{Text: "/foo", Count: "[3/4]"}},
		{"回数を付けた n", "|" + searchCountText, "/foo<CR>0k3n", Message{Text: "/foo", Count: "[4/4]"}},
		{"末尾から先頭へ戻った /", "foo bar\nbaz foo\nqux\nfoo foo\n|end", "/foo<CR>", Message{Text: "/foo", Count: "W [1/4]"}},
		{"先頭から末尾へ戻った N", "|" + searchCountText, "/foo<CR>NN", Message{Text: "?foo", Count: "W [4/4]"}},
		{"?", "foo bar\nbaz foo\nqux\nfoo foo\n|end", "?foo<CR>", Message{Text: "?foo", Count: "[4/4]"}},
		{"? の後の N は前向き", "foo bar\nbaz foo\nqux\nfoo foo\n|end", "?foo<CR>N", Message{Text: "/foo", Count: "W [1/4]"}},
		// Neovim の * は次の一致（[2/4]）へ移るが、本体の * は動かないので、カーソルの一致の番号を出す（オーナーの nvim の *N と同じ番号）。
		{"*", "|" + searchCountText, "*", Message{Text: `/\bfoo\b`, Count: "[1/4]"}},
		{"単語の途中の *", "foo bar\nbaz f|oo\nqux\nfoo foo\nend", "*", Message{Text: `/\bfoo\b`, Count: "[2/4]"}},
		// Neovim の # は末尾の一致（W [4/4]）へ移るが、本体の # は動かない。
		{"#", "|" + searchCountText, "#", Message{Text: `?\bfoo\b`, Count: "[1/4]"}},
		// 単語の手前の空白の * は、単語の先頭で数える（Neovim 0.12.5 の *N は [2/4] と [1/2]。PR #322 のレビューの推奨 5）。
		{"単語の手前の空白の *", "foo bar\nbaz| foo\nqux\nfoo foo\nend", "*", Message{Text: `/\bfoo\b`, Count: "[2/4]"}},
		{"行頭の空白の *", "|  foo\nfoo", "*", Message{Text: `/\bfoo\b`, Count: "[1/2]"}},
		{"* の後の n", "|" + searchCountText, "*n", Message{Text: `/\bfoo\b`, Count: "[2/4]"}},
		{"オペレーターの後の /", "|" + searchCountText, "d/foo<CR>", Message{Text: "/foo", Count: "[2/4]"}},
		{"オペレーターの後の / の .", "|" + searchCountText, "d/foo<CR>.", Message{Text: "/foo", Count: "[2/3]"}},
		{"ビジュアルモードの /", "|" + searchCountText, "v/foo<CR>", Message{Text: "/foo", Count: "[2/4]"}},
		{"マクロの再生の中の n", "|" + searchCountText, "/foo<CR>qanqgg@a", Message{Text: "/foo", Count: "[2/4]"}},
		{"一致を消した後の n は数え直す", "|" + searchCountText, "/foo<CR>xn", Message{Text: "/foo", Count: "[2/3]"}},
		{"行末の一致", "|abc\ndef", "/$<CR>n", Message{Text: "/$", Count: "[2/2]"}},
		{"全角の文字を含む一致", "|xあいx あい", "/あい<CR>n", Message{Text: "/あい", Count: "[2/2]"}},
		{"全角の文字の後の一致", "|あfooい\nfoo", "/foo<CR>", Message{Text: "/foo", Count: "[1/2]"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := setupSearchCount(t, tc.text, true)
			if got := lastMessage(s, tc.keys); got != tc.want {
				t.Errorf("message = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// 行末の空の一致が、前に寄せた位置で前の一致と重なっても、件数は n で止まる位置の数と同じで、n のたびに番号が 1 ずつ進む。
// 期待値は、PR #322 のレビューの再現（ab、a b、x の 3 行の 1 行目の先頭で /\b<CR> の後に n を 5 回）を、2026-11-21 に
// nvim --clean（0.12.5）で同じ位置に一致する /\<\|\> を打って出た件数である（[2/6] から W [1/6] まで）。
//
// req: FR-167
func TestSearchCountEmptyAtLineEnd(t *testing.T) {
	s := setupSearchCount(t, "|ab\na b\nx", true)
	steps := []struct{ keys, cursor, count string }{
		{`/\b<CR>`, "0:1", "[2/6]"},
		{"n", "1:0", "[3/6]"},
		{"n", "1:1", "[4/6]"},
		{"n", "1:2", "[5/6]"},
		{"n", "2:0", "[6/6]"},
		{"n", "0:0", "W [1/6]"},
	}
	for _, st := range steps {
		m := lastMessage(s, st.keys)
		if got := s.Cursor().String(); got != st.cursor || m.Count != st.count {
			t.Errorf("%s: cursor = %s, count = %q, want %s, %q", st.keys, got, m.Count, st.cursor, st.count)
		}
	}
}

// 件数が上限の 999 を超えたら、件数を ">999" と出す。カーソルの一致が 1000 件目より先なら、何件目かも ">999" と出す。
// 期待値は、2026-11-21 に nvim --clean（0.12.5。'maxsearchcount' の既定は 999）で、"x 1" から "x 1200" の 1200 行のファイルで確かめた
// （/x で [2/>999]、998G/x で [999/>999]、999G/x と G?x で [>999/>999]）。
//
// req: FR-167
func TestSearchCountLimit(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 1200; i++ {
		b.WriteString("x ")
		b.WriteString(strings.Repeat("y", i%7))
		b.WriteByte('\n')
	}
	text := strings.TrimSuffix(b.String(), "\n")
	for _, tc := range []struct {
		keys string
		want Message
	}{
		{"/x<CR>", Message{Text: "/x", Count: "[2/>999]"}},
		{"998G/x<CR>", Message{Text: "/x", Count: "[999/>999]"}},
		{"999G/x<CR>", Message{Text: "/x", Count: "[>999/>999]"}},
		{"G?x<CR>", Message{Text: "?x", Count: "[>999/>999]"}},
		{"G/x<CR>", Message{Text: "/x", Count: "W [1/>999]"}},
	} {
		s := setupSearchCount(t, "|"+text, true)
		if got := lastMessage(s, tc.keys); got != tc.want {
			t.Errorf("%s: message = %+v, want %+v", tc.keys, got, tc.want)
		}
	}
}

// 数える時間の上限を超えたら、件数の代わりに "[?/??]" を返す（Neovim 0.12.5 と同じ）。上限を超えた結果は覚えないので、
// 上限に収まるようになれば、次の n で件数を出す。
//
// req: FR-167
func TestSearchCountTimeout(t *testing.T) {
	old := searchCountTimeout
	t.Cleanup(func() { searchCountTimeout = old })
	searchCountTimeout = 0
	text := "foo\n" + strings.Repeat("abcdefghij klmnop\n", 10000) + "foo"
	s := setupSearchCount(t, "|"+text, true)
	if got, want := lastMessage(s, "/foo<CR>"), (Message{Text: "/foo", Count: "[?/??]"}); got != want {
		t.Errorf("message = %+v, want %+v", got, want)
	}
	if got, want := lastMessage(s, "n"), (Message{Text: "/foo", Count: "W [?/??]"}); got != want {
		t.Errorf("折り返した n: message = %+v, want %+v", got, want)
	}
	searchCountTimeout = time.Minute
	if got, want := lastMessage(s, "n"), (Message{Text: "/foo", Count: "[2/2]"}); got != want {
		t.Errorf("上限に収まった n: message = %+v, want %+v", got, want)
	}
}

// 一致が無い（E486）ときは、件数を出さずに誤りにする（Neovim 0.12.5 と同じ）。
// / と ? を打っている間（打つたびの検索）は、コマンドラインの行を使うので件数を返さない。
//
// req: FR-167
func TestSearchCountNotFound(t *testing.T) {
	s := setupSearchCount(t, "|"+searchCountText, true)
	rs := feedKeys(s, "/zzz<CR>")
	if r := rs[len(rs)-1]; r.Err == nil || r.Message != (Message{}) {
		t.Errorf("E486: err = %v, message = %+v", r.Err, r.Message)
	}
	for _, r := range feedKeys(s, "/fo") {
		if r.Message != (Message{}) {
			t.Errorf("打っている間の message = %+v", r.Message)
		}
	}
}

// BenchmarkSearchCountNext は、NFR-05 と同じ形の 10,000 行のファイルで n を打ち続けたときの、n の 1 回の処理の時間を測る（FR-167）。
// off は件数を出さないとき、on は件数を出すとき（同じパターンで続けて打つので、2 回目からは集めた一致を使い回す）、
// miss は件数を出し、毎回一致を集め直すとき（バッファを変えた直後の n に当たる）である。
// パターンは、どの行にも一致する fox（1000 件で数えるのをやめる）と、1 行にだけ一致する line 005000（全ての行を読む）である。
func BenchmarkSearchCountNext(b *testing.B) {
	var sb strings.Builder
	for i := 1; i <= 10000; i++ {
		fmt.Fprintf(&sb, "line %06d: the quick brown fox jumps over the lazy dog 0123456789\n", i)
	}
	text := strings.TrimSuffix(sb.String(), "\n")
	for _, pat := range []string{"fox", "line 005000"} {
		for _, mode := range []string{"off", "on", "miss"} {
			b.Run(pat+"/"+mode, func(b *testing.B) {
				s := setup(&testing.T{}, "|"+text)
				s.Ex.Options = &toggleOptions{vals: map[string]option.Value{option.SearchShowCount: option.BoolValue(mode != "off")}}
				feedKeys(s, "/"+pat+"<CR>")
				n := editcmd.MustParseKeys("n")[0]
				b.ResetTimer()
				for range b.N {
					if mode == "miss" {
						s.matchCache = nil
					}
					if r := s.Feed(n); r.Err != nil {
						b.Fatal(r.Err)
					}
				}
			})
		}
	}
}

// 設定の search.showCount が偽（本体の既定）なら、件数を返さず、今までどおり、折り返したときは警告の文を返す。
// 設定を読む口が無い State でも件数を返さない。
//
// req: FR-167
func TestSearchCountOff(t *testing.T) {
	for _, s := range []*State{setupSearchCount(t, "|"+searchCountText, false), setup(t, "|"+searchCountText)} {
		if got, want := lastMessage(s, "/foo<CR>"), (Message{Text: "/foo"}); got != want {
			t.Errorf("message = %+v, want %+v", got, want)
		}
		if got, want := lastMessage(s, "G/foo<CR>"), (Message{Text: "末尾まで検索したので先頭から続けました", Warning: true}); got != want {
			t.Errorf("折り返し: message = %+v, want %+v", got, want)
		}
		if got, want := lastMessage(s, "*"), (Message{Text: `/\bfoo\b`}); got != want {
			t.Errorf("*: message = %+v, want %+v", got, want)
		}
	}
}
