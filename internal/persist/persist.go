// Package persist は、エディタを終えた後も残す状態のファイル（アンドゥの履歴とカーソルの位置。FR-165、FR-166）を読み書きする
// （基本設計 4.10）。
//
// 置き場のディレクトリ（$XDG_STATE_HOME/vi-imitation。internal/config の StateDir）は呼び出し側が渡す。
// 置き場を決める規則を internal/config に置くのは、設定ファイルと拡張機能の置き場（$XDG_CONFIG_HOME、$XDG_DATA_HOME）と同じ規則で決めるためである。
// このパッケージは、基本設計 2 章の機能の層（internal/config と同じ層）に置き、このリポジトリの他のパッケージを import しない。
// アンドゥの履歴の中身はバイト列のまま扱い、作るのと読むのは internal/buffer の Buffer.MarshalHistory と Buffer.RestoreHistory、
// 両者をつなぐのは internal/editor である。
//
// # 置き場の中身
//
//	<dir>/              0700 で作る
//	  undo/             0700。ファイルごとのアンドゥの履歴（UndoPath）
//	    %home%u%a.txt   0600
//	  state.json        0600。ファイルごとのカーソルの位置（Cursors）
//	  state.json.lock   0600。state.json を書き換える間に掛ける排他のロック
//
// ディレクトリとファイルは、残すときに初めて作る。設定で残さないとき（呼び出し側が呼ばないとき）は何も作らない。
// ファイルは一時ファイルに書いてから rename で置き換えるので、書く途中で止まっても前の中身が残る。
//
// 置き場のファイルは、編集したファイルの中身の一部（アンドゥの履歴）とパスを持つので、ほかの利用者から読めない許可属性で作る。
package persist

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// 置き場の中の名前。
const (
	// UndoDirName は、アンドゥの履歴を置くディレクトリの名前である。
	UndoDirName = "undo"
	// StateFileName は、カーソルの位置を置くファイルの名前である。
	// この先、ほかの状態（レジスター、ジャンプの一覧など。Neovim の shada に当たるもの）を足すときも、このファイルの項目に足す。
	StateFileName = "state.json"
	// lockSuffix は、StateFileName の書き換えに掛けるロックのファイルの名前の後ろに付ける文字列である。
	lockSuffix = ".lock"
)

// 許可属性。
const (
	dirPerm  = 0o700
	filePerm = 0o600
)

// ensureDir は、dir を、無ければ dirPerm で作る（途中のディレクトリも同じ許可属性で作る）。
// 既にあれば許可属性を変えない（利用者が決めた属性を尊重する）。
func ensureDir(dir string) error {
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("persist: %w", err)
	}
	return nil
}

// writeFileAtomic は、data を path へ、同じディレクトリの一時ファイルに書いてから rename で置き換えて書く。
// 一時ファイルは os.CreateTemp で作るので許可属性は filePerm（0600）である。
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := ensureDir(dir); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return fmt.Errorf("persist: %w", err)
	}
	name := tmp.Name()
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Chmod(filePerm)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	if err != nil {
		_ = os.Remove(name) // 一時ファイルの後始末に失敗しても、元の誤りを返すほうが大事である
		return fmt.Errorf("persist: %w", err)
	}
	return nil
}

// removeIfExists は、path を消す。無ければ何もしない。
func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("persist: %w", err)
	}
	return nil
}
