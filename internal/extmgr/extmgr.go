// Package extmgr は、拡張機能の取得、ビルド、更新、削除と、ロックファイルを持つ（基本設計 5.6。要件定義 4.13）。
//
// 設定ファイルの extensions.install に並べた拡張機能を、git のリポジトリから取得し、go build でビルドして、
// 利用者の拡張機能のディレクトリ（$XDG_DATA_HOME/modal-editor/extensions/<名前>/）に置く。
// 拡張機能のプロセスの管理（起動、停止、入れ替えた拡張機能の再起動）は持たない（internal/exthost）。
//
// 基本設計 2 章の層では、internal/exthost と internal/config と同じ「機能」の層に置く。
// 土台の ext だけを import する。設定は呼び出し側が config.ExtensionInstall を Spec に写して渡す。
//
// # 呼び出し方
//
// Sync は git と go の子プロセスを起動し、ネットワークに出るので、数秒から数分かかる。
// イベントループの goroutine から呼ばず、別の goroutine で呼ぶ（基本設計 2.1、5.6）。
// 進み具合は Options.Progress で、Sync を呼んだ goroutine の上で知らせる。
// 同じディレクトリに対して Sync を同時に 2 つ呼ばない（呼び出し側が 1 つずつにする）。
//
// # ディレクトリ
//
//   - Options.ExtensionsDir: 拡張機能を置くディレクトリ。<名前>/ の下に、宣言ファイル（manifest.json）と、
//     宣言ファイルの command の名前でビルドした実行ファイルと、Sync が入れた印（MarkerFileName）を置く
//   - Options.WorkDir: 取得したソース（src/<名前>/ に git clone したもの）と、ビルドと入れ替えの途中のディレクトリ（tmp/）。
//     入れ替えをディレクトリの名前の変更で行うので、ExtensionsDir と同じファイルシステムに置く。
//     ExtensionsDir の下に置かないのは、本体（config.LoadExtensions）が途中のディレクトリを拡張機能として読まないようにするためである
//   - Options.LockPath: ロックファイル（LockFileName。設定ファイルと同じディレクトリ）
//
// # 取得元のリポジトリの形（仮）
//
// リポジトリの最上位に、宣言ファイル manifest.json と、main パッケージを置く。
// Sync は最上位で go build -trimpath -o <command> . を実行する。
// 宣言ファイルの command は、宣言ファイルと同じディレクトリのファイルの名前（"./<名前>" か "<名前>"）にする。
// 宣言ファイルと実行ファイルの他のファイル（スクリプトなど）は、いまは置かない。
//
// # テストの環境
//
// テストはネットワークに出ず、利用者の Go のキャッシュと GOPATH を汚さない。
// TestMain が一時ディレクトリを 1 つ作り、git と go の子プロセスに Options.Env で次を渡す。
//
//   - GOCACHE、GOMODCACHE、GOPATH: 一時ディレクトリの下。GOCACHE はテストの間で共有し（最初のビルドだけが標準ライブラリをコンパイルする）、
//     終わったら一時ディレクトリごと消す
//   - GOFLAGS=-modcacherw: 利用者の GOFLAGS を上書きし、モジュールのキャッシュを書き込める許可属性にして消せるようにする。
//     GOENV=off で、利用者の go env -w の設定も読まない
//   - GOPROXY=off、GOTOOLCHAIN=local、GOWORK=off、CGO_ENABLED=0: モジュールとツールチェインを取りに行かず、C コンパイラも使わない。
//     テストの拡張機能は標準ライブラリだけを使う
//   - GIT_CONFIG_GLOBAL=/dev/null、GIT_CONFIG_NOSYSTEM=1: 利用者の git の設定（署名やフック）を読まない
//
// 取得元のリポジトリは、テストが一時ディレクトリに git init で作り、パスを repo に書く。
package extmgr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/tailscale/hujson"

	"github.com/piplupflipper/modal-editor/ext"
)

// ManifestFileName は、宣言ファイルの名前である（基本設計 5.1。config.ManifestFileName と同じ）。
const ManifestFileName = "manifest.json"

// MarkerFileName は、Sync が入れた拡張機能のディレクトリに置く印のファイルの名前である（仮）。
// 中身は、入れたときの LockEntry の JSON である。
// この印の無いディレクトリ（利用者が手で置いたもの）は、Sync が上書きも削除もしない（FR-122）。
const MarkerFileName = ".extmgr.json"

