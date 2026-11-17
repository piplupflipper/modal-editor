package ex

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/piplupflipper/modal-editor/internal/buffer"
)

// lines10 は、"l1" から "l10" までの 10 行のテキストである。
const lines10 = "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\nl10\n"

// errCode は、err が *Error ならその Code を、nil なら "" を返す。
func errCode(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		return ""
	}
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("error is not *Error: %v", err)
	}
	return e.Code
}

// req: FR-08
func TestParseRange(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		cursor    int // 0 始まり
		cmdline   string
		line1     int // 1 始まり
		line2     int
		count     int
		wantRest  string
		wantCode  string
		wantPos   int
		checkPos  bool
		wantSaved string // 検索で記録された直前のパターン
	}{
		{name: "省略", text: lines10, cursor: 2, cmdline: "d", count: 0, wantRest: "d"},
		{name: "行番号", text: lines10, cmdline: "3", line1: 3, line2: 3, count: 1},
		{name: "2 つの行番号", text: lines10, cmdline: "2,4d", line1: 2, line2: 4, count: 2, wantRest: "d"},
		{name: "空白を挟む", text: lines10, cmdline: "2 , 4 d", line1: 2, line2: 4, count: 2, wantRest: "d"},
		{name: "現在の行と最後の行", text: lines10, cursor: 4, cmdline: ".,$", line1: 5, line2: 10, count: 2},
		{name: "全体", text: lines10, cmdline: "%s/a/b/", line1: 1, line2: 10, count: 2, wantRest: "s/a/b/"},
		{name: "相対", text: lines10, cursor: 4, cmdline: ".-2,.+3", line1: 3, line2: 8, count: 2},
		{name: "基点を省いた相対", text: lines10, cursor: 4, cmdline: "-,+2", line1: 4, line2: 7, count: 2},
		{name: "相対を重ねる", text: lines10, cursor: 0, cmdline: "$-1-1", line1: 8, line2: 8, count: 1},
		{name: "前のアドレスの省略", text: lines10, cursor: 1, cmdline: ",4", line1: 2, line2: 4, count: 2},
		{name: "後のアドレスの省略", text: lines10, cursor: 5, cmdline: "2,", line1: 2, line2: 6, count: 2},
		{name: "前方の検索", text: lines10, cursor: 0, cmdline: "/l5/", line1: 5, line2: 5, count: 1, wantSaved: "l5"},
		{name: "前方の検索は次の行から", text: "a\nx\na\n", cursor: 0, cmdline: "/a/", line1: 3, line2: 3, count: 1},
		{name: "前方の検索は折り返す", text: "a\nx\ny\n", cursor: 1, cmdline: "/a", line1: 1, line2: 1, count: 1},
		{name: "後方の検索", text: "a\nx\na\nb\n", cursor: 3, cmdline: "?a?", line1: 3, line2: 3, count: 1},
		{name: "検索と相対", text: lines10, cmdline: "/l3/+1,/l3/+2", line1: 4, line2: 5, count: 2},
		{name: "検索の区切り文字の退避", text: "a/b\nc\n", cursor: 1, cmdline: `/a\/b/`, line1: 1, line2: 1, count: 1, wantSaved: "a/b"},
		{name: "セミコロンは現在の行を移す", text: lines10, cursor: 0, cmdline: "5;+2", line1: 5, line2: 7, count: 2},
		{name: "3 つ以上は最後の 2 つ", text: lines10, cmdline: "1,2,3", line1: 2, line2: 3, count: 3},
		{name: "0 行目", text: lines10, cmdline: "0", line1: 0, line2: 0, count: 1},
		// 行数を超えた行番号は、範囲の解析では誤りにしない（丸めるか E16 にするかは使う側が決める）。
		{name: "バッファの外", text: lines10, cmdline: "11", line1: 11, line2: 11, count: 1},
		{name: "途中でバッファの外に出る相対", text: lines10, cmdline: "11-3", line1: 8, line2: 8, count: 1},
		{name: "セミコロンはバッファの外の行を最後の行に丸めて移す", text: lines10, cursor: 0, cmdline: "20;-1", line1: 20, line2: 9, count: 2},
		{name: "負の行", text: lines10, cursor: 0, cmdline: "1,.-2d", wantCode: "E16", wantPos: 2, checkPos: true},
		{name: "検索の該当なし", text: lines10, cmdline: "/zz/", wantCode: "E486"},
		{name: "検索の不正な正規表現", text: lines10, cmdline: "/a(/", wantCode: "E383", wantPos: 0, checkPos: true},
		{name: "マーク", text: lines10, cmdline: "'<,'>d", line1: 3, line2: 5, count: 2, wantRest: "d"},
		{name: "マークと相対", text: lines10, cmdline: "'a+1", line1: 8, line2: 8, count: 1},
		{name: "設定されていないマーク", text: lines10, cmdline: "'b", wantCode: "E20"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			marks := map[rune]int{'<': 2, '>': 4, 'a': 6}
			st := &State{Mark: func(name rune) (int, bool) {
				l, ok := marks[name]
				return l, ok
			}}
			c := &execCtx{b: buffer.New(tt.text), st: st, cursor: tt.cursor}
			s := &scanner{src: tt.cmdline}
			r, err := c.parseRange(s, addrLines)
			if code := errCode(t, err); code != tt.wantCode {
				t.Fatalf("code = %q, want %q (err = %v)", code, tt.wantCode, err)
			}
			if err != nil {
				var e *Error
				errors.As(err, &e)
				if tt.checkPos && e.Pos != tt.wantPos {
					t.Errorf("pos = %d, want %d", e.Pos, tt.wantPos)
				}
				return
			}
			if r.count != tt.count {
				t.Errorf("count = %d, want %d", r.count, tt.count)
			}
			if tt.count > 0 && (r.line1 != tt.line1 || r.line2 != tt.line2) {
				t.Errorf("range = %d,%d, want %d,%d", r.line1, r.line2, tt.line1, tt.line2)
			}
			s.skipBlanks()
			if s.rest() != tt.wantRest {
				t.Errorf("rest = %q, want %q", s.rest(), tt.wantRest)
			}
			if tt.wantSaved != "" && c.st.LastPattern != tt.wantSaved {
				t.Errorf("LastPattern = %q, want %q", c.st.LastPattern, tt.wantSaved)
			}
		})
	}
}

