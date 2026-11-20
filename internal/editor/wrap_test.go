package editor

import (
	"fmt"
	"strings"
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/option"
)

// wrapFile は、折り返しの確かめに使うファイルの中身である。nvim 0.12.5 の突き合わせに使ったものと同じにした。
//
//	2 行目: 93 文字の半角の行（41 列なら 3 行）
//	3 行目: 35 文字の全角の行（41 列なら、1 行目の最後の 1 列に全角文字が入らない）
//	4 行目: タブを 2 つ含む行
//	7 行目: 400 文字の行
func wrapFile() string {
	lines := []string{
		"line1 short",
		"0123456789abcdefghij0123456789ABCDEFGHIJ0123456789abcdefghij0123456789ABCDEFGHIJ0123456789xyz",
		"あいうえおかきくけこさしすせそたちつてとなにぬねのはひふへほまみむめも",
		"abcdefghijklmnopqrstuvwxyzabcdefghijkl\tTAB\tx",
		"line5",
		"line6",
		strings.Repeat("0123456789", 40),
	}
	for i := 8; i <= 20; i++ {
		lines = append(lines, fmt.Sprintf("line%d", i))
	}
	return strings.Join(lines, "\n") + "\n"
}

// startWrap は、41 列 12 行の偽の端末で wrapFile を開く。opts は設定ファイルの値である。
func startWrap(t *testing.T, opts map[string]option.Value) *harness {
	t.Helper()
	dir := writeFiles(t, map[string]string{"a.txt": wrapFile()})
	term := newFakeTerm()
	term.w, term.h = 41, 12
	h := startTerm(t, term, Config{WorkDir: dir, Options: opts}, "a.txt")
	h.settle()
	return h
}

