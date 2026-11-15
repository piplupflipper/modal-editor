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
		{name: "バッファの外", text: lines10, cmdline: "11", wantCode: "E16", wantPos: 0, checkPos: true},
		{name: "負の行", text: lines10, cursor: 0, cmdline: "1,.-2d", wantCode: "E16", wantPos: 2, checkPos: true},
		{name: "検索の該当なし", text: lines10, cmdline: "/zz/", wantCode: "E486"},
		{name: "検索の不正な正規表現", text: lines10, cmdline: "/a(/", wantCode: "E383", wantPos: 0, checkPos: true},
		{name: "マークは未対応", text: lines10, cmdline: "'<,'>d", wantCode: "E319"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &execCtx{b: buffer.New(tt.text), cursor: tt.cursor}
			s := &scanner{src: tt.cmdline}
			r, err := c.parseRange(s)
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
			if tt.wantSaved != "" && c.lastPattern != tt.wantSaved {
				t.Errorf("lastPattern = %q, want %q", c.lastPattern, tt.wantSaved)
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
		{name: ":d の逆順の範囲", text: lines10, cmdline: "4,2d", want: "l1\nl5\nl6\nl7\nl8\nl9\nl10", wantCursor: 1},
		{name: ":d の余分な文字", text: "a\n", cmdline: "d a b", want: "a", wantCode: "E488"},

		// 移動
		{name: ":3", text: lines10, cmdline: ":3", want: strings.TrimSuffix(lines10, "\n"), wantCursor: 2},
		{name: ":0 は 1 行目", text: lines10, cursor: 5, cmdline: "0", want: strings.TrimSuffix(lines10, "\n")},
		{name: ":$", text: lines10, cmdline: "$", want: strings.TrimSuffix(lines10, "\n"), wantCursor: 9},
		{name: ":/pat/", text: lines10, cmdline: "/l7/", want: strings.TrimSuffix(lines10, "\n"), wantCursor: 6},
		{name: "空のコマンド", text: lines10, cursor: 4, cmdline: ":", want: strings.TrimSuffix(lines10, "\n"), wantCursor: 4},

		// 誤り
		{name: "未知のコマンド", text: "a\n", cmdline: "foo", want: "a", wantCode: "E492"},
		{name: "英字でないコマンド", text: "a\n", cmdline: "1#", want: "a", wantCode: "E492"},
		{name: "範囲を取らないコマンド", text: "a\n", cmdline: "1w", want: "a", wantCode: "E481"},
		{name: "! を取らないコマンド", text: "a\n", cmdline: "d!", want: "a", wantCode: "E477"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := buffer.New(tt.text)
			res, err := Execute(b, tt.cursor, tt.cmdline)
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

// :v で全ての行が一致したときは、Vim と同じく番号の無い通知を誤りとして返す。
//
// req: FR-08
func TestGlobalInvertAllMatch(t *testing.T) {
	_, err := Execute(buffer.New("a\n"), 0, "v/a/d")
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
	res, err := Execute(b, 0, "g/a/d")
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
	res, err := Execute(b, 0, `g/,/s/,/\r/`)
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
	res, err := Execute(b, 0, "2,3d x")
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
		{name: ":q で未保存の変更", modified: true, cmdline: "q", wantCode: "E37"},
		{name: ":q!", modified: true, cmdline: "q!", want: []Action{{Kind: ActionQuit, Force: true}}},
		{name: ":q の余分な文字", cmdline: "q foo", wantCode: "E488"},
		{name: ":wq", modified: true, cmdline: "wq", want: []Action{{Kind: ActionWrite}, {Kind: ActionQuit}}},
		{name: ":x で変更あり", modified: true, cmdline: "x", want: []Action{{Kind: ActionWrite}, {Kind: ActionQuit}}},
		{name: ":x で変更なし", cmdline: "x", want: []Action{{Kind: ActionQuit}}},
		{name: ":x file", cmdline: "x f", want: []Action{{Kind: ActionWrite, File: "f"}, {Kind: ActionQuit}}},
		{name: ":e file", cmdline: "e foo", want: []Action{{Kind: ActionEdit, File: "foo"}}},
		{name: ":e で未保存の変更", modified: true, cmdline: "e foo", wantCode: "E37"},
		{name: ":e!", modified: true, cmdline: "e!", want: []Action{{Kind: ActionEdit, Force: true}}},
		{name: "省略しすぎ", cmdline: "wr", want: []Action{{Kind: ActionWrite}}},
		{name: "長すぎる名前", cmdline: "writex", wantCode: "E492"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := buffer.New("a\n")
			if tt.modified {
				if _, err := b.Apply(buffer.Transaction{Edits: []buffer.Edit{{Text: "x"}}}); err != nil {
					t.Fatal(err)
				}
			}
			res, err := Execute(b, 0, tt.cmdline)
			if code := errCode(t, err); code != tt.wantCode {
				t.Fatalf("code = %q, want %q (err = %v)", code, tt.wantCode, err)
			}
			if err == nil && !reflect.DeepEqual(res.Actions, tt.want) {
				t.Errorf("actions = %+v, want %+v", res.Actions, tt.want)
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
	}
	for _, tt := range tests {
		t.Run(tt.cmdline, func(t *testing.T) {
			_, err := Execute(buffer.New("a\n"), 0, tt.cmdline)
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
