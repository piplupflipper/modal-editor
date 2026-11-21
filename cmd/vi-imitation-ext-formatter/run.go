package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// waitDelay は、整形ツールを止めた後、整形ツールが起動した孫プロセスが標準出力を持ったままでも Wait を戻すまでの時間（仮）である。
const waitDelay = 500 * time.Millisecond

// maxStderr は、整形ツールの標準エラー出力を取っておくバイト数の上限である。知らせに使うのは 1 行だけである（gist）。
const maxStderr = 64 << 10

// maxGist は、知らせに含める標準エラー出力の要旨の、文字数の上限である。メッセージの行は画面の幅で切れる。
const maxGist = 200

// findUp は、dir から根へ向かって 1 つずつ上のディレクトリへたどり、各ディレクトリの下の rels の相対パスを列の順に match に渡して、
// match が true を返した最初のパスを返す。見つからなければ ok が false である。
//
// 実行ファイルの探し方（findCommand）が使う。整形ツールの設定ファイル（biome.json、pyproject.toml など）を
// ファイルのディレクトリから上へ探す処理（#339 の「この上に何が載るか」）も、この関数で書ける。
func findUp(dir string, rels []string, match func(path string) bool) (path string, ok bool) {
	for d := filepath.Clean(dir); ; {
		for _, r := range rels {
			if p := filepath.Join(d, r); match(p) {
				return p, true
			}
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", false
		}
		d = parent
	}
}

// isExecutable は、path が実行できる通常のファイル（シンボリックリンクの先を含む）かを返す。
func isExecutable(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0
}

// errNotFound は、整形ツールの実行ファイルが見つからないことを表す。
var errNotFound = errors.New("not found")

// findCommand は、整形ツールの実行ファイルを探して、そのパスを返す（FR-175）。
//
// command が絶対パスか ~/ で始まるパスなら、そのパスを使う。
// 名前なら、ファイルのディレクトリ fileDir から根へ向かって、各ディレクトリの下の localBinDirs（node_modules/.bin、.venv/bin）を探し、
// 無ければ PATH から探す（conform.nvim の util.from_node_modules と同じく、近いプロジェクトの依存を先に使う）。
// fileDir が空（ファイルの無いバッファ）なら PATH だけを探す。見つからなければ errNotFound を返す。
//
// req: FR-175
func findCommand(command, fileDir string, localBinDirs []string) (string, error) {
	if rest, ok := strings.CutPrefix(command, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("%w: %w", errNotFound, err)
		}
		command = filepath.Join(home, rest)
	}
	if filepath.IsAbs(command) {
		if isExecutable(command) {
			return command, nil
		}
		return "", errNotFound
	}
	if fileDir != "" && len(localBinDirs) > 0 {
		rels := make([]string, len(localBinDirs))
		for i, d := range localBinDirs {
			rels[i] = filepath.Join(d, command)
		}
		if p, ok := findUp(fileDir, rels, isExecutable); ok {
			return p, nil
		}
	}
	p, err := exec.LookPath(command)
	if err != nil {
		return "", errNotFound
	}
	return p, nil
}

// formatError は、整形ツールが失敗したことを表す。Error は、利用者へ知らせる日本語の 1 行である。
type formatError struct{ msg string }

func (e *formatError) Error() string { return e.msg }

func failf(format string, args ...any) error { return &formatError{msg: fmt.Sprintf(format, args...)} }