// req: FR-08, FR-09
func TestExecuteEdit(t *testing.T) {
	tests := []struct {
		name       string
		text       string
		cursor     int
		cmdline    string
		want       string // 実行した後の Text()。誤りのときは変わらないことを確かめる
		wantCursor int
		wantCode   string
		wantErr    bool // 番号の無い通知（wantCode が空）を誤りとして期待する
	}{
		// :s
		{name: "全体の置換", text: "aa\nba\nc\n", cmdline: ":%s/a/b/g", want: "bb\nbb\nc", wantCursor: 1},
		{name: "g が無ければ行の最初だけ", text: "aa\naa\n", cmdline: "%s/a/b/", want: "ba\nba", wantCursor: 1},
		{name: "範囲を省くと現在の行", text: "a\na\na\n", cursor: 1, cmdline: "s/a/b/", want: "a\nb\na", wantCursor: 1},
		{name: "後方参照", text: "foo=bar\n", cmdline: `s/(\w+)=(\w+)/\2=\1/`, want: "bar=foo"},
		{name: "& と \\0 は一致の全体", text: "ab\n", cmdline: `s/b/[&\0]/`, want: "a[bb]"},
		{name: "\\& は & そのもの", text: "ab\n", cmdline: `s/b/\&/`, want: "a&"},
		{name: "$ は Go の記法として解釈しない", text: "ab\n", cmdline: `s/(b)/$1/`, want: "a$1"},
		{name: "Go の正規表現の文法", text: "x12y345\n", cmdline: `s/\d+/N/g`, want: "xNyN"},
		{name: "大文字と小文字を区別しない", text: "AbA\n", cmdline: "s/a/x/gi", want: "xbx"},
		{name: "区切り文字の変更", text: "/usr/bin\n", cmdline: "s#/usr#/opt#", want: "/opt/bin"},
		{name: "区切り文字の退避", text: "a/b\n", cmdline: `s/\//-/`, want: "a-b"},
		{name: "置換の文字列を省くと消す", text: "abc\n", cmdline: "s/b", want: "ac"},
		{name: "改行で行を分ける", text: "a,b\nc,d\n", cmdline: `%s/,/\r/`, want: "a\nb\nc\nd", wantCursor: 3},
		{name: "多バイト文字", text: "あいう\n", cmdline: "s/い/イ/", want: "あイう"},
		{name: "空の一致", text: "abc\n", cmdline: "s/x*/-/g", want: "-a-b-c-"},
		{name: "回数", text: lines10, cmdline: "2s/l/L/ 3", want: "l1\nL2\nL3\nL4\nl5\nl6\nl7\nl8\nl9\nl10", wantCursor: 3},
		{name: "該当なし", text: "abc\n", cmdline: "%s/x/y/", want: "abc", wantCode: "E486"},
		{name: "該当なしを e で抑える", text: "abc\n", cmdline: "%s/x/y/e", want: "abc"},
		{name: "不正な正規表現", text: "abc\n", cmdline: "s/a(/b/", want: "abc", wantCode: "E383"},
		{name: "英字の区切り文字", text: "abc\n", cmdline: "s a b ", want: "abc", wantCode: "E146"},
		{name: "未対応のフラグ", text: "abc\n", cmdline: "s/a/b/c", want: "abc", wantCode: "E319"},
		{name: "未対応の置換の変換", text: "abc\n", cmdline: `s/a/\u&/`, want: "abc", wantCode: "E319"},
		{name: "空のパターンは直前のパターン", text: "ab\n", cmdline: "s//x/", want: "ab", wantCode: "E35"},

		// :g と :v
		{name: ":g/pat/d", text: "a1\nb\na2\nc\na3\n", cmdline: "g/a/d", want: "b\nc", wantCursor: 1},
		{name: ":v/pat/d", text: "a1\nb\na2\nc\na3\n", cmdline: "v/a/d", want: "a1\na2\na3", wantCursor: 2},
		{name: ":g!/pat/d", text: "a1\nb\na2\n", cmdline: "g!/a/d", want: "a1\na2", wantCursor: 1},
		{name: ":g で全ての行を消す", text: "a\na\n", cmdline: "g/a/d", want: ""},
		{name: ":g で連続する行を消す", text: "x\na\na\nb\n", cmdline: "g/a/.,+1d", want: "x\nb", wantCursor: 1},
		{name: ":g の中の :s は空のパターンで :g のパターンを使う", text: "a1\nb\na2\n", cmdline: "g/a/s//x/", want: "x1\nb\nx2", wantCursor: 2},
		{name: ":g の中の :s が行を増やす", text: "a,b\nc\na,d\n", cmdline: `g/,/s/,/\r/`, want: "a\nb\nc\na\nd", wantCursor: 4},
		{name: ":g の範囲", text: "a\na\na\na\n", cmdline: "2,3g/a/d", want: "a\na", wantCursor: 1},
		{name: ":g の区切り文字の変更", text: "a/b\nc\n", cmdline: "g#/#d", want: "c"},
		{name: ":g の cmd を省く", text: "a\nb\na\nb\n", cmdline: "g/a/", want: "a\nb\na\nb", wantCursor: 2},
		{name: ":g の該当なし", text: "a\n", cmdline: "g/x/d", want: "a", wantCode: "E486"},
		{name: ":v の該当なし", text: "a\n", cmdline: "v/a/d", want: "a", wantErr: true},
		{name: ":g の入れ子", text: "a\n", cmdline: "g/a/g/a/d", want: "a", wantCode: "E147"},
		{name: ":g の中で :q は使えない", text: "a\n", cmdline: "g/a/q", want: "a", wantCode: "E319"},

		// :d
		{name: ":2,4d", text: lines10, cmdline: "2,4d", want: "l1\nl5\nl6\nl7\nl8\nl9\nl10", wantCursor: 1},
		{name: ":d は現在の行", text: "a\nb\nc\n", cursor: 1, cmdline: "d", want: "a\nc", wantCursor: 1},
		{name: ":d で最後の行", text: "a\nb\nc\n", cursor: 2, cmdline: "d", want: "a\nb", wantCursor: 1},
		{name: ":%d", text: "a\nb\n", cmdline: "%d", want: ""},
		{name: ":d の回数", text: lines10, cmdline: "3d 2", want: "l1\nl2\nl5\nl6\nl7\nl8\nl9\nl10", wantCursor: 2},
		{name: ":d の回数は最後の行で止まる", text: "a\nb\nc\n", cmdline: "2d 9", want: "a"},
		{name: ":d の逆順の範囲は確認を求める", text: lines10, cmdline: "4,2d", want: strings.TrimSuffix(lines10, "\n"), wantCode: "E493"},
		{name: ":d の余分な文字", text: "a\n", cmdline: "d a b", want: "a", wantCode: "E488"},
		{name: ":d ! は書き込めないレジスター", text: "a\nb\n", cmdline: "d !", want: "a\nb", wantCode: "E488"},
		// req: FR-50
		{name: ":d - は無い名前のレジスター", text: "a\nb\n", cmdline: "d -", want: "a\nb", wantCode: "E488"},
		{name: ":d + は無い名前のレジスター", text: "a\nb\n", cmdline: "d +", want: "a\nb", wantCode: "E488"},

		// | による連結
		{name: ":d|d", text: "a\nb\nc\n", cmdline: "d|d", want: "c"},
		{name: ":2|d", text: "a\nb\nc\n", cmdline: "2|d", want: "a\nc", wantCursor: 1},
		{name: ":s の後の |", text: "a\nb\n", cmdline: "s/a/x/ | 2d", want: "x", wantCursor: 0},
		{name: ":s のパターンの中の | は区切りではない", text: "a|b\n", cmdline: "s/a|b/x/g", want: "x|x"},
		{name: ":s の回数の後の |", text: "a\na\na\n", cmdline: "s/a/x/ 2|3d", want: "x\nx", wantCursor: 1},
		{name: ":g の cmd の中の |", text: "a1\nb\na2\n", cmdline: "g/a/s/a/x/|s/x/y/", want: "y1\nb\ny2", wantCursor: 2},
		{name: "| の後の誤り", text: "a\nb\n", cmdline: "d|foo", want: "b", wantCode: "E492"},

		// 移動
		{name: ":3", text: lines10, cmdline: ":3", want: strings.TrimSuffix(lines10, "\n"), wantCursor: 2},
		{name: ":0 は 1 行目", text: lines10, cursor: 5, cmdline: "0", want: strings.TrimSuffix(lines10, "\n")},
		{name: ":$", text: lines10, cmdline: "$", want: strings.TrimSuffix(lines10, "\n"), wantCursor: 9},
		{name: ":/pat/", text: lines10, cmdline: "/l7/", want: strings.TrimSuffix(lines10, "\n"), wantCursor: 6},
		{name: "空のコマンド", text: lines10, cursor: 4, cmdline: ":", want: strings.TrimSuffix(lines10, "\n"), wantCursor: 4},

		// 誤り
		{name: "未知のコマンド", text: "a\n", cmdline: "foo", want: "a", wantCode: "E492"},
		{name: "英字でないコマンド", text: "a\n", cmdline: "1#", want: "a", wantCode: "E492"},
		{name: "範囲を取らないコマンド", text: "a\n", cmdline: "1e", want: "a", wantCode: "E481"},
		{name: ":w の一部の行", text: "a\nb\n", cmdline: "2w foo", want: "a\nb", wantCode: "E319"},
		{name: "! を取らないコマンド", text: "a\n", cmdline: "d!", want: "a", wantCode: "E477"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := buffer.New(tt.text)
			res, err := Execute(b, nil, tt.cursor, tt.cmdline)
			if code := errCode(t, err); code != tt.wantCode || (err != nil) != (tt.wantCode != "" || tt.wantErr) {
				t.Fatalf("code = %q, want %q (err = %v)", code, tt.wantCode, err)
			}
			if got := b.Text(); got != tt.want {
				t.Errorf("text = %q, want %q", got, tt.want)
			}
			if err == nil && res.Cursor != tt.wantCursor {
				t.Errorf("cursor = %d, want %d", res.Cursor, tt.wantCursor)
			}
		})
	}
}