// Spec は、取得する拡張機能の 1 つである（設定の extensions.install の 1 項目）。
// 項目の名前と型と並びは config.ExtensionInstall と同じにしてあり、呼び出し側は Spec(e) で変換できる。
type Spec struct {
	// Name は、拡張機能を置くディレクトリの名前である。英数字で始まり、英数字と "."、"_"、"-" だけからなる。
	Name string
	// Repo は、取得元の git のリポジトリ（git clone に渡す URL かパス）である。
	Repo string
	// Version は、取り出す版である。タグ、ブランチ、コミットのハッシュ（4 桁以上の 16 進数）の順に探す。
	Version string
}

// Options は、Sync の設定である。
type Options struct {
	// ExtensionsDir は、拡張機能を置くディレクトリ（$XDG_DATA_HOME/modal-editor/extensions）である。省けない。
	ExtensionsDir string
	// WorkDir は、取得したソースと、ビルドと入れ替えの途中のディレクトリを置くディレクトリである。
	// 空なら ExtensionsDir の隣の extmgr（$XDG_DATA_HOME/modal-editor/extmgr）を使う。
	WorkDir string
	// LockPath は、ロックファイルのパス（設定ファイルと同じディレクトリの LockFileName）である。省けない。
	LockPath string
	// Update は、true なら、ロックファイルの記録を使わず、すべての版を取得元で解決し直す。
	// false なら、ロックファイルに記録があり、設定の repo と version が記録と同じ拡張機能は、記録したコミットを取り出す
	// （基本設計 5.6。他の環境で同じコミットを使えるようにする）。ブランチの版を最新にするときに true にする。
	Update bool
	// Git と Go は、git と go の実行ファイルの名前かパスである。空なら "git" と "go" を PATH から探す。
	Git, Go string
	// Env は、git と go の子プロセスに、本体の環境変数に足して渡す環境変数（"名前=値"）である。同じ名前なら Env を使う。
	Env []string
	// Progress は、拡張機能ごとの処理の段階が変わるたびに呼ぶ関数である。nil なら呼ばない。
	Progress func(Event)
}

// Phase は、Sync の処理の段階である。
type Phase int

const (
	// PhaseFetch は、取得元から git clone か git fetch をしていることを表す。
	PhaseFetch Phase = iota + 1
	// PhaseBuild は、go build をしていることを表す。
	PhaseBuild
	// PhaseInstall は、ビルドしたものを ExtensionsDir に入れていることを表す。
	PhaseInstall
	// PhaseRemove は、設定に並べなくなった拡張機能を削除していることを表す。
	PhaseRemove
)

// String は、メッセージの行に出す段階の名前を返す。
func (p Phase) String() string {
	switch p {
	case PhaseFetch:
		return "取得"
	case PhaseBuild:
		return "ビルド"
	case PhaseInstall:
		return "入れ替え"
	case PhaseRemove:
		return "削除"
	}
	return fmt.Sprintf("Phase(%d)", int(p))
}

// Event は、Options.Progress に渡す進み具合である。
type Event struct {
	// Name は、拡張機能の名前（Spec.Name）である。
	Name string
	// Phase は、始めた段階である。
	Phase Phase
}

// Action は、Sync が 1 つの拡張機能に対して行ったことである。
type Action int

const (
	// ActionUnchanged は、入れてあるものが目的のコミットと同じなので、何もしなかったことを表す。
	ActionUnchanged Action = iota + 1
	// ActionInstalled は、新しく入れたことを表す（FR-102）。
	ActionInstalled
	// ActionUpdated は、別のコミットのものと入れ替えたことを表す（FR-121）。稼働中なら、呼び出し側が止めて起動し直す。
	ActionUpdated
	// ActionRemoved は、設定に並べなくなったので削除したことを表す（FR-122）。
	ActionRemoved
	// ActionKept は、設定に並べなくなったが、Sync が入れた印が無いので削除しなかったことを表す。
	// ロックファイルの記録は消す。
	ActionKept
	// ActionFailed は、失敗したことを表す。Outcome.Err に理由がある。入れてあったものとロックファイルの記録は元のまま残る。
	ActionFailed
)