// runFormatter は、整形ツール f に input を標準入力で渡して走らせ、標準出力を返す（FR-175）。
//
// path はバッファのファイルの絶対パスで、Args の {path} を置き換え、そのディレクトリを作業ディレクトリにする（設定ファイルを
// ファイルの近くから探す整形ツールのため）。path が空なら作業ディレクトリは拡張機能のもの（本体の作業ディレクトリ）で、
// {path} を含む Args の整形ツールは走らせずに失敗にする。
//
// 失敗は *formatError で返す。見つからない、起動できない、終了コードが 0 でない、時間の上限（cfg.timeout）を超えた、
// 正しくない UTF-8 を出力した、空でない入力に空白だけの出力を返した（conform.nvim と同じく、対象外のファイルで何も出さない整形ツールのため）
// のどれかである。ctx が取り消されたら（本体の $/cancel）、ctx.Err() を返す。
//
// req: FR-175
func runFormatter(ctx context.Context, cfg *config, f formatterConfig, path, input string) (string, error) {
	dir := ""
	if path != "" {
		dir = filepath.Dir(path)
	}
	exe, err := findCommand(f.Command, dir, cfg.localBinDirs)
	if err != nil {
		where := "PATH"
		if dir != "" && len(cfg.localBinDirs) > 0 {
			where = strings.Join(cfg.localBinDirs, "、") + "、PATH"
		}
		return "", failf("整形ツール %s が見つからない（%s を探した）", f.Name, where)
	}
	args := make([]string, len(f.Args))
	for i, a := range f.Args {
		if strings.Contains(a, pathPlaceholder) && path == "" {
			return "", failf("整形ツール %s は引数にファイルのパスが要るので、ファイルの無いバッファを整形できない", f.Name)
		}
		args[i] = strings.ReplaceAll(a, pathPlaceholder, path)
	}

	runCtx, cancel := context.WithTimeout(ctx, cfg.timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, exe, args...) //nolint:gosec // 利用者が設定に書いた整形ツールを走らせる
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(input)
	var stdout bytes.Buffer
	stderr := &limitedBuffer{max: maxStderr}
	cmd.Stdout = &stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = waitDelay
	if err = startFormatter(cmd); err != nil {
		return "", failf("整形ツール %s を起動できない: %v", f.Name, err)
	}
	err = cmd.Wait()
	switch {
	case ctx.Err() != nil:
		return "", ctx.Err()
	case runCtx.Err() != nil:
		return "", failf("整形ツール %s が %v で終わらなかった", f.Name, cfg.timeout)
	case err != nil:
		msg := fmt.Sprintf("整形ツール %s が失敗した", f.Name)
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() >= 0 {
			msg += fmt.Sprintf("（終了コード %d）", exit.ExitCode())
		} else {
			msg += fmt.Sprintf("（%v）", err)
		}
		if g := gist(stderr.String()); g != "" {
			msg += ": " + g
		}
		return "", &formatError{msg: msg}
	}
	out := stdout.String()
	if !utf8.ValidString(out) {
		return "", failf("整形ツール %s が正しくない UTF-8 を出力したので、適用しなかった", f.Name)
	}
	if strings.TrimSpace(out) == "" && strings.TrimSpace(input) != "" {
		return "", failf("整形ツール %s が空の出力を返したので、適用しなかった", f.Name)
	}
	return out, nil
}

// ansiEscape は、端末の色などの制御シーケンス（CSI）である。知らせの行から除く。
var ansiEscape = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]")

// gist は、整形ツールの標準エラー出力の要旨として、制御シーケンスを除いた空でない行のうち、
// error を含む（大文字と小文字を区別しない）最初の行を、無ければ最初の行を、maxGist 文字までで返す。
// biome 2.4.15 は、構文の誤りで整形をやめたとき、最初の行に見出し（format ━━━…）を、その後の行に
// 「× Code formatting aborted due to parsing errors.…」を出す（2026-11-21 に確かめた）。見出しより理由の行を知らせる。
func gist(stderr string) string {
	first := ""
	for line := range strings.SplitSeq(ansiEscape.ReplaceAllString(stderr, ""), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if first == "" {
			first = line
		}
		if strings.Contains(strings.ToLower(line), "error") {
			first = line
			break
		}
	}
	if !utf8.ValidString(first) {
		first = strings.ToValidUTF8(first, "?")
	}
	if r := []rune(first); len(r) > maxGist {
		first = string(r[:maxGist]) + "…"
	}
	return first
}

// limitedBuffer は、max バイトまでを取っておき、残りを捨てる io.Writer である。
type limitedBuffer struct {
	buf bytes.Buffer
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.buf.Len(); room > 0 {
		b.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string { return b.buf.String() }