// 行番号が行数を超えたときの扱いを、Vim 9.1 と突き合わせる（issue #54）。
// 表は、3 行のバッファ "a1\na2\na3" の 2 行目にカーソルを置いて、各コマンドを Vim 9.1 で実行した結果である。
// Vim は "vim -Nu NONE -n --not-a-term -c <cmd>" で、Ex モードでない状態で実行した（2026-11-16）。
// コマンド名の無い範囲（:10、:1,10）は最後の行へ移り、コマンドに付けた範囲（:10d）と "|" が続く範囲（:10|）は E16 になる。
//
// req: FR-08
func TestLineOverflowVim(t *testing.T) {
	tests := []struct {
		cmdline    string
		wantCode   string
		wantCursor int // 0 始まり。誤りのときは見ない
		want       string
	}{
		{cmdline: "4", wantCode: "", wantCursor: 2, want: "a1\na2\na3"},
		{cmdline: "10", wantCode: "", wantCursor: 2, want: "a1\na2\na3"},
		{cmdline: "$+1", wantCode: "", wantCursor: 2, want: "a1\na2\na3"},
		{cmdline: ".+5", wantCode: "", wantCursor: 2, want: "a1\na2\na3"},
		{cmdline: "0+5", wantCode: "", wantCursor: 2, want: "a1\na2\na3"},
		{cmdline: "$+1-1", wantCode: "", wantCursor: 2, want: "a1\na2\na3"},
		{cmdline: "4-2", wantCode: "", wantCursor: 1, want: "a1\na2\na3"},
		{cmdline: "%", wantCode: "", wantCursor: 2, want: "a1\na2\na3"},
		{cmdline: "3,4", wantCode: "", wantCursor: 2, want: "a1\na2\na3"},
		{cmdline: "1,4", wantCode: "", wantCursor: 2, want: "a1\na2\na3"},
		{cmdline: "5,4", wantCode: "", wantCursor: 2, want: "a1\na2\na3"},
		{cmdline: "4,1", wantCode: "", wantCursor: 0, want: "a1\na2\na3"},
		{cmdline: "4,", wantCode: "", wantCursor: 1, want: "a1\na2\na3"},
		{cmdline: ",4", wantCode: "", wantCursor: 2, want: "a1\na2\na3"},
		{cmdline: "4;5", wantCode: "", wantCursor: 2, want: "a1\na2\na3"},
		{cmdline: "10;", wantCode: "", wantCursor: 2, want: "a1\na2\na3"},
		{cmdline: "4;-1", wantCode: "", wantCursor: 1, want: "a1\na2\na3"},
		{cmdline: "4;+0", wantCode: "", wantCursor: 2, want: "a1\na2\na3"},
		{cmdline: "10;/a/", wantCode: "", wantCursor: 0, want: "a1\na2\na3"},
		{cmdline: "/a3/+1", wantCode: "", wantCursor: 2, want: "a1\na2\na3"},
		{cmdline: "?a1?+5", wantCode: "", wantCursor: 2, want: "a1\na2\na3"},
		{cmdline: "g/a/10", wantCode: "", wantCursor: 2, want: "a1\na2\na3"},
		{cmdline: "4\"x", wantCode: "", wantCursor: 2, want: "a1\na2\na3"},
		{cmdline: "3|", wantCode: "", wantCursor: 2, want: "a1\na2\na3"},
		{cmdline: "2,3|", wantCode: "", wantCursor: 2, want: "a1\na2\na3"},
		{cmdline: "%|", wantCode: "", wantCursor: 2, want: "a1\na2\na3"},
		{cmdline: "3d", wantCode: "", wantCursor: 1, want: "a1\na2"},
		{cmdline: "4-2d", wantCode: "", wantCursor: 1, want: "a1\na3"},
		{cmdline: "4-1,3d", wantCode: "", wantCursor: 1, want: "a1\na2"},
		{cmdline: "10,1,2d", wantCode: "", wantCursor: 0, want: "a3"},
		{cmdline: "2d 10", wantCode: "", wantCursor: 0, want: "a1"},
		{cmdline: "s/a/b/ 10", wantCode: "", wantCursor: 2, want: "a1\nb2\nb3"},
		{cmdline: "%s/a/b/ 10", wantCode: "", wantCursor: 2, want: "a1\na2\nb3"},
		{cmdline: "4|", wantCode: "E16", wantCursor: 1, want: "a1\na2\na3"},
		{cmdline: "4 |", wantCode: "E16", wantCursor: 1, want: "a1\na2\na3"},
		{cmdline: "10|3", wantCode: "E16", wantCursor: 1, want: "a1\na2\na3"},
		{cmdline: "4,1|", wantCode: "E16", wantCursor: 1, want: "a1\na2\na3"},
		{cmdline: "4|s/a/b/", wantCode: "E16", wantCursor: 1, want: "a1\na2\na3"},
		{cmdline: "g/a2/10|s/a/b/", wantCode: "E16", wantCursor: 1, want: "a1\na2\na3"},
		{cmdline: "4d", wantCode: "E16", wantCursor: 1, want: "a1\na2\na3"},
		{cmdline: "$+1d", wantCode: "E16", wantCursor: 1, want: "a1\na2\na3"},
		{cmdline: "1,4d", wantCode: "E16", wantCursor: 1, want: "a1\na2\na3"},
		{cmdline: "2,10d", wantCode: "E16", wantCursor: 1, want: "a1\na2\na3"},
		{cmdline: "4s/a/b/", wantCode: "E16", wantCursor: 1, want: "a1\na2\na3"},
		{cmdline: "1,4s/a/b/", wantCode: "E16", wantCursor: 1, want: "a1\na2\na3"},
		{cmdline: "4g/a/d", wantCode: "E16", wantCursor: 1, want: "a1\na2\na3"},
		{cmdline: "1,4g/a/d", wantCode: "E16", wantCursor: 1, want: "a1\na2\na3"},
		{cmdline: "-5", wantCode: "E16", wantCursor: 1, want: "a1\na2\na3"},
		{cmdline: "0-1", wantCode: "E16", wantCursor: 1, want: "a1\na2\na3"},
		{cmdline: "1,.-5", wantCode: "E16", wantCursor: 1, want: "a1\na2\na3"},
	}
	for _, tt := range tests {
		t.Run(tt.cmdline, func(t *testing.T) {
			b := buffer.New("a1\na2\na3\n")
			res, err := Execute(b, nil, 1, tt.cmdline)
			if code := errCode(t, err); code != tt.wantCode {
				t.Fatalf("code = %q, want %q (err = %v)", code, tt.wantCode, err)
			}
			if got := b.Text(); got != tt.want {
				t.Errorf("text = %q, want %q", got, tt.want)
			}
			if err == nil && res.Cursor != tt.wantCursor {
				t.Errorf("cursor = %d, want %d", res.Cursor, tt.wantCursor)
			}
		})
	}
}

