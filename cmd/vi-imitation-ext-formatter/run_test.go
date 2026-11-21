package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeScript は、dir/rel に、本文 body の sh のスクリプトを実行できるファイルとして書き、そのパスを返す。
func writeScript(t *testing.T, dir, rel, body string) string {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil { //nolint:gosec // テストの整形ツールは実行できるファイルにする
		t.Fatal(err)
	}
	return p
}

// 実行ファイルの名前は、ファイルのディレクトリから上へ、各ディレクトリの node_modules/.bin と .venv/bin を探し、無ければ PATH から探す。
// 近いディレクトリの依存を先に使い、実行できないファイルは飛ばす。絶対パスと ~/ で始まるパスは、そのまま使う。
//
// req: FR-175
func TestFindCommand(t *testing.T) {
	root := t.TempDir()
	pathDir := filepath.Join(root, "bin")
	t.Setenv("PATH", pathDir)
	t.Setenv("HOME", root)
	inPath := writeScript(t, root, "bin/fmt", "cat")
	top := writeScript(t, root, "proj/node_modules/.bin/fmt", "cat")
	venv := writeScript(t, root, "proj/pkg/.venv/bin/fmt", "cat")
	// 実行できない（実行の権限の無い）ファイルは飛ばす。
	writeScript(t, root, "proj/pkg/sub/node_modules/.bin/fmt", "cat")
	if err := os.Chmod(filepath.Join(root, "proj/pkg/sub/node_modules/.bin/fmt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "proj/pkg/sub/deep"), 0o700); err != nil {
		t.Fatal(err)
	}
	dirs := defaultLocalBinDirs

	for _, c := range []struct {
		name, command, dir string
		dirs               []string
		want               string
	}{
		{"近い .venv/bin", "fmt", filepath.Join(root, "proj/pkg/sub/deep"), dirs, venv},
		{"上の node_modules/.bin", "fmt", filepath.Join(root, "proj"), dirs, top},
		{"依存を探さない設定", "fmt", filepath.Join(root, "proj/pkg"), []string{}, inPath},
		{"プロジェクトの外", "fmt", root, dirs, inPath},
		{"ファイルの無いバッファ", "fmt", "", dirs, inPath},
		{"絶対パス", top, filepath.Join(root, "proj/pkg"), dirs, top},
		{"~/ で始まるパス", "~/bin/fmt", filepath.Join(root, "proj/pkg"), dirs, inPath},
	} {
		got, err := findCommand(c.command, c.dir, c.dirs)
		if err != nil || got != c.want {
			t.Errorf("%s: findCommand = %q, %v; want %q", c.name, got, err, c.want)
		}
	}
	for _, command := range []string{"nosuch", filepath.Join(root, "nosuch"), "~/nosuch"} {
		if _, err := findCommand(command, filepath.Join(root, "proj"), dirs); !errors.Is(err, errNotFound) {
			t.Errorf("findCommand(%q) = %v, want errNotFound", command, err)
		}
	}
}

// プロジェクトの依存は、Git の根（.git のディレクトリか、worktree の .git のファイルのあるディレクトリ）より上へは探さない。
// 有効にした Python の仮想環境（VIRTUAL_ENV）の bin は、プロジェクトの依存の後、PATH の前に探す（PR #341 の再レビューの推奨 2）。
// まだ無いディレクトリのファイルでも、上の在るディレクトリの依存を探す。
//
// req: FR-175
func TestFindCommandGitRootAndVirtualEnv(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PATH", filepath.Join(root, "bin"))
	t.Setenv("VIRTUAL_ENV", "")
	writeScript(t, root, "outer/node_modules/.bin/above", "cat")
	inRepo := writeScript(t, root, "outer/repo/node_modules/.bin/inrepo", "cat")
	// worktree の形の .git のファイル。
	writeScript(t, root, "outer/repo/.git", "")
	writeScript(t, root, "outer/dirrepo/.git/HEAD", "")
	writeScript(t, root, "outer/node_modules/.bin/above2", "cat")
	src := filepath.Join(root, "outer/repo/src/not/yet")

	if got, err := findCommand("inrepo", src, defaultLocalBinDirs); err != nil || got != inRepo {
		t.Errorf("Git の根の依存: %q, %v; want %q", got, err, inRepo)
	}
	for _, dir := range []string{src, filepath.Join(root, "outer/dirrepo/a")} {
		if got, err := findCommand("above", dir, defaultLocalBinDirs); !errors.Is(err, errNotFound) {
			t.Errorf("%s から Git の根の上の依存: %q, %v; want errNotFound", dir, got, err)
		}
	}
	// Git の作業ツリーの外では、ファイルシステムの根までたどる。
	if got, err := findCommand("above2", filepath.Join(root, "outer/plain/a"), defaultLocalBinDirs); err != nil || got != filepath.Join(root, "outer/node_modules/.bin/above2") {
		t.Errorf("Git の作業ツリーの外: %q, %v", got, err)
	}

	// プロジェクトの中の依存は、仮想環境にも同じ名前があっても先に使う。
	writeScript(t, root, "env/bin/inrepo", "cat")
	venv := writeScript(t, root, "env/bin/onlyvenv", "cat")
	writeScript(t, root, "bin/onlyvenv", "cat")
	t.Setenv("VIRTUAL_ENV", filepath.Join(root, "env"))
	if got, err := findCommand("inrepo", src, defaultLocalBinDirs); err != nil || got != inRepo {
		t.Errorf("プロジェクトの依存と VIRTUAL_ENV: %q, %v; want %q", got, err, inRepo)
	}
	// プロジェクトに無ければ、PATH より先に仮想環境のものを使う。
	if got, err := findCommand("onlyvenv", src, defaultLocalBinDirs); err != nil || got != venv {
		t.Errorf("VIRTUAL_ENV: %q, %v; want %q", got, err, venv)
	}
	t.Setenv("VIRTUAL_ENV", "env")
	if got, err := findCommand("onlyvenv", src, defaultLocalBinDirs); err != nil || got != filepath.Join(root, "bin/onlyvenv") {
		t.Errorf("相対パスの VIRTUAL_ENV は使わない: %q, %v", got, err)
	}
}

// existingDir は、無いディレクトリなら在る一番近い上のディレクトリを返す。
func TestExistingDir(t *testing.T) {
	root := t.TempDir()
	if got := existingDir(filepath.Join(root, "a", "b")); got != root {
		t.Errorf("existingDir = %q, want %q", got, root)
	}
	if got := existingDir(root); got != root {
		t.Errorf("existingDir(在る) = %q, want %q", got, root)
	}
}

// findUp は、根に着くまで上へたどり、各ディレクトリで rels を列の順に試す。stop が true を返したディレクトリより上へはたどらない。
func TestFindUp(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"a/b.json", "a/x/c.json", "a/x/b.json"} {
		writeScript(t, root, rel, "")
	}
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }
	for _, c := range []struct {
		dir  string
		rels []string
		want string
	}{
		{"a/x/y", []string{"b.json", "c.json"}, "a/x/b.json"},
		{"a/x/y", []string{"c.json", "b.json"}, "a/x/c.json"},
		{"a", []string{"c.json", "b.json"}, "a/b.json"},
	} {
		got, ok := findUp(filepath.Join(root, c.dir), c.rels, exists, nil)
		if !ok || got != filepath.Join(root, c.want) {
			t.Errorf("findUp(%s, %v) = %q, %v; want %s", c.dir, c.rels, got, ok, c.want)
		}
	}
	if got, ok := findUp(filepath.Join(root, "a"), []string{"nosuch.json"}, exists, nil); ok {
		t.Errorf("findUp = %q, want 見つからない", got)
	}
	// a/x で止めると、a の b.json には届かない。止めたディレクトリそのものは調べる。
	stopX := func(d string) bool { return d == filepath.Join(root, "a", "x") }
	if got, ok := findUp(filepath.Join(root, "a/x/y"), []string{"c.json"}, exists, stopX); !ok || got != filepath.Join(root, "a/x/c.json") {
		t.Errorf("止めたディレクトリの c.json = %q, %v", got, ok)
	}
	if got, ok := findUp(filepath.Join(root, "a/x/y"), []string{"b.json"}, exists, stopX); !ok || got != filepath.Join(root, "a/x/b.json") {
		t.Errorf("止めたディレクトリの b.json = %q, %v", got, ok)
	}
	writeScript(t, root, "a/top.json", "")
	if got, ok := findUp(filepath.Join(root, "a/x/y"), []string{"top.json"}, exists, stopX); ok {
		t.Errorf("止めたディレクトリの上の top.json = %q, want 見つからない", got)
	}
	if _, ok := findUp(filepath.Join(root, "a/x/y"), []string{"top.json"}, exists, nil); !ok {
		t.Error("止めなければ上の top.json に届く")
	}
}

