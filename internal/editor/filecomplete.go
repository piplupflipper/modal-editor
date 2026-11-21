package editor

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/piplupflipper/vi-imitation/internal/command/ex"
)

// ファイルとディレクトリの補完の候補（FR-199、FR-200。基本設計 7.4 の「ファイルの候補」）。

// fileQuery は、ファイルとディレクトリの補完の候補を集める問いである（collectFiles）。
type fileQuery struct {
	// word は、コマンドラインの語（打ったままの、"\" を付けた形）である。
	word string
	// workDir は、相対パスの基点（client の作業ディレクトリ）である。
	workDir string
	// env は、語の "~" と環境変数を展開する口である。イベントループの外で読むので、集め始めたときの写し（fileEnvSnapshot）を渡す。
	env ex.FileEnv
	// ignoreCase は、名前の大文字と小文字を区別しないかである（commandLine.completeIgnoreCase）。
	ignoreCase bool
}

// editorFileEnv は、Ex コマンドのファイルの引数を展開する口（ex.State.FileEnv）である。読むたびに、読み込んでいる client の
// 外部のコマンドに足す環境変数（childEnv）を先に、server のプロセスの環境変数を後に引く（FR-200）。
type editorFileEnv struct{ e *Editor }

func (f editorFileEnv) Home() string {
	home, _ := os.UserHomeDir()
	return home
}

func (f editorFileEnv) LookupEnv(name string) (string, bool) { return envLookup(f.e.childEnv())(name) }

// fileEnvSnapshot は、読み込んでいる client の、ファイルの引数を展開する口の写しを返す（イベントループの外で読むため）。
func (e *Editor) fileEnvSnapshot() ex.FileEnv {
	home, _ := os.UserHomeDir()
	return fileEnvCopy{home: home, env: slices.Clone(e.childEnv())}
}

// fileEnvCopy は、ホームディレクトリと環境変数の写しである（fileEnvSnapshot）。
type fileEnvCopy struct {
	home string
	env  []string
}

func (f fileEnvCopy) Home() string { return f.home }

func (f fileEnvCopy) LookupEnv(name string) (string, bool) { return envLookup(f.env)(name) }

// envLookup は、環境変数 env（"名前=値" の列。後のものが勝つ）を先に、server のプロセスの環境変数を後に引く関数を返す。
func envLookup(env []string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		for i := len(env) - 1; i >= 0; i-- {
			if k, v, ok := strings.Cut(env[i], "="); ok && k == name {
				return v, true
			}
		}
		return os.LookupEnv(name)
	}
}

// fileSuffixes は、補完の候補の後ろへ回すファイルの名前の末尾である（Vim 9.1 と Neovim 0.12.5 の 'suffixes' の既定）。
var fileSuffixes = []string{".bak", "~", ".o", ".h", ".info", ".swp", ".obj"}

// fileReadChunk は、ディレクトリを一度に読む項目の数である。読むたびに取り消されたかを確かめる。
const fileReadChunk = 1024