// 範囲の誤りの扱いを、Vim 9.1 と突き合わせる（issue #57）。逆順の範囲、:w と :q の範囲、誤りの後の ";" のカーソルを見る。
// 表は、5 行のバッファの 3 行目の 6 バイト目にカーソルを置き、変更済みの状態で各コマンドを Vim 9.1 で実行した結果である。
// Vim は "vim -Nu NONE -n --not-a-term -c <cmd>" で、Ex モードでない状態で実行した（2026-11-16）。
// -c で実行した Vim は逆順の範囲で確認を求めずに E493 にするので、表の E493 は確認を求める場合に当たる（TestBackwardsRangeConfirm）。
// :%w のようにファイル名の無い書き出しは、Vim では E32（ファイル名が無い）になるが、範囲の扱いは通ったので、表では誤り無しとした。
// wantLine と wantCol は誤りのときも見る。wantCol は Move が CursorKeepColumn のときだけ意味を持ち、ここでは Move の種類で確かめる。
//
// req: FR-08
func TestRangeErrorsVim(t *testing.T) {
	const text = "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne\n"
	tests := []struct {
		cmdline  string
		wantCode string
		wantLine int // 0 始まり
		wantCol  int // 0 始まりのバイト位置
		want     string
	}{
		{cmdline: "4,2d", wantCode: "E493", wantLine: 2, wantCol: 5, want: "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne"},
		{cmdline: "9,2d", wantCode: "E493", wantLine: 2, wantCol: 5, want: "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne"},
		{cmdline: "1;4,2d", wantCode: "E493", wantLine: 0, wantCol: 5, want: "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne"},
		{cmdline: "4,2s/a/x/", wantCode: "E493", wantLine: 2, wantCol: 5, want: "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne"},
		{cmdline: "4,2g/a/d", wantCode: "E493", wantLine: 2, wantCol: 5, want: "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne"},
		{cmdline: "4,2w", wantCode: "E493", wantLine: 2, wantCol: 5, want: "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne"},
		{cmdline: "2,1q", wantCode: "E493", wantLine: 2, wantCol: 5, want: "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne"},
		{cmdline: "4,2d|1d", wantCode: "E493", wantLine: 2, wantCol: 5, want: "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne"},
		{cmdline: "g/a/4,2d", wantCode: "E16", wantLine: 0, wantCol: 0, want: "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne"},
		{cmdline: "4,2", wantCode: "", wantLine: 1, wantCol: 2, want: "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne"},
		{cmdline: "1;4,2", wantCode: "", wantLine: 1, wantCol: 2, want: "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne"},
		{cmdline: "4,2|", wantCode: "E16", wantLine: 2, wantCol: 5, want: "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne"},
		{cmdline: "3,1|", wantCode: "E16", wantLine: 2, wantCol: 5, want: "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne"},
		{cmdline: "6w", wantCode: "E16", wantLine: 2, wantCol: 5, want: "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne"},
		{cmdline: "6wq", wantCode: "E16", wantLine: 2, wantCol: 5, want: "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne"},
		{cmdline: "6x", wantCode: "E16", wantLine: 2, wantCol: 5, want: "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne"},
		{cmdline: "4q", wantCode: "E16", wantLine: 2, wantCol: 5, want: "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne"},
		{cmdline: "2q", wantCode: "E16", wantLine: 2, wantCol: 5, want: "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne"},
		{cmdline: "1q", wantCode: "E37", wantLine: 2, wantCol: 5, want: "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne"},
		{cmdline: "0q", wantCode: "E37", wantLine: 2, wantCol: 5, want: "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne"},
		{cmdline: "$q", wantCode: "E37", wantLine: 2, wantCol: 5, want: "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne"},
		{cmdline: ".q", wantCode: "E37", wantLine: 2, wantCol: 5, want: "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne"},
		{cmdline: ",q", wantCode: "E37", wantLine: 2, wantCol: 5, want: "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne"},
		{cmdline: "1,1q", wantCode: "E37", wantLine: 2, wantCol: 5, want: "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne"},
		{cmdline: ".+1q", wantCode: "E16", wantLine: 2, wantCol: 5, want: "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne"},
		{cmdline: "%q", wantCode: "E16", wantLine: 2, wantCol: 5, want: "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne"},
		{cmdline: "/a/q", wantCode: "E16", wantLine: 2, wantCol: 5, want: "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne"},
		{cmdline: "'aq", wantCode: "E16", wantLine: 2, wantCol: 5, want: "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne"},
		{cmdline: "1,2q", wantCode: "E16", wantLine: 2, wantCol: 5, want: "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne"},
		{cmdline: "2;3q", wantCode: "E16", wantLine: 1, wantCol: 5, want: "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne"},
		{cmdline: "2e", wantCode: "E481", wantLine: 2, wantCol: 5, want: "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne"},
		{cmdline: "%w", wantCode: "", wantLine: 2, wantCol: 5, want: "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne"},
		{cmdline: "1,5w", wantCode: "", wantLine: 2, wantCol: 5, want: "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne"},
		{cmdline: "0,$w", wantCode: "", wantLine: 2, wantCol: 5, want: "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne"},
		{cmdline: "2;9d", wantCode: "E16", wantLine: 1, wantCol: 5, want: "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne"},
		{cmdline: "4;9d", wantCode: "E16", wantLine: 3, wantCol: 3, want: "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne"},
		{cmdline: "5;9d", wantCode: "E16", wantLine: 4, wantCol: 0, want: "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne"},
		{cmdline: "2;4s/zzz/y/e", wantCode: "", wantLine: 1, wantCol: 5, want: "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne"},
		{cmdline: "2;4s/zzz/y/", wantCode: "E486", wantLine: 1, wantCol: 5, want: "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne"},
		{cmdline: "4;/zzz/d", wantCode: "E486", wantLine: 3, wantCol: 3, want: "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne"},
		{cmdline: "2;9|", wantCode: "E16", wantLine: 1, wantCol: 5, want: "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne"},
		{cmdline: "2;", wantCode: "", wantLine: 1, wantCol: 2, want: "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne"},
		{cmdline: "2;3", wantCode: "", wantLine: 2, wantCol: 2, want: "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne"},
		{cmdline: "4;", wantCode: "", wantLine: 3, wantCol: 2, want: "  aaaa\n  bbbbbbb\n  ccccccc\n  dd\ne"},
		{cmdline: "2;3d", wantCode: "", wantLine: 1, wantCol: 2, want: "  aaaa\n  dd\ne"},
	}
	for _, tt := range tests {
		t.Run(tt.cmdline, func(t *testing.T) {
			b := buffer.New(text)
			// Vim の setline と同じく変更済みにする（:q が E37 になる）。
			if _, err := b.Apply(buffer.Transaction{BaseVersion: b.Version(), Edits: []buffer.Edit{{Range: buffer.Range{Start: buffer.Position{Line: 4}, End: buffer.Position{Line: 4, Byte: 1}}, Text: "e"}}}); err != nil {
				t.Fatal(err)
			}
			res, err := Execute(b, nil, 2, tt.cmdline)
			if code := errCode(t, err); code != tt.wantCode {
				t.Fatalf("code = %q, want %q (err = %v)", code, tt.wantCode, err)
			}
			if got := b.Text(); got != tt.want {
				t.Errorf("text = %q, want %q", got, tt.want)
			}
			if res.Cursor != tt.wantLine {
				t.Errorf("cursor = %d, want %d", res.Cursor, tt.wantLine)
			}
			// 列は呼び出し側が決めるので、Move の種類が Vim の列と合うかを見る。
			var col int
			line := b.Line(res.Cursor)
			switch res.Move {
			case CursorUnchanged, CursorKeepColumn:
				col = min(5, max(len(line)-1, 0))
			case CursorFirstNonBlank:
				col = len(line) - len(strings.TrimLeft(line, " \t"))
			case CursorLineStart:
				col = 0
			}
			if col != tt.wantCol {
				t.Errorf("col = %d (move = %d), want %d", col, res.Move, tt.wantCol)
			}
		})
	}
}