// gj と gk の行き先、回数、オペレーターの後、ビジュアルモードを、nvim 0.12.5（--clean、41 列）の結果と比べる。
// 期待値は、2026-11-20 に nvim --headless で :set columns=41 にして :normal でキーを打ち、line(".") と col(".") と
// 無名のレジスターを読んだ値である（位置は 0 から数えた行とバイト）。
//
// nvim と違うのは、回数の途中でバッファの端に着いたときである。nvim は着いた位置へ移ってから失敗する（オペレーターを取り消す）が、
// 本体は着いた位置へ移って成功する（docs/vim-differences.md）。この表には、その場合を入れていない（TestDisplayLineEdge）。
//
// req: FR-157
func TestDisplayLineMotion(t *testing.T) {
	h := startWrap(t, map[string]option.Value{option.LineWrapEnabled: option.BoolValue(true)})
	orig := h.text()
	tests := []struct {
		keys string
		pos  string // 行:バイト
		reg  string // 空でなければ、無名のレジスターの中身
	}{
		{"2Ggj", "1:41", ""},
		{"2G5lgjgj", "1:87", ""},
		{"2G5lgjgjgj", "2:6", ""},   // 次の行の全角文字の行へ。x は 5 列目で、全角の 3 文字目（4 列目から）
		{"2G$gj", "2:57", ""},       // $ の後は、見た目の行の右の端の文字（20 文字目）
		{"2G$gjgj", "2:102", ""},    // 2 番目の見た目の行の最後の文字
		{"2G3gj", "2:0", ""},        //
		{"2G9gj", "6:0", ""},        // 2 行目の 2 つ、3 行目の 2 つ、4 行目の 2 つ、5、6、7 行目
		{"2G40lgjgjgj", "2:57", ""}, // x が 40 列目（">" の列）なら、その見た目の行の最後の文字
		{"2G40lgjgjgk", "1:81", ""}, // 望みの列は、最後の見た目の行で文字の右へ伸ばしたまま保つ
		{"1Ggk", "0:0", ""},         // 最初の行の最初の見た目の行では動かない
		{"2G3lgk", "0:3", ""},       //
		{"3Ggj", "2:60", ""},        // 全角の行の 2 番目の見た目の行の最初の文字
		{"3G3lgj", "2:69", ""},      //
		{"3Ggk", "1:82", ""},        // 前の行の最後の見た目の行へ
		{"4G39lgj", "3:43", ""},     // 次の見た目の行に x の列が無ければ、最後の文字
		{"3G19ljgjgk", "3:37", ""},  // タブの最初の列を望みの列にして着くと、タブの前の文字へ（nvim の oneleft）
		{"20G3gk", "16:0", ""},      //
		{"6G3gjd2gk", "6:0", strings.Repeat("0123456789", 8) + "01"},
		{"2G3ld3gj", "1:3", "3456789abcdefghij0123456789ABCDEFGHIJ0123456789abcdefghij0123456789ABCDEFGHIJ0123456789xyz\nあ"},
		{"2G$dgj", "1:92", "z\nあいうえおかきくけこさしすせそたちつてと"}, // $ の後は最後の文字を含む
		// 行の先頭から行の先頭への Exclusive は行単位になる（Vim の exclusive-linewise）。レジスターは行単位で、テキストに最後の改行を含めない。
		{"2Gd3gj", "1:0", "0123456789abcdefghij0123456789ABCDEFGHIJ0123456789abcdefghij0123456789ABCDEFGHIJ0123456789xyz"},
		{"2Gdgk", "0:0", "line1 short"},
		// ビジュアルモードの y の後は、y を押したときの位置に残す（FR-129。nvim は選択の始まりへ移す。docs/vim-differences.md）。
		{"2Gvgjy", "1:41", "0123456789abcdefghij0123456789ABCDEFGHIJ01"},
		{"2G$vgjy", "2:57", "z\nあいうえおかきくけこさしすせそたちつてと"},
	}
	for _, tt := range tests {
		h.keys("<Esc>" + tt.keys)
		h.settle()
		var pos, reg string
		h.e.do(func() {
			pos = h.e.cur.state.Cursor().String()
			r, _ := h.e.cur.state.Registers().Get(0)
			reg = r.Text
		})
		if pos != tt.pos {
			t.Errorf("%s: カーソル = %s, want %s", tt.keys, pos, tt.pos)
		}
		if tt.reg != "" && reg != tt.reg {
			t.Errorf("%s: レジスター = %q, want %q", tt.keys, reg, tt.reg)
		}
		if strings.ContainsAny(tt.keys, "dx") {
			// 消したものを戻して、次の行のために元のテキストにする。
			h.keys("u")
			h.settle()
			if got := h.text(); got != orig {
				t.Fatalf("%s: u の後のテキストが元に戻らない: %q", tt.keys, got)
			}
		}
	}
}

