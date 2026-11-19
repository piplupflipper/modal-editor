package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os/exec"
	"path/filepath"
	"strings"
)

// 候補の出し方（基本設計 6.9）。
//
// 外部のコマンド（fd、rg）は、作業ディレクトリを cmd.Dir にし、探す場所を "." と明示して起動する。
// rg は、探す場所を書かずに標準入力がパイプだと標準入力を検索するので、本体とのメッセージの標準入力を読ませないためである
// （exec.Cmd の Stdin を nil にして /dev/null をつなぐ）。
// 子プロセスは拡張機能と同じプロセスグループのままにする。本体が拡張機能を SIGKILL で終わらせるときは、プロセスグループへ送るので、
// 子プロセスも一緒に終わる（Setpgid にすると孤児が残る。dap の拡張機能の procattr_linux.go の doc）。

// lookPath は、exec.LookPath の差し替え口である（テストで外部のコマンドが無い場合を試す）。
var lookPath = exec.LookPath

// fileLister は、作業ディレクトリ dir の下のファイルを、見つけるたびに emit へ dir からの相対パスで渡す。
// ctx が終わるか、すべてを渡し終えたら戻る。
type fileLister func(ctx context.Context, dir string, hidden bool, emit func(rel string)) error

// pickFileLister は、使えるファイルの一覧の出し方を、fd（Debian と Ubuntu では fdfind）、rg --files、Go で歩く、の順に選び、その名前と一緒に返す。
// fd と rg は .gitignore を読むが、Go で歩くときは読まない。どれも .git の下は除く。
func pickFileLister() (string, fileLister) {
	for _, name := range []string{"fd", "fdfind"} {
		if path, err := lookPath(name); err == nil {
			return name, commandLister(path, func(hidden bool) []string {
				args := []string{"--type", "f", "--color", "never", "--exclude", ".git"}
				if hidden {
					args = append(args, "--hidden")
				}
				return append(args, ".", ".")
			})
		}
	}
	if path, err := lookPath("rg"); err == nil {
		return "rg", commandLister(path, func(hidden bool) []string {
			args := []string{"--files", "--color", "never", "-g", "!.git/"}
			if hidden {
				args = append(args, "--hidden")
			}
			return append(args, ".")
		})
	}
	return "walk", walkFiles
}

// commandLister は、外部のコマンド path を args の引数で起動し、標準出力の 1 行を 1 つのファイルとして渡す fileLister を返す。
// 行の先頭の "./" は除く。
func commandLister(path string, args func(hidden bool) []string) fileLister {
	return func(ctx context.Context, dir string, hidden bool, emit func(string)) error {
		cmd := exec.CommandContext(ctx, path, args(hidden)...) //nolint:gosec // path は LookPath で見つけた fd か rg で、引数は固定の列である
		cmd.Dir = dir
		out, err := cmd.StdoutPipe()
		if err != nil {
			return err
		}
		if err := cmd.Start(); err != nil {
			return err
		}
		sc := bufio.NewScanner(out)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			if l := strings.TrimPrefix(sc.Text(), "./"); l != "" {
				emit(l)
			}
		}
		werr := cmd.Wait()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return werr
	}
}

// walkFiles は、dir の下のファイルを Go で歩いて渡す（fd も rg も無いとき）。.git のディレクトリは除き、
// hidden が false なら . で始まるファイルとディレクトリも除く。読めないディレクトリは飛ばす。
func walkFiles(ctx context.Context, dir string, hidden bool, emit func(string)) error {
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		if path != dir && (name == ".git" || (!hidden && strings.HasPrefix(name, "."))) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() || d.Type()&fs.ModeSymlink != 0 {
			if rel, err := filepath.Rel(dir, path); err == nil {
				emit(rel)
			}
		}
		return nil
	})
}

// grepMatch は、rg の 1 件の一致である。
type grepMatch struct {
	// Path は、検索したディレクトリからの相対パスである。
	Path string
	// Line は 1 から数えた行、Col は 0 から数えたバイトの列（最初の一致の始まり）である。
	Line, Col int
	// Text は、行のテキスト（末尾の改行を除く）、Spans は、その中の一致のバイトの範囲である。
	Text  string
	Spans [][2]int
}

// errNoRG は、rg が見つからないことを表す。
var errNoRG = errors.New("rg が見つからないので、検索できません")

// rgArgs は、rg で pattern を検索する引数である（オーナーの fzf-lua の rg_opts に合わせ、smart case、隠しファイルを含め、.git の下を除く）。
func rgArgs(pattern string, hidden bool) []string {
	args := []string{"--json", "--smart-case", "--max-columns", "4096", "-g", "!.git/"}
	if hidden {
		args = append(args, "--hidden")
	}
	return append(args, "-e", pattern, ".")
}