// 逆順の範囲は E493 と Result.Confirm を返し、Confirm.Resume で入れ替えて続きを実行する。
// 期待値は Vim 9.1 で、対話のコマンドラインから打ち、確認に y と答えた結果である（feedkeys で入力、2026-11-16）。
//
// req: FR-08
func TestBackwardsRangeConfirm(t *testing.T) {
	tests := []struct {
		cmdline    string
		cursor     int
		want       string
		wantCursor int
		wantCode   string // Resume の誤り
	}{
		// Vim 9.1: ":4,2d|1d" に y と答えると、2 行目から 4 行目を消した後に 1 行目を消す。
		{cmdline: "4,2d|1d", cursor: 2, want: "e", wantCursor: 0},
		// "." は、";" で移す前のカーソルの行で評価し直す（.-1;.-2 は 2;0 で、入れ替えると 0,2 になり 1 行目から消す）。
		{cmdline: ".-1;.-2d", cursor: 2, want: "c\nd\ne", wantCursor: 0},
		// 入れ替えた後に行数を超えていれば E16 にする（:d で 4 行になった後の 5,4d）。
		{cmdline: "d|5,4d", cursor: 0, want: "b\nc\nd\ne", wantCursor: 0, wantCode: "E16"},
	}
	for _, tt := range tests {
		t.Run(tt.cmdline, func(t *testing.T) {
			b := buffer.New("a\nb\nc\nd\ne\n")
			st := &State{}
			res, err := Execute(b, st, tt.cursor, tt.cmdline)
			if code := errCode(t, err); code != CodeBackwardsRange {
				t.Fatalf("code = %q, want %q (err = %v)", code, CodeBackwardsRange, err)
			}
			if res.Confirm == nil || res.Confirm.Prompt == "" {
				t.Fatalf("Confirm = %+v, want a prompt", res.Confirm)
			}
			res, err = res.Confirm.Resume(b, st)
			if code := errCode(t, err); code != tt.wantCode {
				t.Fatalf("Resume code = %q, want %q (err = %v)", code, tt.wantCode, err)
			}
			if res.Confirm != nil {
				t.Errorf("Confirm after Resume = %+v, want nil", res.Confirm)
			}
			if got := b.Text(); got != tt.want {
				t.Errorf("text = %q, want %q", got, tt.want)
			}
			if res.Cursor != tt.wantCursor {
				t.Errorf("cursor = %d, want %d", res.Cursor, tt.wantCursor)
			}
		})
	}

	t.Run("続くコマンドがまた確認を求める", func(t *testing.T) {
		b := buffer.New("a\nb\nc\nd\ne\n")
		res, err := Execute(b, nil, 0, "2,1d|3,2d")
		if errCode(t, err) != CodeBackwardsRange || res.Confirm == nil {
			t.Fatalf("err = %v, Confirm = %v", err, res.Confirm)
		}
		res, err = res.Confirm.Resume(b, nil)
		if errCode(t, err) != CodeBackwardsRange || res.Confirm == nil {
			t.Fatalf("err = %v, Confirm = %v", err, res.Confirm)
		}
		if got := b.Text(); got != "c\nd\ne" {
			t.Errorf("text = %q", got)
		}
		if _, err := res.Confirm.Resume(b, nil); err != nil {
			t.Fatal(err)
		}
		if got := b.Text(); got != "c" {
			t.Errorf("text = %q", got)
		}
	})

	t.Run(":g の中は確認を求めない", func(t *testing.T) {
		b := buffer.New("a\nb\nc\n")
		res, err := Execute(b, nil, 0, "g/a/3,2d")
		if errCode(t, err) != "E16" || res.Confirm != nil {
			t.Fatalf("err = %v, Confirm = %v", err, res.Confirm)
		}
	})
}