// gj と gk が、全角文字を送った ">" の列と、タブを通るときの行き先である（PR #310 のレビューの必須 1、2、3）。
// 期待値は、2026-11-20 に nvim 0.12.5 を --headless で :set columns=41 number numberwidth=5（本文は 36 列）にして :normal でキーを打ち、
// line(".") と col(".") を読んだ値である（位置は 0 から数えた行とバイト）。レビューの Vim 9.1 の結果とも同じである。
//
//   - 2 行目は x と全角文字 17 文字で 35 列を使い、36 列目に ">" を出す。">" の列を通る gj と gk は、見た目の行を飛ばさない
//   - 着いたタブは、望みの列が見た目の行の左半分なら、タブの前の文字へ戻さない（nv_screengo の末尾の補正）。タブで字下げした行で j と同じ列へ動く
//   - 4 行目（a から q の 17 文字をタブでつないだ行）の、見た目の行の境目をまたぐタブにも着く
//   - ">" の後のタブの幅は、">" の列を数えた列で決める（11 行目の 2 つ目の見た目の行は "つa b       c"）。ルーラーの表示の列も同じ列で数える
//
// req: FR-156, FR-157
func TestDisplayLinePadAndTab(t *testing.T) {
	tabs := strings.Join(strings.Split("abcdefghijklmnopq", ""), "\t")
	text := strings.Join([]string{
		strings.Repeat("a", 40), "xあいうえおかきくけこさしすせそたちつてと", "next line", tabs, "foo bar baz",
		"func main() {", "\t\tx := 1", "\t\ty := 2", "\treturn", "}", "xあいうえおかきくけこさしすせそたちつa\tb\tc",
	}, "\n") + "\n"
	dir := writeFiles(t, map[string]string{"a.txt": text})
	term := newFakeTerm()
	term.w, term.h = 41, 20
	h := startTerm(t, term, Config{WorkDir: dir, Options: map[string]option.Value{
		option.LineWrapEnabled: option.BoolValue(true), option.LineNumberShow: option.BoolValue(true),
		option.LineNumberMinWidth: option.IntValue(5),
	}}, "a.txt")
	h.settle()
	tests := []struct {
		keys, pos string
	}{
		{"gg35lgjgj", "1:49"},
		{"gg35lgjgjgj", "1:58"},
		{"gg35lgjgjgk", "0:39"},
		{"6G010lgj", "6:1"},
		{"6G010lgjgj", "7:1"},
		{"6G010lj", "6:1"},
		{"5G0gk", "3:27"},
		{"5G05lgk", "3:29"},
		{"5G06lgk", "3:29"},
	}
	for _, tt := range tests {
		h.keys("<Esc>" + tt.keys)
		h.settle()
		var pos string
		h.e.do(func() { pos = h.e.cur.state.Cursor().String() })
		if pos != tt.pos {
			t.Errorf("%s: カーソル = %s, want %s", tt.keys, pos, tt.pos)
		}
	}
	h.keys("<Esc>gg35lgjgjgj")
	h.settle()
	h.eventually("ルーラーの表示の列は > の列を数える", func() bool { return strings.Contains(h.statusRow(), "2,59-41") })
	h.keys("11G")
	h.settle()
	h.eventually("> の後のタブの幅", func() bool {
		return strings.Contains(h.term.screen(), "\n     つa b       c")
	})
}

// 回数の途中でバッファの端に着いたら、着いた位置へ移って成功する（Vim は移ってから失敗する）。1 行も動けなければ失敗する。
// 折り返さないとき（:set nowrap）は、j と k と同じく行単位で動き、オペレーターの後は行単位になる。:set wrap! で切り替えられる。
//
// req: FR-157
func TestDisplayLineEdge(t *testing.T) {
	h := startWrap(t, map[string]option.Value{option.LineWrapEnabled: option.BoolValue(true)})
	cursor := func() string {
		var s string
		h.e.do(func() { s = h.e.cur.state.Cursor().String() })
		return s
	}
	h.keys("19G5gj")
	h.settle()
	if got := cursor(); got != "19:0" {
		t.Errorf("19G5gj: カーソル = %s, want 19:0", got)
	}
	// 最後の行の最後の見た目の行では動けず、失敗する。失敗した操作は、マクロの再生を止める。
	// マクロは、動ける 1 行目で記録し（gj で 2 行目へ、x で 0 を消す）、u で戻してから最後の行で再生する。
	h.keys("ggqagjxqu")
	h.settle()
	h.keys("G@a")
	h.settle()
	if got, text := cursor(), h.text(); got != "19:0" || !strings.HasSuffix(text, "line20") {
		t.Errorf("最後の行の gj: カーソル = %s、末尾 = %q（x を実行しない）", got, text[len(text)-10:])
	}

	// オペレーターの後も、着いた位置までを消す（行の先頭から行の先頭への Exclusive なので行単位）。nvim は何も消さない。
	h.keys("2Gd6gk")
	h.settle()
	if got := h.text(); !strings.HasPrefix(got, "0123456789abcdefghij") {
		t.Errorf("2Gd6gk: 先頭 = %q（1 行目を消す）", got[:20])
	}
	h.keys("u")
	h.settle()

	h.keys("<Esc>:set nowrap<CR>2G5lgj")
	h.settle()
	if got := cursor(); got != "2:6" {
		t.Errorf("折り返さないときの gj: カーソル = %s, want 2:6（j と同じく望みの列 5 の文字）", got)
	}
	h.keys("2Gdgj")
	h.settle()
	if got := h.text(); !strings.HasPrefix(got, "line1 short\nabcdefghijkl") {
		t.Errorf("折り返さないときの dgj: 先頭 = %q（2 行目と 3 行目を行単位で消す）", got[:min(len(got), 30)])
	}
	h.keys("u:set wrap!<CR>2Ggj")
	h.settle()
	if got := cursor(); got != "1:41" {
		t.Errorf(":set wrap! の後の gj: カーソル = %s, want 1:41", got)
	}
}