// String は、メッセージの行に出す名前を返す。
func (a Action) String() string {
	switch a {
	case ActionUnchanged:
		return "変更なし"
	case ActionInstalled:
		return "追加"
	case ActionUpdated:
		return "更新"
	case ActionRemoved:
		return "削除"
	case ActionKept:
		return "手で置いたものなので残した"
	case ActionFailed:
		return "失敗"
	}
	return fmt.Sprintf("Action(%d)", int(a))
}

// Outcome は、1 つの拡張機能の結果である。
type Outcome struct {
	// Name は、拡張機能の名前である。
	Name string
	// Action は、行ったことである。
	Action Action
	// Commit は、入れてあるコミットである。ActionRemoved、ActionKept と、何も入っていない ActionFailed では空。
	Commit string
	// Err は、ActionFailed の理由である。
	Err error
}

// ErrGoNotFound は、拡張機能のビルドに要る go が見つからないことを表す（NFR-06 の注。利用者の環境に Go が要る）。
var ErrGoNotFound = errors.New("拡張機能のビルドに Go が要るが、go が見つからない")

// ErrGitNotFound は、拡張機能の取得に要る git が見つからないことを表す。
var ErrGitNotFound = errors.New("拡張機能の取得に git が要るが、git が見つからない")

// validName は、Spec.Name に使える名前である（config の extensions.install の検査と同じ）。
var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// hexVersion は、コミットのハッシュとして扱う版である。
var hexVersion = regexp.MustCompile(`^[0-9a-f]{4,64}$`)

// Sync は、specs に並べた拡張機能を、取得し、ビルドし、ExtensionsDir に入れ、ロックファイルに記録する（基本設計 5.6）。
//
// 拡張機能ごとに次のとおりにする。
//
//   - 目的のコミットを決める。ロックファイルに記録があり、repo と version が記録と同じで、Update が false なら記録したコミット。
//     そうでなければ、取得元から取得して version を解決したコミット
//   - 入れてあるもの（印の記録）が目的のコミットと同じなら、何もしない（ActionUnchanged）。
//     ロックファイルの記録が使えるときは、git も go も起動しない
//   - そうでなければ、取り出してビルドし、入れ替える（ActionInstalled、ActionUpdated。FR-102、FR-121）。
//     ビルドまでを WorkDir で行い、成功してから ExtensionsDir の <名前>/ をディレクトリの名前の変更で入れ替える。
//     途中で失敗したら、入れてあったものを元のまま残す（ActionFailed）
//
// その後、ロックファイルに記録があり specs に無い名前を削除する（FR-122）。
// 削除するのは、Sync が入れた印のあるディレクトリだけで、印の無いもの（利用者が手で置いたもの）は残す（ActionKept）。
// ロックファイルに記録の無い名前のディレクトリには触らない。
// specs に並べた名前のディレクトリが、印の無いまま既にあるときも、上書きせずに失敗にする。
//
// 最後に、成功したものの記録をロックファイルに書く（FR-103）。失敗したものは前の記録を残す。
//
// 1 つの拡張機能の失敗では止めず、Outcome に入れて次へ進む。戻り値の []Outcome は specs の順、その後に削除の結果を名前の順に並べる。
// 次のときは、何も変えずに誤りを返す。
//
//   - Options の ExtensionsDir か LockPath が空のとき
//   - ロックファイルを読めないとき
//   - 取り出してビルドするものがあり、go か git が見つからないとき（ErrGoNotFound、ErrGitNotFound）
//
// ctx が終わったら、残りの拡張機能を ActionFailed にし、削除をせず、それまでの結果をロックファイルに書いて ctx.Err() を返す。
// ロックファイルを書けないときも誤りを返す。
func Sync(ctx context.Context, specs []Spec, opts Options) ([]Outcome, error) {
	return newSyncer(opts).sync(ctx, specs)
}

// syncer は、1 回の Sync の状態である。
type syncer struct {
	opts Options
	// rename は、入れ替えに使うディレクトリの名前の変更である。テストが途中の失敗を起こすために差し替える。
	rename func(oldpath, newpath string) error
}

func newSyncer(opts Options) *syncer {
	if opts.WorkDir == "" && opts.ExtensionsDir != "" {
		opts.WorkDir = filepath.Join(filepath.Dir(opts.ExtensionsDir), "extmgr")
	}
	if opts.Git == "" {
		opts.Git = "git"
	}
	if opts.Go == "" {
		opts.Go = "go"
	}
	return &syncer{opts: opts, rename: os.Rename}
}