// :v で全ての行が一致したときは、Vim と同じく番号の無い通知を誤りとして返す。
//
// req: FR-08
func TestGlobalInvertAllMatch(t *testing.T) {
	_, err := Execute(buffer.New("a\n"), nil, 0, "v/a/d")
	var e *Error
	if !errors.As(err, &e) || e.Code != "" || !strings.Contains(e.Msg, "全ての行") {
		t.Fatalf("err = %v, want notice without code", err)
	}
}

// :g の多数の削除は、アンドゥ 1 回で戻る。
//
// req: FR-08
func TestGlobalUndoOnce(t *testing.T) {
	text := "a\nb\na\nb\na\n"
	b := buffer.New(text)
	res, err := Execute(b, nil, 0, "g/a/d")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changes) != 3 {
		t.Errorf("changes = %d, want 3", len(res.Changes))
	}
	if len(res.Deletions) != 3 || res.Deletions[0].Lines[0] != "a" {
		t.Errorf("deletions = %+v", res.Deletions)
	}
	if _, ok := b.Undo(); !ok {
		t.Fatal("undo failed")
	}
	if got := b.Text(); got != strings.TrimSuffix(text, "\n") {
		t.Errorf("after undo = %q", got)
	}
	if b.CanUndo() {
		t.Error("CanUndo = true after one undo")
	}
}

// Result.Changes を順に適用し直すと、実行した後のテキストになる（拡張機能へ送る差分として使える）。
//
// req: FR-08
func TestChangesReplay(t *testing.T) {
	text := "a,b\nc\na,d\n"
	b := buffer.New(text)
	res, err := Execute(b, nil, 0, `g/,/s/,/\r/`)
	if err != nil {
		t.Fatal(err)
	}
	replay := buffer.New(text)
	for _, ch := range res.Changes {
		if _, err := replay.Apply(buffer.Transaction{BaseVersion: replay.Version(), Edits: ch.Edits}); err != nil {
			t.Fatal(err)
		}
	}
	if replay.Text() != b.Text() {
		t.Errorf("replay = %q, want %q", replay.Text(), b.Text())
	}
}

// req: FR-08
func TestDeleteRegister(t *testing.T) {
	b := buffer.New("a\nb\nc\n")
	res, err := Execute(b, nil, 0, "2,3d x")
	if err != nil {
		t.Fatal(err)
	}
	want := []Deletion{{Register: 'x', Lines: []string{"b", "c"}}}
	if !reflect.DeepEqual(res.Deletions, want) {
		t.Errorf("deletions = %+v, want %+v", res.Deletions, want)
	}
}