// 折り返すとき、<PageDown> と <PageUp> は行の高さ（見た目の行の数）を数えて動く。
// 期待値は、2026-11-20 に Vim 9.1（パッチ 2141 まで。-Nu NONE --not-a-term、columns=41、lines=12 で表示領域は 10 行）で、
// キーの間に :redraw を挟んで打ち、line("w0") と line(".") を読んだ値である（行は 1 から数える）。
// 400 文字の 7 行目は 10 行の高さで、重ねて残す行（表示領域の行数から 2 を引いた 8 行まで）に入らない。
// G の後の <PageUp> だけは Vim と違う。Vim 9.1 の新しいページ送り（パッチ 211 から）はカーソルを表示領域の最後の行（17 行目）へ移すが、
// 本体は前のページ送り（onepage）と同じく、重ねて残した行（12 行目）へ移す（最初の行の 8 行目は同じ）。
//
// req: FR-156
func TestPageScrollWrap(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": wrapFile()})
	term := newFakeTerm()
	// タブの行、ステータス行、コマンドラインの行を除いて、表示領域を 10 行にする。
	term.w, term.h = 41, 13
	h := startTerm(t, term, Config{WorkDir: dir, Options: map[string]option.Value{option.LineWrapEnabled: option.BoolValue(true)}}, "a.txt")
	h.settle()
	tests := []struct {
		keys      string
		top, line int // 1 から数える
	}{
		{"<PageDown>", 7, 7},
		{"<PageDown>", 8, 8},
		{"<PageDown>", 16, 16},
		{"<PageUp>", 8, 17},
		{"<PageUp>", 7, 7},
		{"<PageUp>", 1, 6},
		{"8G", 8, 8},
		{"<PageUp>", 7, 7},
		{"G", 11, 20},
		{"<PageUp>", 8, 12},
	}
	for _, tt := range tests {
		h.keys(tt.keys)
		h.settle()
		h.eventually(tt.keys, func() bool {
			return h.e.renderer.Top()+1 == tt.top && h.e.cur.state.Cursor().Line+1 == tt.line
		})
	}
}

// 折り返すと、描くたびに設定の今の値を読み、続きの見た目の行の行番号の列は空ける。:set nowrap で戻る。
//
// req: FR-156
func TestLineWrapOption(t *testing.T) {
	h := startWrap(t, map[string]option.Value{
		option.LineWrapEnabled: option.BoolValue(true), option.LineNumberShow: option.BoolValue(true),
	})
	// 画面の最初の行はタブの行なので、バッファの最初の行は 2 行目に描く。
	h.eventually("折り返して描く", func() bool {
		return h.screenRow(2) == "  2 0123456789abcdefghij0123456789ABCDEFG" && h.screenRow(3) == "    HIJ0123456789abcdefghij0123456789ABCD"
	})
	h.keys(":set nowrap<CR>")
	h.settle()
	h.eventually("折り返さない", func() bool { return h.screenRow(3) == "  3 あいうえおかきくけこさしすせそたちつ>" })
}
