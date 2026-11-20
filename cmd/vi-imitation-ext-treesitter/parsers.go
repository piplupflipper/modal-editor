package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/piplupflipper/vi-imitation/internal/config"
	"github.com/piplupflipper/vi-imitation/internal/extmgr"
)

// 利用者の設定に並べたパーサ（FR-142）を、取得し、手元でビルドし、読み込む。
//
// 置き場所は次のとおりである（仮）。
//
//   - $XDG_DATA_HOME/vi-imitation/treesitter/src/<名前>/: git で取得したソース（extmgr.Checkout）
//   - $XDG_DATA_HOME/vi-imitation/treesitter/parsers/<名前>/<鍵>/: ビルドしたもの。parser.so（共有ライブラリ）、
//     highlights.scm（ハイライトのクエリ）、build.json（buildInfo）を置く。鍵は、コミットと path と queries から作る（buildKey）
//   - $XDG_DATA_HOME/vi-imitation/treesitter/lock: 取得とビルドを、別の本体（端末多重化ソフトの window ごとの server）の
//     拡張機能と 1 つずつにする flock のファイル
//   - 設定ファイルと同じディレクトリの treesitter.lock.json: 取得したコミットのロックファイル（parserLock）
//
// 同じパーサを 2 回目からは、ロックファイルの記録が設定の repo と version に合い、その記録のコミットの鍵のディレクトリがあれば、
// git も C コンパイラも起動せずに読み込む。

// ParserLockFileName は、パーサのロックファイルの名前である（仮）。設定ファイルと同じディレクトリに置く。
const ParserLockFileName = "treesitter.lock.json"

// parserLockVersion は、パーサのロックファイルの形式の版である。
const parserLockVersion = 1

// fetchTimeout と buildTimeout は、1 つのパーサの取得（git）とビルド（C コンパイラ）の待ちの上限である（仮）。
const (
	fetchTimeout = 10 * time.Minute
	buildTimeout = 10 * time.Minute
)

// parserEnv は、パーサを用意するときの置き場所と、子プロセスの設定である。
// newHighlighter が defaultParserEnv で入れる。テストは、Server を動かす前に一時ディレクトリへ向け直す。
type parserEnv struct {
	// dataDir は、取得したソースとビルドしたものを置くディレクトリ（$XDG_DATA_HOME/vi-imitation/treesitter）である。
	dataDir string
	// lockPath は、パーサのロックファイルのパスである。
	lockPath string
	// err は、置き場所を決められなかった理由である。nil でなければ、パーサを用意しようとしたときにこの誤りで失敗する。
	err error
	// git は、git の実行ファイルの名前かパスである。空なら "git" である。
	git string
	// env は、git と C コンパイラの子プロセスに、拡張機能の環境変数に足して渡す環境変数である。
	env []string
	// fetchTimeout と buildTimeout は、取得とビルドの待ちの上限である。
	fetchTimeout, buildTimeout time.Duration
}

// defaultParserEnv は、本体と同じ規則（internal/config の DefaultPath と ExtensionDirs）で置き場所を決める。
func defaultParserEnv() parserEnv {
	env := parserEnv{fetchTimeout: fetchTimeout, buildTimeout: buildTimeout}
	cfgPath, err := config.DefaultPath()
	if err != nil {
		env.err = err
		return env
	}
	dirs := config.ExtensionDirs("")
	if len(dirs) == 0 {
		env.err = errors.New("XDG_DATA_HOME と HOME が無いので、パーサを置く場所を決められない")
		return env
	}
	env.lockPath = filepath.Join(filepath.Dir(cfgPath), ParserLockFileName)
	env.dataDir = filepath.Join(filepath.Dir(dirs[0]), "treesitter")
	return env
}

// parserLock は、パーサのロックファイルの内容である（仮）。
// 形は extmgr のロックファイル（extensions.lock.json）と同じ考え方で、名前の順に 2 文字の字下げで書く。
//
//	{
//	  "version": 1,
//	  "parsers": {
//	    "python": {
//	      "repo": "https://github.com/tree-sitter/tree-sitter-python",
//	      "version": "v0.25.0",
//	      "commit": "0123456789abcdef0123456789abcdef01234567"
//	    }
//	  }
//	}
type parserLock struct {
	Version int                         `json:"version"`
	Parsers map[string]extmgr.LockEntry `json:"parsers"`
}

