package extmgr

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
)

// LockFileName は、ロックファイルの名前である（仮。基本設計 5.6）。設定ファイルと同じディレクトリに置く（FR-103）。
const LockFileName = "extensions.lock.json"

// lockVersion は、ロックファイルの形式の版である。形式を変えたら上げる。
const lockVersion = 1

// Lock は、ロックファイルの内容である（仮。基本設計 5.6、FR-103）。
//
// 形は次のとおりで、キーの順は encoding/json が名前の順に並べる。
// 利用者が設定ファイルと一緒に dotfiles のリポジトリで管理するので、差分が小さくなるように 2 文字の字下げで書く。
//
//	{
//	  "version": 1,
//	  "extensions": {
//	    "surround": {
//	      "repo": "https://github.com/example/vi-imitation-surround",
//	      "version": "v0.1.0",
//	      "commit": "0123456789abcdef0123456789abcdef01234567"
//	    }
//	  }
//	}
type Lock struct {
	// Version は、形式の版である。いまは 1 だけを読む。
	Version int `json:"version"`
	// Extensions は、Sync が入れた拡張機能の名前から、取得したときの取得元と版とコミットへの対応である。
	Extensions map[string]LockEntry `json:"extensions"`
}

// LockEntry は、ロックファイルの 1 つの拡張機能の記録である。
type LockEntry struct {
	// Repo は、取得したときの設定の repo である。
	Repo string `json:"repo"`
	// Version は、取得したときの設定の version である。設定の version と比べて、版を変えたかを決める。
	Version string `json:"version"`
	// Commit は、Version を解決して取り出したコミットの 40 桁（SHA-256 のリポジトリなら 64 桁）のハッシュである。
	Commit string `json:"commit"`
}

// ReadLock は、path のロックファイルを読む。
// ファイルが無ければ、空の Lock と nil を返す。
// 読めないとき、形が違うとき、形式の版が違うときは誤りを返す。Sync はこのときロックファイルを書き換えずに止める。
func ReadLock(path string) (Lock, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Lock{Version: lockVersion, Extensions: map[string]LockEntry{}}, nil
	}
	if err != nil {
		return Lock{}, fmt.Errorf("ロックファイルを読めない: %w", err)
	}
	var l Lock
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&l); err != nil {
		return Lock{}, fmt.Errorf("ロックファイル %s を読めない: %w", path, err)
	}
	if l.Version != lockVersion {
		return Lock{}, fmt.Errorf("ロックファイル %s の形式の版 %d を読めない（読めるのは %d）", path, l.Version, lockVersion)
	}
	if l.Extensions == nil {
		l.Extensions = map[string]LockEntry{}
	}
	for name, e := range l.Extensions {
		if !fullHash.MatchString(e.Commit) {
			return Lock{}, fmt.Errorf("ロックファイル %s の %s の commit %q は 40 桁か 64 桁の小文字の 16 進数にする", path, name, e.Commit)
		}
	}
	return l, nil
}

// fullHash は、ロックファイルに書くコミットのハッシュ（SHA-1 の 40 桁か SHA-256 の 64 桁）である。
// 短いハッシュは、取得元のコミットが増えると曖昧になりうるので受け入れない。
var fullHash = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// encode は、l をロックファイルの中身にする。
func (l *Lock) encode() ([]byte, error) {
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// writeLock は、l を path に書く。
// 中身が今のファイルと同じなら書かない。ファイルが無く、記録も無ければ作らない。
// 途中で失敗しても前の中身が残るように、同じディレクトリの一時ファイルに書いてから名前を変える。
// path が symlink なら（dotfiles のリポジトリへ GNU stow などでファイルごとに張ったもの）、symlink は残し、
// リンク先のファイルのディレクトリに一時ファイルを書いて、リンク先の名前へ変える。
// リンク先が無い（切れた symlink。dotfiles のリポジトリでまだ作っていないか、消したとき）ときも、リンクの指す先に作る。
// ReadLock は切れた symlink を「ファイルが無い」として読むので、それと揃える（#213）。
func writeLock(path string, l *Lock) error {
	path, err := lockTarget(path)
	if err != nil {
		return fmt.Errorf("ロックファイルの symlink の先を求められない: %w", err)
	}
	data, err := l.encode()
	if err != nil {
		return err
	}
	old, err := os.ReadFile(path)
	switch {
	case err == nil && bytes.Equal(old, data):
		return nil
	case errors.Is(err, fs.ErrNotExist) && len(l.Extensions) == 0:
		return nil
	}
	f, err := os.CreateTemp(filepath.Dir(path), "."+LockFileName+".*")
	if err != nil {
		return fmt.Errorf("ロックファイルを書けない: %w", err)
	}
	tmp := f.Name()
	_, werr := f.Write(data)
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("ロックファイルを書けない: %w", err)
	}
	// CreateTemp は 0600 で作る。dotfiles のリポジトリで管理するので、設定ファイルと同じく他の利用者も読める許可属性にする。
	if err := os.Chmod(tmp, 0o644); err != nil { //nolint:gosec // G302: 秘密を含まない設定のファイルなので、設定ファイルと同じ許可属性にする
		_ = os.Remove(tmp)
		return fmt.Errorf("ロックファイルを書けない: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("ロックファイルを書けない: %w", err)
	}
	return nil
}

// maxLockLinks は、lockTarget が辿る symlink の数の上限である（Linux の 1 つのパスの解決の上限 40 と同じ）。
const maxLockLinks = 40

// lockTarget は、ロックファイルのパス path が symlink なら、書き込む先のパスを返す。symlink でなければ path を返す。
// リンク先があれば filepath.EvalSymlinks の結果を返す。
// リンク先が無ければ、symlink を 1 つずつ os.Readlink で辿り、最後のリンクの指す先（まだ無いファイル）を返す。
// 相対パスのリンクは、リンクのあるディレクトリから解決する。
func lockTarget(path string) (string, error) {
	fi, err := os.Lstat(path)
	if err != nil || fi.Mode()&fs.ModeSymlink == 0 {
		return path, nil //nolint:nilerr // 無いファイルと読めないファイルは、この後の読み書きで扱う
	}
	if target, err := filepath.EvalSymlinks(path); err == nil {
		return target, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	for range maxLockLinks {
		link, err := os.Readlink(path)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(link) {
			// リンクのあるディレクトリ自身が symlink を含んでも、そこから解決するように、ディレクトリを先に実体のパスにする。
			dir, derr := filepath.EvalSymlinks(filepath.Dir(path))
			if derr != nil {
				return "", derr
			}
			link = filepath.Join(dir, link)
		}
		path = link
		fi, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			return path, nil
		}
		if err != nil {
			return "", err
		}
		if fi.Mode()&fs.ModeSymlink == 0 {
			return path, nil
		}
	}
	return "", fmt.Errorf("%s: symlink が %d 段を超える", path, maxLockLinks)
}