// target は、1 つの拡張機能について、Sync の前に決めたことである。
type target struct {
	spec Spec
	// pinned は、ロックファイルに使える記録があることを表す。commit がその記録である。
	pinned bool
	commit string
	// installed は、入れてあるものの印の記録である。印が無ければ nil。
	installed *LockEntry
	// err は、Spec の誤りか、印の無いディレクトリが既にあることである。
	err error
}

// upToDate は、取得もビルドもせずに済むことを返す。
func (t *target) upToDate() bool {
	return t.err == nil && t.pinned && t.installed != nil && t.installed.Commit == t.commit && t.installed.Repo == t.spec.Repo
}

func (s *syncer) sync(ctx context.Context, specs []Spec) ([]Outcome, error) {
	if s.opts.ExtensionsDir == "" || s.opts.LockPath == "" {
		return nil, errors.New("extmgr: Options の ExtensionsDir と LockPath は省けない")
	}
	lock, err := ReadLock(s.opts.LockPath)
	if err != nil {
		return nil, err
	}

	targets := make([]target, len(specs))
	seen := map[string]bool{}
	needBuild := false
	for i, sp := range specs {
		t := &targets[i]
		t.spec = sp
		if t.err = validateSpec(sp); t.err == nil && seen[sp.Name] {
			t.err = fmt.Errorf("拡張機能の名前 %q が重なっている", sp.Name)
		}
		seen[sp.Name] = true
		if t.err != nil {
			continue
		}
		if le, ok := lock.Extensions[sp.Name]; ok && !s.opts.Update && le.Repo == sp.Repo && le.Version == sp.Version {
			t.pinned, t.commit = true, le.Commit
		}
		t.installed, t.err = s.readMarker(sp.Name)
		if !t.upToDate() && t.err == nil {
			needBuild = true
		}
	}
	if needBuild {
		if _, err := exec.LookPath(s.opts.Go); err != nil {
			return nil, fmt.Errorf("%w（%s）: %w", ErrGoNotFound, s.opts.Go, err)
		}
		if _, err := exec.LookPath(s.opts.Git); err != nil {
			return nil, fmt.Errorf("%w（%s）: %w", ErrGitNotFound, s.opts.Git, err)
		}
	}

	outcomes := make([]Outcome, 0, len(specs))
	for i := range targets {
		t := &targets[i]
		o := Outcome{Name: t.spec.Name}
		switch {
		case t.err != nil:
			o.Action, o.Err = ActionFailed, t.err
		case ctx.Err() != nil:
			o.Action, o.Err = ActionFailed, ctx.Err()
		case t.upToDate():
			o.Action, o.Commit = ActionUnchanged, t.commit
		default:
			o = s.install(ctx, t)
		}
		if o.Action == ActionFailed {
			if t.installed != nil {
				o.Commit = t.installed.Commit
			}
		} else {
			lock.Extensions[t.spec.Name] = LockEntry{Repo: t.spec.Repo, Version: t.spec.Version, Commit: o.Commit}
		}
		outcomes = append(outcomes, o)
	}

	if ctx.Err() == nil {
		outcomes = append(outcomes, s.removeUnlisted(&lock, seen)...)
	}
	if err := writeLock(s.opts.LockPath, &lock); err != nil {
		return outcomes, err
	}
	return outcomes, ctx.Err()
}

// validateSpec は、sp の項目を検査する。repo と version は git の引数になるので、"-" で始まるものを拒む。
func validateSpec(sp Spec) error {
	switch {
	case !validName.MatchString(sp.Name):
		return fmt.Errorf("拡張機能の名前 %q は英数字で始め、英数字と . _ - だけにする", sp.Name)
	case sp.Repo == "" || strings.HasPrefix(sp.Repo, "-"):
		return fmt.Errorf("拡張機能 %s の repo %q は空にせず、- で始めない", sp.Name, sp.Repo)
	case sp.Version == "" || strings.HasPrefix(sp.Version, "-"):
		return fmt.Errorf("拡張機能 %s の version %q は空にせず、- で始めない", sp.Name, sp.Version)
	}
	return nil
}