// runGrep は、dir の下を rg で pattern を検索し、一致を見つけるたびに emit へ渡す。emit が false を返したら止める。
// rg の終了コード 1（一致なし）は誤りにしない。正規表現の誤りなどは、rg の標準エラー出力の最初の行を誤りにする。
func runGrep(ctx context.Context, dir, pattern string, hidden bool, emit func(grepMatch) bool) error {
	path, err := lookPath("rg")
	if err != nil {
		return errNoRG
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, rgArgs(pattern, hidden)...) //nolint:gosec // path は LookPath で見つけた rg で、利用者の入力は -e の値としてだけ渡す（シェルを通さない）
	cmd.Dir = dir
	var stderr strings.Builder
	cmd.Stderr = &limitedWriter{w: &stderr, n: 4096}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	stopped := false
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		m, ok := parseRGMatch(sc.Bytes())
		if !ok {
			continue
		}
		if !emit(m) {
			stopped = true
			cancel()
			break
		}
	}
	werr := cmd.Wait()
	switch {
	case stopped || ctx.Err() != nil:
		return nil
	case werr == nil:
		return nil
	}
	all := strings.TrimSpace(stderr.String())
	msg, _, _ := strings.Cut(all, "\n")
	var ee *exec.ExitError
	if errors.As(werr, &ee) && ee.ExitCode() == 1 && msg == "" {
		// rg は一致が無いと 1 で終わり、標準エラー出力には何も書かない。
		// 1 で終わっても標準エラー出力があれば誤りにする（mise などの shim が rg を見つけられずに 1 で終わったとき。#246 で起きた）。
		return nil
	}
	if unreadableOnly(all) {
		// 読めないファイルがあると、rg はほかの一致を出したうえで 2 で終わる（PR #249 のレビューの推奨 6）。
		return errUnreadable
	}
	if msg != "" {
		return errors.New(msg)
	}
	return werr
}

// errUnreadable は、rg が一部のファイルやディレクトリを読めなかったことを表す。ほかの一致はそのまま出す。
var errUnreadable = errors.New("一部のファイルを読めません")

// unreadableOnly は、rg の標準エラー出力 s が、ファイルを読めなかった知らせ（"rg: <パス>: ...(os error N)"）だけかを返す。
// 正規表現の誤りなどは、この形にならない。
func unreadableOnly(s string) bool {
	if s == "" {
		return false
	}
	for l := range strings.SplitSeq(s, "\n") {
		if l = strings.TrimSpace(l); l == "" {
			continue
		}
		if !strings.HasPrefix(l, "rg: ") || !strings.Contains(l, "(os error ") {
			return false
		}
	}
	return true
}

// limitedWriter は、先頭の n バイトだけを w へ書き、残りを捨てる。
type limitedWriter struct {
	w io.Writer
	n int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.n > 0 {
		k := min(len(p), l.n)
		_, _ = l.w.Write(p[:k])
		l.n -= k
	}
	return len(p), nil
}

// rgEvent は、rg --json の 1 行のうち、使う項目である。
type rgEvent struct {
	Type string `json:"type"`
	Data struct {
		Path       rgText `json:"path"`
		Lines      rgText `json:"lines"`
		LineNumber int    `json:"line_number"`
		Submatches []struct {
			Start int `json:"start"`
			End   int `json:"end"`
		} `json:"submatches"`
	} `json:"data"`
}

// rgText は、rg --json の文字列の値である。UTF-8 でないものは bytes に base64 で入るが、その一致は捨てる。
type rgText struct {
	Text string `json:"text"`
}

// parseRGMatch は、rg --json の 1 行 b が一致（type が match）なら grepMatch にする。
func parseRGMatch(b []byte) (grepMatch, bool) {
	var ev rgEvent
	if err := json.Unmarshal(b, &ev); err != nil || ev.Type != "match" || ev.Data.Path.Text == "" || len(ev.Data.Submatches) == 0 {
		return grepMatch{}, false
	}
	text := strings.TrimRight(ev.Data.Lines.Text, "\r\n")
	m := grepMatch{
		Path: strings.TrimPrefix(ev.Data.Path.Text, "./"),
		Line: ev.Data.LineNumber,
		Col:  ev.Data.Submatches[0].Start,
		Text: text,
	}
	for _, s := range ev.Data.Submatches {
		if s.Start >= 0 && s.End <= len(text) && s.Start < s.End {
			m.Spans = append(m.Spans, [2]int{s.Start, s.End})
		}
	}
	return m, true
}
