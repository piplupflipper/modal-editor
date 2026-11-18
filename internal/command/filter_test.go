package command

import (
	"bytes"
	"errors"
	"os/exec"
	"testing"

	"github.com/piplupflipper/modal-editor/internal/editcmd"
)

// shCommand は、テストの ex.State.Shell である。cmd を /bin/sh -c で実行し、標準出力と標準エラー出力を合わせて返す。
// 本体の Shell（internal/editor）と同じ形で、Vim の 'shell' を /bin/sh にしたときの結果と比べる。
func shCommand(cmd string, input []byte) ([]byte, int, error) {
	c := exec.Command("/bin/sh", "-c", cmd) //nolint:gosec // cmd はこのファイルの表に書いたテストのコマンドだけである
	if input != nil {
		c.Stdin = bytes.NewReader(input)
	}
	var out bytes.Buffer
	c.Stdout, c.Stderr = &out, &out
	err := c.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return out.Bytes(), ee.ExitCode(), nil
	}
	return out.Bytes(), 0, err
}

// filterCases は、:{range}!{cmd} と :r !{cmd} の結果を、Vim 9.1 と突き合わせたケースである（FR-81）。
//
// Vim は Ex モードでない起動（`vim -Nu NONE -n --not-a-term -S <script>`）で、'shell' を /bin/sh にして動かした。
// 本文は "c\n  bb\n a\nd\ne" で、カーソルを置いてから Ex コマンドを実行し、カーソルと本文と最後のメッセージを読み、
// u の後のカーソルを読んだ。
var filterCases = []struct {
	text   string // "|" はカーソルの位置
	cmd    string
	want   string
	cursor string
	undo   string // u の後のカーソル。空なら u を試さない（変更が無いとき）
	msg    string // Vim のメッセージに当たる通知
}{
	{"|c\n  bb\n a\nd\ne", "2,3!tr a-z A-Z", "c\n  BB\n A\nd\ne", "1:2", "1:0", ""},
	{"|c\n  bb\n a\nd\ne", "2,3!true", "c\nd\ne", "1:0", "1:0", ""},
	{"|c\n  bb\n a\nd\ne", "%!true", "", "0:0", "0:0", "5 行をフィルターしました"},
	{"|c\n  bb\n a\nd\ne", "2,3!printf '   P\\nQ\\n'", "c\n   P\nQ\nd\ne", "1:3", "1:0", ""},
	{"c\n  bb\n |a\nd\ne", "2,3!printf '   P\\nQ\\n'", "c\n   P\nQ\nd\ne", "1:3", "1:0", ""},
	// 終了コードが 0 でなくても置き換え、標準エラー出力もバッファへ入る（Vim の 'shellredir' の既定 ">%s 2>&1"）。
	// Vim は "shell returned 3" を画面に出す（:messages には残らない）。ここでは警告の通知にする。
	{"|c\n  bb\n a\nd\ne", "2,3!echo out; echo err >&2; exit 3", "c\nout\nerr\nd\ne", "1:0", "1:0", "シェルが 3 を返しました"},
	{"|c\n  bb\n a\nd\ne", "2,3!printf 'x\\ny'", "c\nx\ny\nd\ne", "1:0", "1:0", ""},
	{"|c\n  bb\n a\nd\ne", "2,3!printf '\\n'", "c\n\nd\ne", "1:0", "1:0", ""},
	{"|c\n  bb\n a\nd\ne", "1,5!tac", "e\nd\n a\n  bb\nc", "0:0", "0:0", "5 行をフィルターしました"},
	{"|c\n  bb\n a\nd\ne", "2,4!tac", "c\nd\n a\n  bb\ne", "1:0", "1:0", "3 行をフィルターしました"},
	// "|" は cmd の一部としてシェルへ渡す。
	{"|c\n  bb\n a\nd\ne", "2,3!cat | tr a-z A-Z", "c\n  BB\n A\nd\ne", "1:2", "1:0", ""},
	{"c\n  b|b\n a\nd\ne", "r !printf '  X\\n Y\\n  Z\\n'", "c\n  bb\n  X\n Y\n  Z\n a\nd\ne", "4:2", "1:0", "3 行増えました"},
	{"c\n  |bb\n a\nd\ne", "0r !printf '  X\\n Y\\n  Z\\n'", "  X\n Y\n  Z\nc\n  bb\n a\nd\ne", "2:2", "0:0", "3 行増えました"},
	{"c\n  |bb\n a\nd\ne", "$r !echo X", "c\n  bb\n a\nd\ne\nX", "5:0", "4:0", ""},
	{"c\n  b|b\n a\nd\ne", "r !true", "c\n  bb\n a\nd\ne", "1:3", "", ""},
	{"|c\n  bb\n a\nd\ne", "r !echo out; echo err >&2; exit 2", "c\nout\nerr\n  bb\n a\nd\ne", "2:0", "0:0", "シェルが 2 を返しました"},
	{"|c\n  bb\n a\nd\ne", "r!echo Q", "c\nQ\n  bb\n a\nd\ne", "1:0", "0:0", ""},
	{"c\n  bb\n a\n|d\ne", "2r !echo x | tr x y", "c\n  bb\ny\n a\nd\ne", "2:0", "1:0", ""},
	// cmd が空なら何もしない（Vim 9.1 の do_filter と同じ）。
	{"|c\n  bb\n a\nd\ne", "2,3!", "c\n  bb\n a\nd\ne", "0:0", "", ""},
	{"|c\n  bb\n a\nd\ne", "r !", "c\n  bb\n a\nd\ne", "0:0", "", ""},
	// 行の無いバッファ。Vim 9.1 は :r で元の空の行を残し、:%! は置き換える。
	{"|", "r !echo X", "\nX", "1:0", "0:0", ""},
	{"|", "0r !echo X", "X\n", "0:0", "0:0", ""},
	{"|", "%!echo X", "X", "0:0", "0:0", ""},
}