// readMarker は、ExtensionsDir の name のディレクトリの印を読む。
// ディレクトリが無ければ nil, nil、ディレクトリがあり印が無ければ（利用者が手で置いたもの）誤りを返す。
func (s *syncer) readMarker(name string) (*LockEntry, error) {
	dir := filepath.Join(s.opts.ExtensionsDir, name)
	data, err := os.ReadFile(filepath.Join(dir, MarkerFileName))
	if err == nil {
		var le LockEntry
		if jerr := json.Unmarshal(data, &le); jerr != nil {
			return nil, fmt.Errorf("拡張機能 %s の印 %s を読めない: %w", name, MarkerFileName, jerr)
		}
		return &le, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("拡張機能 %s の印を読めない: %w", name, err)
	}
	if _, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return nil, fmt.Errorf("%s は extensions.install で入れたものではない（%s が無い）ので、上書きしない", dir, MarkerFileName)
}

func (s *syncer) progress(name string, p Phase) {
	if s.opts.Progress != nil {
		s.opts.Progress(Event{Name: name, Phase: p})
	}
}

// install は、t を取得し、ビルドし、入れ替える。
func (s *syncer) install(ctx context.Context, t *target) Outcome {
	name := t.spec.Name
	fail := func(err error) Outcome {
		return Outcome{Name: name, Action: ActionFailed, Err: fmt.Errorf("拡張機能 %s: %w", name, err)}
	}
	s.progress(name, PhaseFetch)
	src, err := s.fetch(ctx, t)
	if err != nil {
		return fail(err)
	}
	commit, err := s.resolve(ctx, src, t)
	if err != nil {
		return fail(err)
	}
	if t.installed != nil && t.installed.Commit == commit && t.installed.Repo == t.spec.Repo {
		return Outcome{Name: name, Action: ActionUnchanged, Commit: commit}
	}
	if _, err = s.git(ctx, src, "-c", "advice.detachedHead=false", "checkout", "--quiet", "--force", "--detach", commit); err != nil {
		return fail(err)
	}
	if _, err = s.git(ctx, src, "clean", "-ffdxq"); err != nil {
		return fail(err)
	}

	s.progress(name, PhaseBuild)
	staging, err := s.build(ctx, src, LockEntry{Repo: t.spec.Repo, Version: t.spec.Version, Commit: commit})
	if staging != "" {
		defer os.RemoveAll(staging) //nolint:errcheck // 途中のディレクトリの後始末で、残っても次の Sync の動作に影響しない
	}
	if err != nil {
		return fail(err)
	}

	s.progress(name, PhaseInstall)
	if err := s.swap(name, filepath.Join(staging, "ext")); err != nil {
		return fail(err)
	}
	if t.installed != nil {
		return Outcome{Name: name, Action: ActionUpdated, Commit: commit}
	}
	return Outcome{Name: name, Action: ActionInstalled, Commit: commit}
}

// srcDir は、name のソースを取得するディレクトリである。
func (s *syncer) srcDir(name string) string {
	return filepath.Join(s.opts.WorkDir, "src", name)
}

// fetch は、取得元を WorkDir の src/<名前> に git clone するか、既にあれば git fetch する。
// ロックファイルの記録を使うときは、記録したコミットが既にあれば取得しない。
// 取得したディレクトリを返す。
func (s *syncer) fetch(ctx context.Context, t *target) (string, error) {
	src := s.srcDir(t.spec.Name)
	if _, err := os.Stat(filepath.Join(src, ".git")); err != nil {
		if err := os.RemoveAll(src); err != nil {
			return "", err
		}
		if err := os.MkdirAll(filepath.Dir(src), 0o750); err != nil {
			return "", err
		}
		if _, err := s.git(ctx, "", "clone", "--quiet", "--no-checkout", "--", t.spec.Repo, src); err != nil {
			return "", err
		}
		return src, nil
	}
	url, err := s.git(ctx, src, "remote", "get-url", "origin")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(url) != t.spec.Repo {
		if _, err := s.git(ctx, src, "remote", "set-url", "origin", t.spec.Repo); err != nil {
			return "", err
		}
	} else if t.pinned && s.hasCommit(ctx, src, t.commit) {
		return src, nil
	}
	if _, err := s.git(ctx, src, "fetch", "--quiet", "--tags", "--force", "--prune", "origin"); err != nil {
		return "", err
	}
	return src, nil
}

// hasCommit は、src にコミット commit があるかを返す。
func (s *syncer) hasCommit(ctx context.Context, src, commit string) bool {
	_, err := s.git(ctx, src, "cat-file", "-e", commit+"^{commit}")
	return err == nil
}