// fullCommit は、ロックファイルに書くコミットのハッシュ（40 桁か 64 桁）である。
var fullCommit = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// readParserLock は、path のロックファイルを読む。無ければ空の記録を返す。
// 読めないとき、形と版が違うとき、コミットの形が違うときは誤りを返す。そのときはロックファイルを書き換えない。
func readParserLock(path string) (parserLock, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return parserLock{Version: parserLockVersion, Parsers: map[string]extmgr.LockEntry{}}, nil
	}
	if err != nil {
		return parserLock{}, fmt.Errorf("ロックファイルを読めない: %w", err)
	}
	var l parserLock
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&l); err != nil {
		return parserLock{}, fmt.Errorf("ロックファイル %s を読めない: %w", path, err)
	}
	if l.Version != parserLockVersion {
		return parserLock{}, fmt.Errorf("ロックファイル %s の形式の版 %d を読めない（読めるのは %d）", path, l.Version, parserLockVersion)
	}
	if l.Parsers == nil {
		l.Parsers = map[string]extmgr.LockEntry{}
	}
	for name, e := range l.Parsers {
		if !fullCommit.MatchString(e.Commit) {
			return parserLock{}, fmt.Errorf("ロックファイル %s の %s の commit %q は 40 桁か 64 桁の小文字の 16 進数にする", path, name, e.Commit)
		}
	}
	return l, nil
}

// writeParserLock は、l を path に書く（extmgr.WriteLockFile。symlink ならリンク先を書き換える）。
func writeParserLock(path string, l parserLock) error {
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	return extmgr.WriteLockFile(path, append(data, '\n'), len(l.Parsers) > 0)
}

// buildInfo は、ビルドしたディレクトリの build.json の内容である。読み込むときに、ソースを見ずに文法の関数の名前を知るのに使う。
type buildInfo struct {
	Repo   string `json:"repo"`
	Commit string `json:"commit"`
	// Symbol は、文法の TSLanguage を返す関数の名前（tree_sitter_<文法の名前>）である。
	Symbol string `json:"symbol"`
}

// 置き場所の中のファイルの名前である。
const (
	libraryFileName    = "parser.so"
	highlightsFileName = "highlights.scm"
	buildInfoFileName  = "build.json"
)

// preparer は、1 つのパーサを用意する（取得、ビルド、読み込み）。
type preparer struct {
	env      parserEnv
	compiler string
	// listed は、設定に並べたパーサの名前の集合である（treesitterSettings.listed）。
	listed map[string]bool
	// notify は、利用者へ進み具合を知らせる。
	notify func(string)
}

// prepare は、パーサ spec を用意し、読み込んだ言語を返す（FR-142）。
//
// ロックファイルの記録が spec の repo と version に合えば、そのコミットを使う。
// そのコミットのビルドしたものがあれば、それを読み込む（git も C コンパイラも起動しない）。
// 無ければ、git で取得してコミットを取り出し（記録が合わなければ version を解決し）、C コンパイラでビルドし、
// ロックファイルに取り出したコミットを記録してから読み込む。
// 取得からロックファイルの記録までは、置き場所の flock を取って行う。
// 失敗したら、理由を返す。呼び出し側は、このパーサだけを使わない。
//
// req: FR-142
func (p *preparer) prepare(ctx context.Context, spec parserSpec) (*language, error) {
	if p.env.err != nil {
		return nil, p.env.err
	}
	unlock, err := lockDataDir(ctx, p.env.dataDir)
	if err != nil {
		return nil, err
	}
	defer unlock()
	lock, err := readParserLock(p.env.lockPath)
	if err != nil {
		return nil, err
	}
	pinned := ""
	if e, ok := lock.Parsers[spec.name]; ok && e.Repo == spec.repo && e.Version == spec.version {
		pinned = e.Commit
	}
	if pinned != "" {
		if dir := p.buildDir(spec, pinned); exists(filepath.Join(dir, libraryFileName)) {
			return loadBuilt(spec.name, dir)
		}
	}

	p.notify(fmt.Sprintf("tree-sitter: パーサ %s を取得している（%s %s）", spec.name, spec.repo, spec.version))
	src := filepath.Join(p.env.dataDir, "src", spec.name)
	fctx, cancel := context.WithTimeout(ctx, p.env.fetchTimeout)
	commit, err := extmgr.Checkout(fctx, src, spec.repo, spec.version, pinned, extmgr.CheckoutOptions{Git: p.env.git, Env: p.env.env})
	cancel()
	if err != nil {
		return nil, fmt.Errorf("取得できない: %w", err)
	}
	dir := p.buildDir(spec, commit)
	if !exists(filepath.Join(dir, libraryFileName)) {
		p.notify(fmt.Sprintf("tree-sitter: パーサ %s をビルドしている（%s）", spec.name, p.compiler))
		if err = p.build(ctx, spec, src, commit, dir); err != nil {
			return nil, err
		}
	}
	lock.Parsers[spec.name] = extmgr.LockEntry{Repo: spec.repo, Version: spec.version, Commit: commit}
	for name := range lock.Parsers {
		if !p.listed[name] {
			delete(lock.Parsers, name)
		}
	}
	if err = writeParserLock(p.env.lockPath, lock); err != nil {
		return nil, err
	}
	lang, err := loadBuilt(spec.name, dir)
	if err != nil {
		return nil, err
	}
	p.notify(fmt.Sprintf("tree-sitter: パーサ %s を用意した（%s）", spec.name, commit[:12]))
	return lang, nil
}