// req: FR-08
func TestExecuteActions(t *testing.T) {
	tests := []struct {
		name     string
		modified bool
		cmdline  string
		want     []Action
		wantCode string
	}{
		{name: ":w", cmdline: "w", want: []Action{{Kind: ActionWrite}}},
		{name: ":write file", cmdline: "write foo.txt", want: []Action{{Kind: ActionWrite, File: "foo.txt"}}},
		{name: ":w! と空白の退避", cmdline: `w! a\ b`, want: []Action{{Kind: ActionWrite, File: "a b", Force: true}}},
		{name: ":w !cmd は未対応", cmdline: "w !ls", wantCode: "E319"},
		{name: ":q", cmdline: "q", want: []Action{{Kind: ActionQuit}}},
		{name: ":quit", cmdline: "quit", want: []Action{{Kind: ActionQuit}}},
		{name: ":checktime", modified: true, cmdline: "checktime", want: []Action{{Kind: ActionCheckTime}}},
		{name: ":checkt", cmdline: "checkt", want: []Action{{Kind: ActionCheckTime}}},
		{name: ":q で未保存の変更", modified: true, cmdline: "q", wantCode: "E37"},
		{name: ":q!", modified: true, cmdline: "q!", want: []Action{{Kind: ActionQuit, Force: true}}},
		{name: ":q の余分な文字", cmdline: "q foo", wantCode: "E488"},
		{name: ":%w は :w と同じ", cmdline: "%w", want: []Action{{Kind: ActionWrite}}},
		{name: ":0,$wq は :wq と同じ", cmdline: "0,$wq", want: []Action{{Kind: ActionWrite}, {Kind: ActionQuit}}},
		{name: ":1q は :q と同じ", cmdline: "1q", want: []Action{{Kind: ActionQuit}}},
		{name: ":2q は E16", cmdline: "2q", wantCode: "E16"},
		{name: ":2w は E16", cmdline: "2w", wantCode: "E16"},
		{name: ":wq", modified: true, cmdline: "wq", want: []Action{{Kind: ActionWrite}, {Kind: ActionQuit}}},
		{name: ":x で変更あり", modified: true, cmdline: "x", want: []Action{{Kind: ActionWrite}, {Kind: ActionQuit}}},
		{name: ":x で変更なし", cmdline: "x", want: []Action{{Kind: ActionQuit}}},
		{name: ":x file", cmdline: "x f", want: []Action{{Kind: ActionWrite, File: "f"}, {Kind: ActionQuit}}},
		{name: ":e file", cmdline: "e foo", want: []Action{{Kind: ActionEdit, File: "foo"}}},
		{name: ":e で未保存の変更", modified: true, cmdline: "e foo", wantCode: "E37"},
		{name: ":e!", modified: true, cmdline: "e!", want: []Action{{Kind: ActionEdit, Force: true}}},
		{name: "省略しすぎ", cmdline: "wr", want: []Action{{Kind: ActionWrite}}},
		{name: "長すぎる名前", cmdline: "writex", wantCode: "E492"},
		{name: ":w|q で未保存の変更", modified: true, cmdline: "w|q", want: []Action{{Kind: ActionWrite}, {Kind: ActionQuit}}},
		{name: ":w other|q で未保存の変更", modified: true, cmdline: "w other|q", wantCode: "E37"},
		{name: ":w|s|q は書いた後の変更で E37", modified: true, cmdline: "w|s/x/y/|q", wantCode: "E37"},
		{name: ":w|x は書き出しを重ねない", modified: true, cmdline: "w|x", want: []Action{{Kind: ActionWrite}, {Kind: ActionQuit}}},
		{name: ":w|q", cmdline: "w|q", want: []Action{{Kind: ActionWrite}, {Kind: ActionQuit}}},
		{name: ":w | q", modified: true, cmdline: "w foo | q!", want: []Action{{Kind: ActionWrite, File: "foo"}, {Kind: ActionQuit, Force: true}}},
		{name: ":e foo|q", cmdline: "e foo|q", want: []Action{{Kind: ActionEdit, File: "foo"}, {Kind: ActionQuit}}},
		{name: "ファイル名の \\|", cmdline: `w a\|b`, want: []Action{{Kind: ActionWrite, File: "a|b"}}},
		{name: ":q|q の余分な文字", cmdline: "q x|q", wantCode: "E488"},
		{name: ":output", cmdline: "output", want: []Action{{Kind: ActionOutput, Output: OutputOpen}}},
		{name: ":out acp", cmdline: "out  acp ", want: []Action{{Kind: ActionOutput, Output: OutputOpen, Text: "acp"}}},
		{name: ":outc", cmdline: "outc", want: []Action{{Kind: ActionOutput, Output: OutputClose}}},
		{name: ":outclose の余分な文字", cmdline: "outclose x", wantCode: "E488"},
		{name: ":ou は短すぎる", cmdline: "ou", wantCode: "E492"},
		{name: ":2output は範囲を取らない", cmdline: "2output", wantCode: "E481"},
		{name: ":output|q", cmdline: "output dap|q", want: []Action{{Kind: ActionOutput, Output: OutputOpen, Text: "dap"}, {Kind: ActionQuit}}},
		{name: ":ls", cmdline: "ls", want: []Action{{Kind: ActionBuffer, Buffer: BufferList}}},
		{name: ":buffers!", cmdline: "buffers!", want: []Action{{Kind: ActionBuffer, Buffer: BufferList}}},
		{name: ":ls の余分な文字", cmdline: "ls x", wantCode: "E488"},
		{name: ":b は未保存の変更を呼び出し側が確かめる", modified: true, cmdline: "b 2", want: []Action{{Kind: ActionBuffer, Buffer: BufferGo, Count: 2}}},
		{name: ":bu は :buffer", cmdline: "bu3", want: []Action{{Kind: ActionBuffer, Buffer: BufferGo, Count: 3}}},
		{name: ":buffer! 名前", cmdline: `buffer! a\ b.txt`, want: []Action{{Kind: ActionBuffer, Buffer: BufferGo, Text: "a b.txt", Force: true}}},
		{name: ":b 引数なし", cmdline: "b", want: []Action{{Kind: ActionBuffer, Buffer: BufferGo}}},
		{name: ":b 1 2 は E488", cmdline: "b 1 2", wantCode: "E488"},
		{name: ":3b は範囲を取らない", cmdline: "3b", wantCode: "E481"},
		{name: ":bn", cmdline: "bn", want: []Action{{Kind: ActionBuffer, Buffer: BufferNext, Count: 1}}},
		{name: ":bnext! 3", cmdline: "bnext! 3", want: []Action{{Kind: ActionBuffer, Buffer: BufferNext, Count: 3, Force: true}}},
		{name: ":bN", cmdline: "bN", want: []Action{{Kind: ActionBuffer, Buffer: BufferPrev, Count: 1}}},
		{name: ":bp 2|q", cmdline: "bp 2|q", want: []Action{{Kind: ActionBuffer, Buffer: BufferPrev, Count: 2}, {Kind: ActionQuit}}},
		{name: ":bn 0 は E939", cmdline: "bn 0", wantCode: "E939"},
		{name: ":bn x は E488", cmdline: "bn x", wantCode: "E488"},
		{name: ":ju", cmdline: "ju", want: []Action{{Kind: ActionJumpList}}},
		{name: ":wa", modified: true, cmdline: "wa", want: []Action{{Kind: ActionWriteAll}}},
		{name: ":qa は未保存の変更を呼び出し側が確かめる", modified: true, cmdline: "qa", want: []Action{{Kind: ActionQuitAll}}},
		{name: ":qa!", cmdline: "qall!", want: []Action{{Kind: ActionQuitAll, Force: true}}},
		{name: ":wqa", cmdline: "wqa", want: []Action{{Kind: ActionWriteAll}, {Kind: ActionQuitAll}}},
		{name: ":xa!", cmdline: "xa!", want: []Action{{Kind: ActionWriteAll, Force: true}, {Kind: ActionQuitAll, Force: true}}},
		{name: ":wq は :wqa にならない", cmdline: "wq", want: []Action{{Kind: ActionWrite}, {Kind: ActionQuit}}},
		{name: ":bd", cmdline: "bd", want: []Action{{Kind: ActionBufferDelete}}},
		{name: ":bdelete! 3", cmdline: "bdelete! 3", want: []Action{{Kind: ActionBufferDelete, Count: 3, Force: true}}},
		{name: ":bd 名前", cmdline: "bd a.txt", want: []Action{{Kind: ActionBufferDelete, Text: "a.txt"}}},
		{name: ":jumps の余分な文字", cmdline: "jumps 1", wantCode: "E488"},
		// req: FR-54
		{name: ":reg", cmdline: "reg", want: []Action{{Kind: ActionRegisterList}}},
		{name: ":registers", cmdline: "registers", want: []Action{{Kind: ActionRegisterList}}},
		{name: ":re は :registers にならない", cmdline: "re", wantCode: "E492"},
		{name: ":reg の余分な文字", cmdline: "reg a", wantCode: "E488"},
		{name: ":2reg は範囲を取らない", cmdline: "2reg", wantCode: "E481"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := buffer.New("a\n")
			if tt.modified {
				if _, err := b.Apply(buffer.Transaction{Edits: []buffer.Edit{{Text: "x"}}}); err != nil {
					t.Fatal(err)
				}
			}
			res, err := Execute(b, nil, 0, tt.cmdline)
			if code := errCode(t, err); code != tt.wantCode {
				t.Fatalf("code = %q, want %q (err = %v)", code, tt.wantCode, err)
			}
			if err == nil && !reflect.DeepEqual(res.Actions, tt.want) {
				t.Errorf("actions = %+v, want %+v", res.Actions, tt.want)
			}
		})
	}
}

// ジャンプに当たるコマンド（一致のあった :s、印の付いた行のある :g と :v、:d）は Result.Jumped を立てる。
// どれが当たるかは、手元の Vim 9.1 で getjumplist() を読んで確かめた（2026-11-16）。
//
// req: FR-08
func TestExecuteJumped(t *testing.T) {
	tests := []struct {
		cmdline string
		want    bool
	}{
		{"s/a/x/", true},
		{"s/zzz/x/e", false},
		{"s/zzz/x/", false},
		{"g/b/d", true},
		{"g/zzz/d", false},
		{"v/./d", false},
		{"2d", true},
		{"9d", false},
		{"3", false},
		{"w", false},
	}
	for _, tt := range tests {
		t.Run(tt.cmdline, func(t *testing.T) {
			res, _ := Execute(buffer.New("a\nb\nc\n"), nil, 0, tt.cmdline)
			if res.Jumped != tt.want {
				t.Errorf("Jumped = %v, want %v", res.Jumped, tt.want)
			}
		})
	}
}