// resolve は、t の目的のコミットのハッシュを返す。
// ロックファイルの記録を使うときはそのコミット、そうでなければ version をタグ、ブランチ、コミットのハッシュの順に探す。
func (s *syncer) resolve(ctx context.Context, src string, t *target) (string, error) {
	if t.pinned {
		if !hexVersion.MatchString(t.commit) || !s.hasCommit(ctx, src, t.commit) {
			return "", fmt.Errorf("ロックファイルに記録したコミット %q が取得元に無い", t.commit)
		}
		return t.commit, nil
	}
	v := t.spec.Version
	refs := []string{"refs/tags/" + v, "refs/remotes/origin/" + v}
	if hexVersion.MatchString(v) {
		refs = append(refs, v)
	}
	for _, ref := range refs {
		out, err := s.git(ctx, src, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
		if err == nil {
			return strings.TrimSpace(out), nil
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
	}
	return "", fmt.Errorf("版 %q が取得元のタグにもブランチにもコミットにも無い", v)
}

// build は、src の宣言ファイルを読んで検査し、WorkDir の tmp の下の新しいディレクトリの ext/ に、
// 宣言ファイルと、command の名前でビルドした実行ファイルと、印を置く。
// 作ったディレクトリ（ext/ の親）を返す。失敗しても、作ったディレクトリがあれば返す（呼び出し側が消す）。
func (s *syncer) build(ctx context.Context, src string, entry LockEntry) (string, error) {
	manifest, command, err := readManifest(src)
	if err != nil {
		return "", err
	}
	tmpRoot := filepath.Join(s.opts.WorkDir, "tmp")
	if err = os.MkdirAll(tmpRoot, 0o750); err != nil {
		return "", err
	}
	staging, err := os.MkdirTemp(tmpRoot, filepath.Base(src)+"-")
	if err != nil {
		return "", err
	}
	out := filepath.Join(staging, "ext")
	if err = os.Mkdir(out, 0o755); err != nil { //nolint:gosec // G301: ExtensionsDir の下の拡張機能のディレクトリになるので、同梱の拡張機能（make dist）と同じ許可属性にする
		return staging, err
	}
	if _, err = s.run(ctx, src, s.opts.Go, "build", "-trimpath", "-o", filepath.Join(out, command), "."); err != nil {
		return staging, err
	}
	marker, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return staging, err
	}
	for _, f := range []struct {
		name string
		data []byte
	}{{ManifestFileName, manifest}, {MarkerFileName, append(marker, '\n')}} {
		if err := os.WriteFile(filepath.Join(out, f.name), f.data, 0o644); err != nil { //nolint:gosec // G306: 秘密を含まない宣言ファイルなので、同梱の拡張機能と同じ許可属性にする
			return staging, err
		}
	}
	return staging, nil
}

// readManifest は、src の宣言ファイルを読み、ext.Manifest.Validate で検査して、中身と command のファイルの名前を返す。
// command は、宣言ファイルと同じディレクトリのファイルの名前（"./<名前>" か "<名前>"）でなければならない。
func readManifest(src string) (data []byte, command string, err error) {
	data, err = os.ReadFile(filepath.Join(src, ManifestFileName))
	if err != nil {
		return nil, "", fmt.Errorf("取得元の最上位に宣言ファイル %s が無い: %w", ManifestFileName, err)
	}
	std, err := hujson.Standardize(slices.Clone(data))
	if err != nil {
		return nil, "", fmt.Errorf("宣言ファイルを読めない: %w", err)
	}
	var m ext.Manifest
	if err := json.Unmarshal(std, &m); err != nil {
		return nil, "", fmt.Errorf("宣言ファイルを読めない: %w", err)
	}
	if _, err := m.Validate(); err != nil {
		return nil, "", fmt.Errorf("宣言ファイルの誤り: %w", err)
	}
	command = strings.TrimPrefix(m.Command, "./")
	if command == "" || command == "." || command == ".." || strings.ContainsRune(command, '/') ||
		command == ManifestFileName || command == MarkerFileName {
		return nil, "", fmt.Errorf("宣言ファイルの command %q は、宣言ファイルと同じディレクトリのファイルの名前にする", m.Command)
	}
	return data, command, nil
}

// swap は、newDir を ExtensionsDir の name に入れる。既にあれば入れ替える。
// 前のものを WorkDir の tmp の下へ名前の変更で退けてから newDir の名前を変え、失敗したら前のものを戻す。
func (s *syncer) swap(name, newDir string) error {
	if err := os.MkdirAll(s.opts.ExtensionsDir, 0o755); err != nil { //nolint:gosec // G301: 本体が拡張機能を探すディレクトリで、同梱の拡張機能と同じ許可属性にする
		return err
	}
	dest := filepath.Join(s.opts.ExtensionsDir, name)
	if _, err := os.Lstat(dest); errors.Is(err, fs.ErrNotExist) {
		return s.rename(newDir, dest)
	}
	oldParent, err := os.MkdirTemp(filepath.Join(s.opts.WorkDir, "tmp"), name+"-old-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(oldParent) //nolint:errcheck // 退けた前の版の後始末で、残っても次の Sync の動作に影響しない
	old := filepath.Join(oldParent, "ext")
	if err := s.rename(dest, old); err != nil {
		return fmt.Errorf("前の版を退けられない: %w", err)
	}
	if err := s.rename(newDir, dest); err != nil {
		if rerr := s.rename(old, dest); rerr != nil {
			return fmt.Errorf("新しい版を入れられず（%w）、前の版を %s から戻せない: %w", err, old, rerr)
		}
		return fmt.Errorf("新しい版を入れられない: %w", err)
	}
	return nil
}

// removeUnlisted は、ロックファイルに記録があり、listed に無い名前を削除し、ロックファイルの記録を消す（FR-122）。
// 印の無いディレクトリは残す。結果を名前の順に返す。
func (s *syncer) removeUnlisted(lock *Lock, listed map[string]bool) []Outcome {
	var names []string
	for name := range lock.Extensions {
		if !listed[name] {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	var outcomes []Outcome
	for _, name := range names {
		o := Outcome{Name: name, Action: ActionRemoved}
		if !validName.MatchString(name) {
			// 手で書き換えたロックファイルの名前でパスの外を消さないように、ディレクトリには触らない。
			o.Action = ActionKept
			delete(lock.Extensions, name)
			outcomes = append(outcomes, o)
			continue
		}
		s.progress(name, PhaseRemove)
		dir := filepath.Join(s.opts.ExtensionsDir, name)
		_, err := os.Lstat(filepath.Join(dir, MarkerFileName))
		switch {
		case err == nil:
			if rerr := os.RemoveAll(dir); rerr != nil {
				o.Action, o.Err = ActionFailed, fmt.Errorf("拡張機能 %s を削除できない: %w", name, rerr)
				outcomes = append(outcomes, o)
				continue
			}
		case errors.Is(err, fs.ErrNotExist):
			if _, derr := os.Lstat(dir); derr == nil {
				o.Action = ActionKept
			}
		default:
			o.Action, o.Err = ActionFailed, fmt.Errorf("拡張機能 %s の印を調べられない: %w", name, err)
			outcomes = append(outcomes, o)
			continue
		}
		// 取得したソースは、削除できなくても次の Sync で取得し直すので、誤りにしない。
		_ = os.RemoveAll(s.srcDir(name))
		delete(lock.Extensions, name)
		outcomes = append(outcomes, o)
	}
	return outcomes
}

// git は、dir で git を実行し、標準出力を返す。
// 認証の問い合わせで止まらないように、GIT_TERMINAL_PROMPT=0 を渡す。
func (s *syncer) git(ctx context.Context, dir string, args ...string) (string, error) {
	return s.run(ctx, dir, s.opts.Git, args...)
}

// waitDelay は、子プロセスが終わった後、孫のプロセスが握った出力の口が閉じるのを待つ上限である。
const waitDelay = 5 * time.Second

// run は、dir で name を実行し、標準出力を返す。
// 失敗したときは、標準エラー出力を誤りの文言に含める。
func (s *syncer) run(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // G204: 起動するのは git と go で、引数は Sync が組み立てたもの（repo と version は - で始まらないことを検査済み）
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	cmd.Env = append(cmd.Env, s.opts.Env...)
	cmd.WaitDelay = waitDelay
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := start(cmd)
	if err == nil {
		err = cmd.Wait()
	}
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			msg = ":\n" + msg
		}
		return "", fmt.Errorf("%s %s が失敗した（%w）%s", filepath.Base(name), strings.Join(args, " "), err, msg)
	}
	return stdout.String(), nil
}