// req: FR-81
func TestFilterVim(t *testing.T) {
	for _, tc := range filterCases {
		t.Run(tc.text+"/"+tc.cmd, func(t *testing.T) {
			s := setup(t, tc.text)
			if tc.text == "|" {
				s.buf.MarkNoLines()
			}
			res := runShellLine(s, tc.cmd)
			if res.Err != nil {
				t.Fatalf("err = %v", res.Err)
			}
			if got := s.buf.Text(); got != tc.want {
				t.Errorf("text = %q, want %q", got, tc.want)
			}
			if got := s.Cursor().String(); got != tc.cursor {
				t.Errorf("cursor = %s, want %s", got, tc.cursor)
			}
			if res.Message.Text != tc.msg {
				t.Errorf("message = %q, want %q", res.Message.Text, tc.msg)
			}
			if tc.undo == "" {
				return
			}
			feedKeys(s, "u")
			if got := s.Cursor().String(); got != tc.undo {
				t.Errorf("cursor after u = %s, want %s", got, tc.undo)
			}
			if s.buf.CanUndo() {
				t.Errorf("more undo units remain after one u")
			}
		})
	}
}

// TestFilterNoLines は、:%! の出力が空なら、行の無いバッファ（空のファイル）にすることを確かめる（Vim 9.1 で :%!true の後の :w は 0 バイト）。
//
// req: FR-81
func TestFilterNoLines(t *testing.T) {
	s := setup(t, "|a\nb")
	runShellLine(s, "%!true")
	if !s.buf.NoLines() {
		t.Errorf("NoLines = false, want true")
	}
}

// TestFilterErrors は、外部のコマンドを実行できないときと、まだ無い形の誤りを確かめる。
//
// req: FR-81
func TestFilterErrors(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		fail error // FinishShell に渡す実行の誤り
	}{
		{"実行できない", "2!cat", errors.New("interrupted")},
		{":r の実行できない", "r !cat", errors.New("interrupted")},
		{"範囲の無い :!", "!ls", nil},
		{":r {file}", "r foo", nil},
		// :g の cmd の中では、実行の間に :g の続きを止められないので使えない（Vim では使える）。
		{":g の中の :!", "g/a/.!cat", nil},
		{":g の中の :r !", "g/a/r !echo x", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := setup(t, "|a\nb")
			res := s.ExecuteCommandLine(tt.cmd)
			if res.Shell != nil {
				res = s.FinishShell(res.Shell, []byte("x\n"), 0, tt.fail)
			}
			if res.Err == nil {
				t.Fatalf("err = nil")
			}
			if got := s.buf.Text(); got != "a\nb" {
				t.Errorf("text = %q, want unchanged", got)
			}
		})
	}
}