// collectFiles は、ファイルとディレクトリの補完の候補を集める（FR-199）。items はコマンドラインに入れる形、shown は一覧に出す形
// （名前だけ）である。どちらも ex.EscapeFileArg と ex.EscapeFileName で "\" を付け、ディレクトリの名前には "/" を付ける。
// items を :e と :w が読むと（ex.ExpandFileArg）、候補の名前そのもののパスに戻る（FR-200）。
// 読み方は Neovim 0.12.5 の :e の補完に合わせた（2026-11-22 に、名前に空白、"$"、"|" を含むファイル、隠しファイル、'suffixes' に当たる
// ファイル、シンボリックリンクのあるディレクトリで確かめた）。
//
//   - 語の最後の "/" までをディレクトリとして、ex.ExpandFileArg で開くパスにする（"~" と環境変数を展開し、"\" を付けた文字を戻す）。
//     無ければ workDir、相対パスなら workDir の下である。".." は字面で畳まずに OS に解かせる（シンボリックリンクの先の親を読む。
//     無いディレクトリを通る "nothere/.." は読めない。どちらも Neovim と同じ）
//   - 候補の前には、打ったディレクトリの部分をそのまま付ける（"~" を残す）。打った部分に環境変数があれば、展開したパスにする
//     （Neovim の :e $HOME/<Tab> が絶対パスを入れるのと同じ）。"~" だけの語は、ホームディレクトリの絶対パスにする
//   - 最後の "/" の後で始まる名前を集める。ignoreCase なら大文字と小文字を区別しない
//   - "." で始まる名前は、"/" の後が "." で始まるときだけ集め、そのときは "." と ".." も集める（Neovim の :e .<Tab> と同じ）
//   - UTF-8 として正しくない名前は集めない（コマンドラインは文字の列なので、入れると別の名前になる）
//   - 名前のバイトの順に並べ、末尾が fileSuffixes に当たるファイルを後ろへ回す
//
// 読む途中で ctx が終わったら、ctx.Err() を返す。
//
// req: FR-199
func collectFiles(ctx context.Context, q fileQuery) (items, shown []string, err error) {
	raw := q.word
	if home := q.env.Home(); raw == "~" && home != "" {
		h := ex.EscapeFileArg(strings.TrimSuffix(home, "/") + "/")
		return []string{h}, []string{h}, nil
	}
	slash := strings.LastIndexByte(raw, '/')
	rawDir, rawBase := raw[:slash+1], raw[slash+1:]
	dir := ex.ExpandFileArg(rawDir, q.env)
	base := ex.ExpandFileName(rawBase, q.env)
	if rawDir == "" {
		base = ex.ExpandFileArg(rawBase, q.env)
	}
	prefix := rawDir
	if ex.HasEnvRef(rawDir) {
		prefix = ex.EscapeFileArg(dir)
	}
	readDir := dir
	switch {
	case dir == "":
		readDir = q.workDir
	case !filepath.IsAbs(dir):
		// filepath.Join は ".." を字面で畳むので使わない。
		readDir = strings.TrimSuffix(q.workDir, string(filepath.Separator)) + string(filepath.Separator) + dir
	}
	f, err := os.Open(readDir)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close() //nolint:errcheck // 読むだけのディレクトリなので、閉じる誤りで候補は変わらない
	hidden := strings.HasPrefix(base, ".")
	matches := func(name string) bool {
		if len(name) < len(base) {
			return false
		}
		if q.ignoreCase {
			return strings.EqualFold(name[:len(base)], base)
		}
		return strings.HasPrefix(name, base)
	}
	type entry struct {
		name string
		dir  bool
	}
	var found []entry
	if hidden {
		for _, n := range []string{".", ".."} {
			if matches(n) {
				found = append(found, entry{name: n, dir: true})
			}
		}
	}
	join := strings.TrimSuffix(readDir, string(filepath.Separator)) + string(filepath.Separator)
	for {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		des, rerr := f.ReadDir(fileReadChunk)
		for _, de := range des {
			name := de.Name()
			if (!hidden && strings.HasPrefix(name, ".")) || !utf8.ValidString(name) || !matches(name) {
				continue
			}
			isDir := de.IsDir()
			if de.Type()&os.ModeSymlink != 0 {
				if st, err := os.Stat(join + name); err == nil {
					isDir = st.IsDir()
				}
			}
			found = append(found, entry{name: name, dir: isDir})
		}
		if errors.Is(rerr, io.EOF) || (rerr == nil && len(des) == 0) {
			break
		}
		if rerr != nil {
			return nil, nil, rerr
		}
	}
	suffixed := func(en entry) bool {
		return !en.dir && slices.ContainsFunc(fileSuffixes, func(s string) bool { return strings.HasSuffix(en.name, s) })
	}
	slices.SortStableFunc(found, func(a, b entry) int {
		if sa, sb := suffixed(a), suffixed(b); sa != sb {
			if sa {
				return 1
			}
			return -1
		}
		return strings.Compare(a.name, b.name)
	})
	items, shown = make([]string, len(found)), make([]string, len(found))
	for i, en := range found {
		name := en.name
		if en.dir {
			name += "/"
		}
		esc := ex.EscapeFileName(name)
		if prefix == "" {
			esc = ex.EscapeFileArg(name)
		}
		items[i] = prefix + esc
		shown[i] = esc
	}
	return items, shown, nil
}
