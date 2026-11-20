package extmgr

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// CheckoutOptions は、Checkout の設定である。
type CheckoutOptions struct {
	// Git は、git の実行ファイルの名前かパスである。空なら "git" を PATH から探す。
	Git string
	// Env は、git の子プロセスに、呼び出したプロセスの環境変数に足して渡す環境変数（"名前=値"）である（Options.Env と同じ扱い）。
	Env []string
}

// Checkout は、取得元 repo を src のディレクトリに取得し、version のコミットを取り出して、そのハッシュを返す。
// Sync が拡張機能のソースを取得するのと同じ手順（取得、版の解決、取り出し）を、Sync の外から使うためのものである。
// tree-sitter の拡張機能が、設定に並べたパーサのソースを取得するのに使う（FR-142）。
//
// 手順は次のとおりである。
//
//   - src に git のリポジトリが無ければ git clone する。あれば取得元を repo にそろえて git fetch する。
//     pinned のコミットが既にあれば取得しない。壊れたリポジトリは消して取得し直す（Sync の fetch と同じ）
//   - pinned が空でなければ（ロックファイルの記録）、そのコミットを使う。取得元に無ければ誤りを返す。
//     空なら、version をタグ、ブランチ、コミットのハッシュの順に探す
//   - 作業ツリーをそのコミットにし、追跡していないファイルを消す
//
// repo と version は、"-" で始まるもの、空のもの、相対パスの repo を拒む。pinned は 40 桁か 64 桁の小文字の 16 進数にする。
// git が見つからなければ ErrGitNotFound を包んだ誤りを返す。
// git の子プロセスは Sync と同じく、別のプロセスグループで、呼び出したプロセスが終われば SIGKILL を受け取る形で起動する（Start）。
// ctx が終われば、子プロセスのプロセスグループを SIGKILL で止め、ctx.Err() を返す。
func Checkout(ctx context.Context, src, repo, version, pinned string, opts CheckoutOptions) (string, error) {
	switch {
	case repo == "" || strings.HasPrefix(repo, "-"):
		return "", fmt.Errorf("repo %q は空にせず、- で始めない", repo)
	case IsRelativeRepo(repo):
		return "", fmt.Errorf("repo %q は相対パスにしない（URL か絶対パスにする）", repo)
	case version == "" || strings.HasPrefix(version, "-"):
		return "", fmt.Errorf("version %q は空にせず、- で始めない", version)
	case pinned != "" && !fullHash.MatchString(pinned):
		return "", fmt.Errorf("コミット %q は 40 桁か 64 桁の小文字の 16 進数にする", pinned)
	}
	s := newSyncer(Options{Git: opts.Git, Env: opts.Env})
	if _, err := exec.LookPath(s.opts.Git); err != nil {
		return "", fmt.Errorf("%w（%s）: %w", ErrGitNotFound, s.opts.Git, err)
	}
	t := &target{spec: Spec{Repo: repo, Version: version}, pinned: pinned != "", commit: pinned}
	if _, err := s.fetchTo(ctx, src, t); err != nil {
		return "", err
	}
	commit, err := s.resolve(ctx, src, t)
	if err != nil {
		return "", err
	}
	if err := s.checkout(ctx, src, commit); err != nil {
		return "", err
	}
	return commit, nil
}

// Start は、cmd を、Sync が git と go を起動するのと同じ形で起動する。
// 子プロセスを別のプロセスグループにし、呼び出したプロセスが終わったら（SIGKILL で終わったときも）SIGKILL を受け取るようにし、
// cmd を作った ctx が終わったらプロセスグループ全体へ SIGKILL を送る（Linux の場合。procattr_linux.go の start）。
// tree-sitter の拡張機能が C コンパイラを起動するのに使う（FR-142）。cmd.Wait は呼び出し側が呼ぶ。
func Start(cmd *exec.Cmd) error {
	return start(cmd)
}
