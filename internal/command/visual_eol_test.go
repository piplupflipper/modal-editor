package command

import "testing"

// visualEOLCases は、文字単位の選択の端が、j や k で移った短い行や l で進んだ行の行末の後（Vim がカーソルを置く NUL の位置）にあるときの
// オペレーターの結果を、Vim 9.1 と突き合わせたケースである（#89）。
//
// 期待値の形と入力の前のレジスターは vimCase と同じである。ただし Vim は Ex モードでない起動
// （`vim -Nu NONE -i NONE -n --not-a-term -c 'source <script>'`）で動かし、ケースごとに setline() の後でアンドゥの履歴を消してから、
// keys を feedkeys(keys, "xt") で入力した。
//
// 選択の始まりが行末の後なら、その行の改行から選ぶ（a と bcd|e の vkd は a を残す）。
// 選択の終わりが行末の後なら、その行の改行まで選ぶ（ab|c と d と e の vjd は abe になる）。
// 行末の後から h で戻る列（vkhd）、ドットリピート、アンドゥ、o、短くない行へ戻る j と k、$ の後の k、v と V の切り替え、
// 全角文字の行、ビジュアルモードを抜けた後のカーソルを含む。
var visualEOLCases = []vimCase{
	{"a\nbcd|e", "vkd", "a", "0:0", "v:\nbcde", "v:U", "v:\nbcde", ":", "v:A"},
	{"a\nbcd|e", "vky", "a\nbcde", "0:0", "v:\nbcde", "v:\nbcde", ":", ":", "v:A"},
	{"a\nbcd|e", "vk~", "a\nBCDE", "0:0", "v:U", "v:U", ":", ":", "v:A"},
	{"a\nbcd|e", "vkd.", "", "0:0", "v:a", "v:U", "v:\nbcde", "v:a", "v:A"},
	{"a\nbcd|e", "vkdu", "a\nbcde", "0:0", "v:\nbcde", "v:U", "v:\nbcde", ":", "v:A"},
	{"a\nbcd|e", "vkxp", "a\nbcde", "0:0", "v:\nbcde", "v:U", "v:\nbcde", ":", "v:A"},
	{"ab\ncdef|g", "vkhd", "a", "0:0", "v:b\ncdefg", "v:U", "v:b\ncdefg", ":", "v:A"},
	{"ab\ncdef|g", "vklhd", "a", "0:0", "v:b\ncdefg", "v:U", "v:b\ncdefg", ":", "v:A"},
	{"ab|c\nd\ne", "vjd", "abe", "0:2", "v:c\nd\n", "v:U", "v:c\nd\n", ":", "v:A"},
	{"ab|c\nd\ne", "vjy", "abc\nd\ne", "0:2", "v:c\nd\n", "v:c\nd\n", ":", ":", "v:A"},
	{"ab|c\nd", "vjd", "ab", "0:1", "v:c\nd", "v:U", "v:c\nd", ":", "v:A"},
	{"ab|c\nd\ne\nf", "vjd.", "ab", "0:1", "v:e\nf", "v:U", "v:e\nf", ":", "v:A"},
	{"abc|d\nef", "vjd", "abc", "0:2", "v:d\nef", "v:U", "v:d\nef", ":", "v:A"},
	{"abc|d\nef", "vjhd", "abc", "0:2", "v:d\nef", "v:U", "v:d\nef", ":", "v:A"},
	{"a|bcd\nef", "vjd", "a", "0:0", "v:bcd\nef", "v:U", "v:bcd\nef", ":", "v:A"},
	{"a|b\n\ncd", "vjd", "acd", "0:1", "v:b\n\n", "v:U", "v:b\n\n", ":", "v:A"},
	{"ab\n|c", "vkd", "", "0:0", "v:ab\nc", "v:U", "v:ab\nc", ":", "v:A"},
	{"ab\n|c", "vkxu", "ab\nc", "0:0", "v:ab\nc", "v:U", "v:ab\nc", ":", "v:A"},
	{"ab\n|c", "vkkd", "", "0:0", "v:ab\nc", "v:U", "v:ab\nc", ":", "v:A"},
	{"abcd\nef\nghi|j", "vkkd", "abc", "0:2", "v:d\nef\nghij", "v:U", "v:d\nef\nghij", ":", "v:A"},
	{"abcd\nef\nghi|j", "vkjd", "abcd\nef\nghi", "2:2", "v:j", "v:U", ":", "v:j", "v:A"},
	{"abc\n|de", "v$kd", "abce", "0:3", "v:\nd", "v:U", "v:\nd", ":", "v:A"},
	{"ab|c\nd\ne", "vjod", "abe", "0:2", "v:c\nd\n", "v:U", "v:c\nd\n", ":", "v:A"},
	{"ab|c\nd\ne", "vjo<Esc>x", "ab\nd\ne", "0:1", "v:c", "v:U", ":", "v:c", "v:A"},
	{"ab\ncdef|g", "vkhCX<Esc>u", "ab\ncdefg", "1:1", "V:ab\ncdefg\n", "v:U", "V:ab\ncdefg\n", ":", "v:A"},
	{"ab\ncdef|g", "vkCX<Esc>u", "ab\ncdefg", "1:2", "V:ab\ncdefg\n", "v:U", "V:ab\ncdefg\n", ":", "v:A"},
	{"あ\nいうえ|お", "vkd", "あ", "0:0", "v:\nいうえお", "v:U", "v:\nいうえお", ":", "v:A"},
	{"あ\nいう|えお", "vkd", "あお", "0:3", "v:\nいうえ", "v:U", "v:\nいうえ", ":", "v:A"},
	{"あい\nう|えお", "vkd", "あお", "0:3", "v:い\nうえ", "v:U", "v:い\nうえ", ":", "v:A"},
	{"ab|c\nd\ne", "v$od", "abd\ne", "0:2", "v:c\n", "v:U", "v:c\n", ":", "v:A"},
	{"ab|c\nd\ne", "v$ojd", "abce", "0:3", "v:\nd\n", "v:U", "v:\nd\n", ":", "v:A"},
	{"ab\ncdef|g", "Vkvd", "ab", "0:1", "v:\ncdefg", "v:U", "v:\ncdefg", ":", "v:A"},
	{"ab\ncdef|g", "vkVd", "", "0:0", "V:ab\ncdefg\n", "v:U", "V:ab\ncdefg\n", ":", "v:A"},
	{"ab\ncdef|g", "vk<Esc>x", "a\ncdefg", "0:0", "v:b", "v:U", ":", "v:b", "v:A"},
	{"ab|c\nd\ne", "vjjkd", "abe", "0:2", "v:c\nd\n", "v:U", "v:c\nd\n", ":", "v:A"},
	{"abc\n|de", "vk$jd", "abc\n", "1:0", "v:de", "v:U", ":", "v:de", "v:A"},
	// ビジュアルモードの l は、最後の文字から行末の後へ進む。
	{"ab|c\nd", "vld", "abd", "0:2", "v:c\n", "v:U", "v:c\n", ":", "v:A"},
	{"ab|c\nd", "vlld", "abd", "0:2", "v:c\n", "v:U", "v:c\n", ":", "v:A"},
	{"a|bc\nd", "v5ld", "ad", "0:1", "v:bc\n", "v:U", "v:bc\n", ":", "v:A"},
	{"a|bc\nd", "v2ld", "ad", "0:1", "v:bc\n", "v:U", "v:bc\n", ":", "v:A"},
	{"ab\n|\ncd", "vld", "ab\ncd", "1:0", "v:\n", "v:U", "v:\n", ":", "v:A"},
	{"あ|い\nう", "vld", "あう", "0:3", "v:い\n", "v:U", "v:い\n", ":", "v:A"},
	{"ab|c\nd", "vlhd", "ab\nd", "0:1", "v:c", "v:U", ":", "v:c", "v:A"},
	{"ab|c\nde\nf", "vljd", "abf", "0:2", "v:c\nde\n", "v:U", "v:c\nde\n", ":", "v:A"},
	{"ab|c\nd", "vly", "abc\nd", "0:2", "v:c\n", "v:c\n", ":", ":", "v:A"},
	{"ab|c", "vld", "ab", "0:1", "v:c", "v:U", ":", "v:c", "v:A"},
	{"ab|c\nd", "vl<Esc>x", "ab\nd", "0:1", "v:c", "v:U", ":", "v:c", "v:A"},
	// > と < は、選択の終わりの行まで字下げする（改行まで選んでいても次の行は字下げしない）。
	{"ab|c\nd\ne", "vj>", "\tabc\n\td\ne", "0:1", "v:U", "v:U", ":", ":", "v:A"},
	{"ab|c\nd\ne", "vl>", "\tabc\nd\ne", "0:1", "v:U", "v:U", ":", ":", "v:A"},
	{"\tab|c\n\td\n\te", "vj<", "abc\nd\n\te", "0:0", "v:U", "v:U", ":", ":", "v:A"},
	{"ab|c\nd\ne", "v$>", "\tabc\nd\ne", "0:1", "v:U", "v:U", ":", ":", "v:A"},
	{"ab|c\n\ncd", "vj>", "\tabc\n\ncd", "0:1", "v:U", "v:U", ":", ":", "v:A"},
	{"ab|c\nd\ne\nf", "vj>.", "\t\tabc\n\t\td\ne\nf", "0:2", "v:U", "v:U", ":", ":", "v:A"},
	{"ab|c\nd\ne\nf\ng", "vj>j.", "\tabc\n\t\td\n\te\nf\ng", "1:2", "v:U", "v:U", ":", ":", "v:A"},
	{"ab|c\nd\ne", "vj>u", "abc\nd\ne", "0:2", "v:U", "v:U", ":", ":", "v:A"},
	// 複数の行の選択のドットリピートは、終わりの列が繰り返す先の行の長さ以上のときだけ、その行の改行まで消す。
	{"ab|c\nx\nfoo\nbar\nbaz\nqux", "vjdj.", "abfoo\nbaz\nqux", "1:2", "v:r\nba", "v:U", "v:r\nba", ":", "v:A"},
	{"ab|c\nx\nfoo\nbar\nbazzz\nqux", "vjdj.", "abfoo\nbazzz\nqux", "1:2", "v:r\nba", "v:U", "v:r\nba", ":", "v:A"},
	{"ab|c\nx\nfoo\nbar\nb\nqux", "vjdj.", "abfoo\nbaqux", "1:2", "v:r\nb\n", "v:U", "v:r\nb\n", ":", "v:A"},
	{"ab|c\nx\nfoo\nbar\nbaz\nqux", "v$jdj.", "abfoo\nbaqux", "1:2", "v:r\nbaz\n", "v:U", "v:r\nbaz\n", ":", "v:A"},
	{"ab|c\nx\nfoo\nbar\nbaz\nqux", "vjoldj.", "abcfoo\nbaz\nqux", "1:2", "v:r\nba", "v:U", "v:r\nba", ":", "v:A"},
	{"abc\nd|e\nfoo\nbar\nbaz\nqux", "vkdj.", "a\nr\nbaz\nqux", "1:0", "v:foo\nba", "v:U", "v:foo\nba", ":", "v:A"},
}

// ビジュアルモードの SetCursor（拡張機能の編集の後に呼び出し側が置き直すもの）は、行末の後のカーソルを丸めない。
// Vim で vj の後に <Cmd>call setline(3, 'E')<CR> を挟んで d を打つと、c と d と改行を消して abE になる。
func TestVisualSetCursorKeepsEOL(t *testing.T) {
	s := setup(t, "ab|c\nd\ne")
	feedKeys(s, "vj")
	s.SetCursor(s.Cursor())
	if got := s.Cursor().String(); got != "1:1" {
		t.Fatalf("cursor = %s, want 1:1", got)
	}
	feedKeys(s, "d")
	if got := s.buf.Text(); got != "abe" {
		t.Errorf("text = %q, want %q", got, "abe")
	}
}

// 文字単位の選択の端が行末の後にあるとき、オペレーターが働く範囲が Vim 9.1 と一致する。
//
// req: FR-01, FR-02
func TestVimVisualEOL(t *testing.T) {
	runVimCases(t, visualEOLCases)
}