// buildDir は、spec をコミット commit でビルドしたものを置くディレクトリである。
func (p *preparer) buildDir(spec parserSpec, commit string) string {
	return filepath.Join(p.env.dataDir, "parsers", spec.name, buildKey(spec, commit))
}

// buildKey は、ビルドしたものを見分ける鍵である。コミット、path、queries のどれかが変われば、別のディレクトリにビルドする。
// 同じパスの共有ライブラリを、中身を変えて dlopen し直すことはしない（読み込み済みのライブラリが使われうる）。
func buildKey(spec parserSpec, commit string) string {
	h := sha256.New()
	for _, s := range append([]string{commit, spec.path}, spec.queries...) {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// grammarName は、文法の名前（src/grammar.json の name）に使える文字列である。関数の名前 tree_sitter_<名前> になる。
var grammarName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// build は、取得したソース src（コミット commit を取り出したもの）の path の下の src/parser.c と、あれば src/scanner.c を、
// C コンパイラで共有ライブラリにし、ハイライトのクエリと一緒に dir に置く。
// 置き場所の中の一時ディレクトリでビルドし、成功してから dir へ名前を変える。同じパーサの前のビルドは消す。
// tree-sitter の CLI は要らない（文法の生成済みの parser.c をコンパイルする）。C++ の外部スキャナー（scanner.cc）は扱わない。
//
// req: FR-142
func (p *preparer) build(ctx context.Context, spec parserSpec, src, commit, dir string) error {
	root := filepath.Join(src, spec.path)
	srcDir := filepath.Join(root, "src")
	files := []string{filepath.Join(srcDir, "parser.c")}
	switch {
	case !exists(files[0]):
		return fmt.Errorf("%s に src/parser.c が無い", filepath.Join(spec.repo, spec.path))
	case exists(filepath.Join(srcDir, "scanner.c")):
		files = append(files, filepath.Join(srcDir, "scanner.c"))
	case exists(filepath.Join(srcDir, "scanner.cc")):
		return errors.New("src/scanner.cc（C++ の外部スキャナー）は扱わない")
	}
	symbol, err := grammarSymbol(srcDir, spec.name)
	if err != nil {
		return err
	}
	highlights, err := readQueries(src, root, spec.queries)
	if err != nil {
		return err
	}

	parent := filepath.Dir(dir)
	if err = os.MkdirAll(parent, 0o750); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(parent, ".build-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp) //nolint:errcheck // 一時ディレクトリの後始末。残っても次のビルドで使わない

	args := append([]string{"-shared", "-fPIC", "-O2", "-I", srcDir, "-o", filepath.Join(tmp, libraryFileName)}, files...)
	bctx, cancel := context.WithTimeout(ctx, p.env.buildTimeout)
	defer cancel()
	if err = runChild(bctx, root, p.env.env, p.compiler, args...); err != nil {
		if bctx.Err() != nil && ctx.Err() == nil {
			return fmt.Errorf("ビルドが %v で終わらない", p.env.buildTimeout)
		}
		return fmt.Errorf("ビルドできない: %w", err)
	}
	info, err := json.MarshalIndent(buildInfo{Repo: spec.repo, Commit: commit, Symbol: symbol}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, highlightsFileName), highlights, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, buildInfoFileName), append(info, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, dir); err != nil {
		return err
	}
	// 前のビルド（別のコミットなど）を消す。別の本体の拡張機能が読み込んだままでも、Linux では読み込んだものは使い続けられる。
	entries, _ := os.ReadDir(parent)
	for _, e := range entries {
		if old := filepath.Join(parent, e.Name()); old != dir && !strings.HasPrefix(e.Name(), ".build-") {
			_ = os.RemoveAll(old)
		}
	}
	return nil
}

// grammarSymbol は、src/grammar.json の name から、文法の TSLanguage を返す関数の名前を決める。
// grammar.json を読めなければ、パーサの名前の - と . を _ にしたものを文法の名前とする。
func grammarSymbol(srcDir, name string) (string, error) {
	g := strings.NewReplacer("-", "_", ".", "_").Replace(name)
	if data, err := os.ReadFile(filepath.Join(srcDir, "grammar.json")); err == nil {
		var j struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(data, &j); err == nil && j.Name != "" {
			g = j.Name
		}
	}
	if !grammarName.MatchString(g) {
		return "", fmt.Errorf("文法の名前 %q から関数の名前を作れない", g)
	}
	return "tree_sitter_" + g, nil
}

// readQueries は、ハイライトのクエリを読み、順につないで返す。
// queries が空なら、root（path の下）の queries/highlights.scm、無ければ src（リポジトリの最上位）の queries/highlights.scm を読む。
func readQueries(src, root string, queries []string) ([]byte, error) {
	if len(queries) == 0 {
		for _, dir := range []string{root, src} {
			data, err := os.ReadFile(filepath.Join(dir, "queries", "highlights.scm"))
			if err == nil {
				return data, nil
			}
		}
		return nil, errors.New("ハイライトのクエリ queries/highlights.scm が無い")
	}
	var out []byte
	for _, q := range queries {
		data, err := os.ReadFile(filepath.Join(src, q))
		if err != nil {
			return nil, fmt.Errorf("ハイライトのクエリ %s を読めない: %w", q, err)
		}
		out = append(out, data...)
		out = append(out, '\n')
	}
	return out, nil
}

// loadBuilt は、dir にビルドしたパーサを読み込み、言語にする。
func loadBuilt(name, dir string) (*language, error) {
	data, err := os.ReadFile(filepath.Join(dir, buildInfoFileName))
	if err != nil {
		return nil, err
	}
	var info buildInfo
	if err = json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("%s を読めない: %w", buildInfoFileName, err)
	}
	highlights, err := os.ReadFile(filepath.Join(dir, highlightsFileName))
	if err != nil {
		return nil, err
	}
	grammar, err := loadGrammar(filepath.Join(dir, libraryFileName), info.Symbol)
	if err != nil {
		return nil, fmt.Errorf("読み込めない: %w", err)
	}
	return newGrammarLanguage(name, grammar, highlightsFileName, highlights)
}

// exists は、path にファイルかディレクトリがあるかを返す。
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// runChild は、dir で name を子プロセスとして実行する（extmgr.Start。拡張機能が SIGKILL で終わっても子プロセスが残らない）。
// 失敗したときは、標準エラー出力を誤りの文言に含める。
func runChild(ctx context.Context, dir string, env []string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // G204: 起動するのは利用者が設定した C コンパイラで、引数はこの拡張機能が組み立てたもの
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.WaitDelay = 5 * time.Second
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stderr, &stderr
	err := extmgr.Start(cmd)
	if err == nil {
		err = cmd.Wait()
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			msg = ":\n" + msg
		}
		return fmt.Errorf("%s が失敗した（%w）%s", filepath.Base(name), err, msg)
	}
	return nil
}

// lockPollInterval は、別の本体の拡張機能が取った置き場所の flock が外れるのを確かめる間隔である。
const lockPollInterval = 200 * time.Millisecond

// lockDataDir は、dir を作り、dir/lock のファイルに排他の flock を掛ける。
// 別のプロセスが掛けていれば、外れるか ctx が終わるまで待つ。返した関数でロックを外す。
// ロックはプロセスが終われば外れるので、強制終了で残らない。
func lockDataDir(ctx context.Context, dir string) (func(), error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("置き場所のロックを開けない: %w", err)
	}
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			_ = f.Close()
			return nil, fmt.Errorf("置き場所をロックできない: %w", err)
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, ctx.Err()
		case <-time.After(lockPollInterval):
		}
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