// TestFilterStale は、実行を求めた後にバッファが変わっていれば、出力を捨てることを確かめる（範囲がずれているため）。
//
// req: FR-81
func TestFilterStale(t *testing.T) {
	s := setup(t, "|a\nb")
	res := s.ExecuteCommandLine("2!cat")
	if res.Shell == nil {
		t.Fatal("Shell = nil")
	}
	feedKeys(s, "x")
	res = s.FinishShell(res.Shell, []byte("X\n"), 0, nil)
	if res.Err == nil {
		t.Errorf("err = nil")
	}
	if got := s.buf.Text(); got != "\nb" {
		t.Errorf("text = %q", got)
	}
}

// TestFilterInMacro は、マクロの再生の中の :{range}! が、実行を求めて再生を止め（ErrShellPending）、
// 出力を入れた後に ContinueReplay で残りを続けられることを確かめる。入れ子の再生と回数の残りも Remaining に入る。
// 期待値は Vim 9.1 で、本文 "ab/cd/ef/gh" の 1 行目で qa:.!tr a-z A-Z<CR>jq の後に 2@a を打った結果である（AB/CD/EF/GH、カーソル 3:0）。
//
// req: FR-81
func TestFilterInMacro(t *testing.T) {
	s := setup(t, "|ab\ncd\nef\ngh")
	rec := feedKeys(s, "qa:.!tr a-z A-Z<CR>")
	if sh := rec[len(rec)-1].Shell; sh == nil {
		t.Fatal("記録の間に実行を求めない")
	} else {
		out, status, err := shCommand(sh.Cmd, sh.Input)
		s.FinishShell(sh, out, status, err)
	}
	feedKeys(s, "jq")
	if got := s.buf.Text(); got != "AB\ncd\nef\ngh" {
		t.Fatalf("記録: text = %q", got)
	}
	// 2@a は @b の中から呼ぶ（入れ子の再生）。
	s.registers.named['b'-'a'] = Register{Text: "2@a", keys: editcmd.MustParseKeys("2@a"), filled: true}
	res := feedKeys(s, "@b")
	last := res[len(res)-1]
	for steps := 0; last.Shell != nil; steps++ {
		if steps > 5 {
			t.Fatal("再生が終わらない")
		}
		if !errors.Is(last.Err, ErrShellPending) {
			t.Fatalf("err = %v, want ErrShellPending", last.Err)
		}
		out, status, err := shCommand(last.Shell.Cmd, last.Shell.Input)
		if fin := s.FinishShell(last.Shell, out, status, err); fin.Err != nil {
			t.Fatalf("FinishShell: %v", fin.Err)
		}
		last = s.ContinueReplay(last.Remaining)
	}
	if last.Err != nil {
		t.Fatalf("err = %v", last.Err)
	}
	if got := s.buf.Text(); got != "AB\nCD\nEF\ngh" {
		t.Errorf("text = %q", got)
	}
	if got := s.Cursor().String(); got != "3:0" {
		t.Errorf("cursor = %s", got)
	}
	// ドットリピートの対象は、いちばん外側の再生（@b）である（FR-57）。. で @b をもう一度再生すると、また外部のコマンドを求める。
	if res := feedKeys(s, "."); res[len(res)-1].Shell == nil {
		t.Errorf(". が @b を再生しなかった")
	}
}

// runShellLine は、Ex コマンドを実行し、外部のコマンドの実行を求められたら shCommand で実行して FinishShell に渡す。
func runShellLine(s *State, cmd string) Result {
	res := s.ExecuteCommandLine(cmd)
	if res.Shell == nil || res.Err != nil {
		return res
	}
	out, status, err := shCommand(res.Shell.Cmd, res.Shell.Input)
	return s.FinishShell(res.Shell, out, status, err)
}