// 誤りは、コマンドラインの中の位置と、Vim のエラー番号の付いた文言で返す。
//
// req: FR-08
func TestErrorPosition(t *testing.T) {
	tests := []struct {
		cmdline string
		pos     int
		msg     string
	}{
		{cmdline: "  foo", pos: 2, msg: "E492: エディタのコマンドではありません: foo"},
		{cmdline: "%s/a(/b/", pos: 3, msg: "E383: "},
		{cmdline: "s/a/b/x", pos: 6, msg: "E319: "},
		{cmdline: "g/a/zz", pos: 4, msg: "E492: "},
		{cmdline: `s/a/x\U/`, pos: 5, msg: "E319: "},
		{cmdline: "2d", pos: 0, msg: "E16: "},
		{cmdline: "1, 3s/a/b/", pos: 3, msg: "E16: "},
		{cmdline: "3,1d", pos: 0, msg: "E493: "},
		{cmdline: "d| 3,1d", pos: 3, msg: "E493: "},
		{cmdline: "3,1|", pos: 0, msg: "E16: "},
		{cmdline: "2q", pos: 0, msg: "E16: "},
		{cmdline: "/a/q", pos: 0, msg: "E16: "},
		{cmdline: "5|d", pos: 0, msg: "E16: "},
	}
	for _, tt := range tests {
		t.Run(tt.cmdline, func(t *testing.T) {
			_, err := Execute(buffer.New("a\n"), nil, 0, tt.cmdline)
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("err = %v", err)
			}
			if e.Pos != tt.pos {
				t.Errorf("pos = %d, want %d", e.Pos, tt.pos)
			}
			if !strings.HasPrefix(e.Error(), tt.msg) {
				t.Errorf("message = %q, want prefix %q", e.Error(), tt.msg)
			}
		})
	}
}

// req: FR-09
func TestConvertReplacement(t *testing.T) {
	tests := []struct{ in, want string }{
		{in: `\1-\9`, want: "${1}-${9}"},
		{in: `&\0`, want: "${0}${0}"},
		{in: `\&\\\/`, want: `&\/`},
		{in: `$1\$`, want: "$$1$$"},
		{in: `a\rb\nc\td`, want: "a\nb\nc\td"},
		{in: `~`, want: "~"},
		{in: `x\`, want: `x\`},
	}
	for _, tt := range tests {
		got, err := convertReplacement(tt.in, 0)
		if err != nil || got != tt.want {
			t.Errorf("convertReplacement(%q) = %q, %v, want %q", tt.in, got, err, tt.want)
		}
	}
}

// カーソルの行と置き方は、Vim 9.1（vim -Nu NONE -es で col(".") を見た結果）に合わせる。
// 行は "  " か "    " で始め、最初の空白でない文字と行の先頭を見分けられるようにした。
//
// req: FR-08
func TestCursorMove(t *testing.T) {
	const three = "  aaa\n  bbb\n    ccc\n"
	tests := []struct {
		text     string
		cursor   int
		cmdline  string
		wantLine int
		wantMove CursorMove
	}{
		{text: three, cmdline: "s/a/x/", wantLine: 0, wantMove: CursorFirstNonBlank},
		{text: three, cmdline: "s/zz/x/e", wantLine: 0, wantMove: CursorUnchanged},
		{text: three, cmdline: "3s/zz/x/e", wantLine: 0, wantMove: CursorUnchanged},
		{text: "  a,b\n  c\n", cmdline: `s/,/\r   /`, wantLine: 1, wantMove: CursorFirstNonBlank},
		{text: "  ax\n  bx\n  cx\n", cmdline: `%s/x/\ry/`, wantLine: 5, wantMove: CursorFirstNonBlank},
		{text: three, cmdline: "2d", wantLine: 1, wantMove: CursorFirstNonBlank},
		{text: three, cmdline: "3d", wantLine: 1, wantMove: CursorFirstNonBlank},
		{text: three, cmdline: "3", wantLine: 2, wantMove: CursorFirstNonBlank},
		{text: three, cmdline: "/c/", wantLine: 2, wantMove: CursorFirstNonBlank},
		{text: three, cmdline: "w! /dev/null", wantLine: 0, wantMove: CursorUnchanged},
		{text: "  a\n    bx\n  c\n", cmdline: "g/b/s/zz/y/", wantLine: 1, wantMove: CursorLineStart},
		{text: "  a\n    bx\n  c\n", cmdline: "g/b/s/x/y/", wantLine: 1, wantMove: CursorFirstNonBlank},
		{text: "  a\n    bx\n  c\n", cmdline: "g/b/", wantLine: 1, wantMove: CursorFirstNonBlank},
		{text: "  a\n    bx\n    c\n", cmdline: "g/b/d", wantLine: 1, wantMove: CursorFirstNonBlank},
		{text: "  ax\n  bx\n  cx\n", cmdline: "v/b/s/x/y/", wantLine: 2, wantMove: CursorFirstNonBlank},
	}
	for _, tt := range tests {
		t.Run(tt.cmdline, func(t *testing.T) {
			res, err := Execute(buffer.New(tt.text), nil, tt.cursor, tt.cmdline)
			if err != nil {
				t.Fatal(err)
			}
			if res.Cursor != tt.wantLine || res.Move != tt.wantMove {
				t.Errorf("cursor = %d, %v, want %d, %v", res.Cursor, res.Move, tt.wantLine, tt.wantMove)
			}
		})
	}
}

// State は呼び出しをまたいで受け渡され、Vim と同じ時機に更新される。
//
// req: FR-08, FR-09
func TestStateAcrossCalls(t *testing.T) {
	b := buffer.New("foo bar\nfoo baz\n")
	st := &State{LastPattern: "foo"} // ノーマルモードの / で foo を探した後

	// 空のパターンは、呼び出し側が渡した直前のパターンを使う。
	if _, err := Execute(b, st, 0, "s//X/"); err != nil {
		t.Fatal(err)
	}
	if got := b.Line(0); got != "X bar" {
		t.Errorf("line 0 = %q", got)
	}
	want := &Substitution{Pattern: "foo", Replacement: "X"}
	if !reflect.DeepEqual(st.LastSubstitute, want) {
		t.Errorf("LastSubstitute = %+v, want %+v", st.LastSubstitute, want)
	}

	// コンパイルに失敗したパターンは記録しない。
	if _, err := Execute(b, st, 0, "s/a(/x/"); errCode(t, err) != "E383" {
		t.Fatalf("err = %v", err)
	}
	if st.LastPattern != "foo" {
		t.Errorf("LastPattern = %q after invalid pattern", st.LastPattern)
	}

	// ~ は直前の置換の文字列、\~ は ~ そのもの。
	if _, err := Execute(b, st, 1, `s/b(a)z/~~\~/gi`); err != nil {
		t.Fatal(err)
	}
	if got := b.Line(1); got != "foo XX~" {
		t.Errorf("line 1 = %q", got)
	}
	want = &Substitution{Pattern: "b(a)z", Replacement: `XX\~`, Global: true, IgnoreCase: true}
	if !reflect.DeepEqual(st.LastSubstitute, want) {
		t.Errorf("LastSubstitute = %+v, want %+v", st.LastSubstitute, want)
	}

	// :g と "/pat/" のアドレスのパターンも記録する（その後の n が使う）。
	if _, err := Execute(b, st, 0, "g/bar/"); err != nil {
		t.Fatal(err)
	}
	if st.LastPattern != "bar" {
		t.Errorf("LastPattern = %q after :g", st.LastPattern)
	}
	if _, err := Execute(b, st, 0, "/XX/"); err != nil {
		t.Fatal(err)
	}
	if st.LastPattern != "XX" {
		t.Errorf("LastPattern = %q after address", st.LastPattern)
	}
}