// 整形ツールには中身を標準入力で渡し、標準出力を結果にする。引数の {path} はファイルの絶対パスに置き換え、
// 作業ディレクトリはファイルのディレクトリにする。
// 失敗（見つからない、終了コードが 0 でない、時間の上限を超えた、空の出力、正しくない UTF-8）は、利用者へ知らせる 1 行の誤りにし、
// 終了コードと標準エラー出力の要旨（色の制御シーケンスを除く）を含める。
//
// req: FR-175
func TestRunFormatter(t *testing.T) {
	root := t.TempDir()
	// スクリプトが使う tr、cat、sleep は、システムの PATH から探す。
	t.Setenv("PATH", filepath.Join(root, "bin")+":/usr/bin:/bin")
	writeScript(t, root, "bin/upper", `tr a-z A-Z; echo "$1|$(pwd)" >&2`)
	writeScript(t, root, "bin/args", `printf '%s\n' "$@"; pwd`)
	writeScript(t, root, "bin/fail", `printf '\n\033[31merror\033[0m: line 3: unexpected token\nmore\n' >&2; exit 3`)
	writeScript(t, root, "bin/slow", `sleep 10`)
	writeScript(t, root, "bin/empty", `cat >/dev/null`)
	writeScript(t, root, "bin/binary", `printf '\377\n'`)
	t.Setenv("VIRTUAL_ENV", "")
	cfg, err := parseSettings(nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg.timeout = 300 * time.Millisecond
	path := filepath.Join(root, "src", "a b.txt")
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	out, err := runFormatter(ctx, cfg, formatterConfig{Name: "upper", Command: "upper"}, path, "abc\n")
	if err != nil || out != "ABC\n" {
		t.Errorf("upper = %q, %v", out, err)
	}
	out, err = runFormatter(ctx, cfg, formatterConfig{Name: "args", Command: "args", Args: []string{"--stdin-file-path={path}", "x"}}, path, "")
	if want := "--stdin-file-path=" + path + "\nx\n" + filepath.Dir(path) + "\n"; err != nil || out != want {
		t.Errorf("args = %q, %v; want %q", out, err, want)
	}
	// まだ無いディレクトリの新しいファイル（保存の流れは、親のディレクトリを作る前に整形を問い合わせる）では、
	// 在る一番近い上のディレクトリで走らせる（PR #341 のレビューの必須 2）。
	newPath := filepath.Join(root, "src", "new", "deeper", "n.txt")
	out, err = runFormatter(ctx, cfg, formatterConfig{Name: "args", Command: "args", Args: []string{"{path}"}}, newPath, "")
	if want := newPath + "\n" + filepath.Dir(path) + "\n"; err != nil || out != want {
		t.Errorf("無いディレクトリの args = %q, %v; want %q", out, err, want)
	}

	for _, c := range []struct {
		name string
		f    formatterConfig
		path string
		want string
	}{
		{"見つからない", formatterConfig{Name: "nosuch", Command: "nosuch"}, path, "整形ツール nosuch が見つからない（node_modules/.bin、.venv/bin、PATH を探した）"},
		{"失敗", formatterConfig{Name: "fail", Command: "fail"}, path, "整形ツール fail が失敗した（終了コード 3）: error: line 3: unexpected token"},
		{"時間の上限", formatterConfig{Name: "slow", Command: "slow"}, path, "整形ツール slow が 300ms で終わらなかった"},
		{"空の出力", formatterConfig{Name: "empty", Command: "empty"}, path, "整形ツール empty が空の出力を返したので、適用しなかった"},
		{"正しくない UTF-8", formatterConfig{Name: "binary", Command: "binary"}, path, "整形ツール binary が正しくない UTF-8 を出力したので、適用しなかった"},
		{"パスの要る整形ツール", formatterConfig{Name: "args", Command: "args", Args: []string{"{path}"}}, "", "整形ツール args は引数にファイルのパスが要るので、ファイルの無いバッファを整形できない"},
	} {
		start := time.Now()
		_, err := runFormatter(ctx, cfg, c.f, c.path, "abc\n")
		var fe *formatError
		if !errors.As(err, &fe) || fe.Error() != c.want {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
		if d := time.Since(start); d > 5*time.Second {
			t.Errorf("%s: %v かかった", c.name, d)
		}
	}

	// 空の入力に空の出力を返すのは、失敗にしない。
	if out, err := runFormatter(ctx, cfg, formatterConfig{Name: "empty", Command: "empty"}, path, ""); err != nil || out != "" {
		t.Errorf("空の入力の empty = %q, %v", out, err)
	}

	// 本体が取り消したら（$/cancel）、整形ツールを止めて ctx の誤りを返す。
	cctx, cancel := context.WithCancel(ctx)
	time.AfterFunc(50*time.Millisecond, cancel)
	cfg.timeout = 10 * time.Second
	start := time.Now()
	if _, err := runFormatter(cctx, cfg, formatterConfig{Name: "slow", Command: "slow"}, path, ""); !errors.Is(err, context.Canceled) {
		t.Errorf("取り消した slow: %v, want context.Canceled", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("取り消した slow が %v かかった", d)
	}
}

// 知らせに含める標準エラー出力の要旨は、error を含む最初の行か、無ければ最初の空でない行で、長ければ切る。
func TestGist(t *testing.T) {
	if got := gist("\n  \n\x1b[1;31m✖\x1b[0m bad\nnext"); got != "✖ bad" {
		t.Errorf("gist = %q", got)
	}
	// biome 2.4.15 の構文の誤り（見出しの行の後に理由の行）。
	biome := "format ━━━━━━━━━━━━━━━━━━━━\n\n  × Code formatting aborted due to parsing errors. To format code with errors, enable the 'formatter.formatWithErrors' option.\n  \n"
	if got := gist(biome); !strings.HasPrefix(got, "× Code formatting aborted due to parsing errors.") {
		t.Errorf("biome の gist = %q", got)
	}
	if got := gist(strings.Repeat("あ", maxGist+5)); got != strings.Repeat("あ", maxGist)+"…" {
		t.Errorf("長い行の gist = %q", got)
	}
	if got := gist(""); got != "" {
		t.Errorf("空の gist = %q", got)
	}
}
