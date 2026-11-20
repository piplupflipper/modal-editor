package main

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// ディレクトリの読み込みと gitignore の判定。どちらも木ごとの goroutine（tree.run）か、木を開く前のコマンドの goroutine で呼ぶ。
// ext/server のメッセージを処理する goroutine では呼ばない（main の doc）。

// gitTimeout は、git の 1 回の実行を待つ上限である（仮。cmd/vi-imitation-ext-git と同じ）。
const gitTimeout = 10 * time.Second

// entry は、ディレクトリの子 1 つである。
type entry struct {
	name string
	// dir は、ディレクトリ（ディレクトリを指すシンボリックリンクを含む）であることを表す。
	dir bool
	// ignored は、gitignore されていることを表す（checkIgnore）。
	ignored bool
}

// readDir は、ディレクトリ dir の子を読み、ディレクトリを先に、その中を名前のバイトの順に並べて返す
// （neo-tree の既定の並び。neo-tree.nvim の lua/neo-tree/sources/common/file-items.lua の sort_items）。
// 子の gitignore の判定は checkIgnore で行う。
// 名前が不正な UTF-8 の子は除く。本体とのメッセージは JSON で、不正な UTF-8 を表せず、選んでも同じパスを開けないためである（基本設計 5.3）。
func readDir(ctx context.Context, dir string) ([]entry, error) {
	des, err := os.ReadDir(dir)
	if err != nil && len(des) == 0 {
		return nil, err
	}
	es := make([]entry, 0, len(des))
	for _, d := range des {
		name := d.Name()
		if !utf8.ValidString(name) {
			continue
		}
		isDir := d.IsDir()
		if d.Type()&fs.ModeSymlink != 0 {
			if fi, serr := os.Stat(filepath.Join(dir, name)); serr == nil {
				isDir = fi.IsDir()
			}
		}
		es = append(es, entry{name: name, dir: isDir})
	}
	names := make([]string, len(es))
	for i, e := range es {
		names[i] = e.name
	}
	ignored := checkIgnore(ctx, dir, names)
	for i := range es {
		es[i].ignored = ignored[es[i].name]
	}
	// os.ReadDir は名前の順に返すので、ディレクトリを先にする安定な並べ替えだけでよい。
	slices.SortStableFunc(es, func(a, b entry) int {
		switch {
		case a.dir == b.dir:
			return 0
		case a.dir:
			return -1
		}
		return 1
	})
	return es, nil
}

// checkIgnore は、ディレクトリ dir の子の名前 names のうち、gitignore されたものの集合を返す。
// git check-ignore -z --stdin に名前を渡して判定する（git-check-ignore(1)。追跡しているファイルは、無視の規則に当たっても返らない）。
// 名前の前に ./ を付けるのは、: で始まる名前を pathspec の magic として読ませないためである。
// git のリポジトリの外と、git が無いとき、判定に失敗したとき（期限の超過を含む）は、何も隠さない（空の集合を返す）。
func checkIgnore(ctx context.Context, dir string, names []string) map[string]bool {
	if len(names) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "check-ignore", "-z", "--stdin") //nolint:gosec // 実行するのは git で、dir は木の中のディレクトリ
	cmd.Env = gitEnv(os.Environ())
	var in bytes.Buffer
	for _, n := range names {
		in.WriteString("./")
		in.WriteString(n)
		in.WriteByte(0)
	}
	var out bytes.Buffer
	cmd.Stdin, cmd.Stdout = &in, &out
	if err := startChild(cmd); err != nil {
		return nil
	}
	if err := cmd.Wait(); err != nil {
		// 1 は「どれも無視されていない」、128 はリポジトリの外。どちらも、ほかの失敗も、隠すものは無い。
		return nil
	}
	ignored := map[string]bool{}
	for _, p := range strings.Split(out.String(), "\x00") {
		if p != "" {
			ignored[strings.TrimPrefix(p, "./")] = true
		}
	}
	return ignored
}

// repoEnvVars は、git の子プロセスに渡さない環境変数である。
//
// 本体を GIT_EDITOR にして git commit から起動すると、本体と拡張機能の環境に GIT_DIR や GIT_INDEX_FILE が残る。
// そのまま渡すと、git が木のディレクトリのリポジトリではなく、それらの変数が指すリポジトリを読む。
// 一覧は cmd/vi-imitation-ext-git の repoEnvVars と同じである（そちらの doc に出典がある）。
// 拡張機能の実行ファイルどうしでパッケージを共有しないので写した。
var repoEnvVars = map[string]bool{
	"GIT_ALTERNATE_OBJECT_DIRECTORIES": true,
	"GIT_CONFIG":                       true,
	"GIT_CONFIG_PARAMETERS":            true,
	"GIT_CONFIG_COUNT":                 true,
	"GIT_OBJECT_DIRECTORY":             true,
	"GIT_DIR":                          true,
	"GIT_WORK_TREE":                    true,
	"GIT_IMPLICIT_WORK_TREE":           true,
	"GIT_GRAFT_FILE":                   true,
	"GIT_INDEX_FILE":                   true,
	"GIT_NO_REPLACE_OBJECTS":           true,
	"GIT_REPLACE_REF_BASE":             true,
	"GIT_PREFIX":                       true,
	"GIT_SHALLOW_FILE":                 true,
	"GIT_COMMON_DIR":                   true,
	"GIT_NAMESPACE":                    true,
	"GIT_CEILING_DIRECTORIES":          true,
	"GIT_DISCOVERY_ACROSS_FILESYSTEM":  true,
}

// gitEnv は、環境 env から repoEnvVars を除き、git がインデックスの任意のロックを取らないようにする GIT_OPTIONAL_LOCKS=0 を足したものを返す。
// 読むだけの git を、利用者が別の端末で打つ git と取り合わないためである。
func gitEnv(env []string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if repoEnvVars[name] || name == "GIT_OPTIONAL_LOCKS" {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "GIT_OPTIONAL_LOCKS=0")
}
