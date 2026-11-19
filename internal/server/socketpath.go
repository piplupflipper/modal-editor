package server

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// unitNameLen は、server のディレクトリの名前に使う、単位の文字列のハッシュの 16 進の桁数である（仮）。
// 64 ビット分あれば、1 人の利用者の同時に動く server の数では重ならない。
const unitNameLen = 16

// UnitDirName は、server の単位（mux.Mux.Unit の文字列。tmux のサーバーのソケットのパスと window の ID、
// または herdr のソケットのパスとタブの ID）から、server のディレクトリ（基本設計 1.2 の `<名前>/`）の名前を作る（FR-59）。
//
// 名前は単位の文字列の SHA-256 の先頭 16 桁で、同じ単位からは同じ名前ができる。
// 文字列をそのまま使わないのは、ソケットのパスの長さの上限（103 バイト）に収めるためと、
// 単位の文字列にパスの区切り（/）が入るためである。
// 1 つのプロセスの構成のディレクトリ（`<プロセス ID>-<乱数>`）と区別できるよう、先頭に u- を付ける。
func UnitDirName(unit string) string {
	sum := sha256.Sum256([]byte(unit))
	return "u-" + hex.EncodeToString(sum[:])[:unitNameLen]
}

// SocketPathForUnit は、server の単位 unit の server のソケットのパスを決め、そのディレクトリを PrepareDir で用意する（基本設計 1.2。FR-59）。
//
// ディレクトリは次の順に選び、使えた最初のものを返す。
//
//  1. runtimeDir（環境変数 XDG_RUNTIME_DIR）が既にある絶対パスのディレクトリなら、`<runtimeDir>/vi-imitation/<名前>/`
//  2. `<tempDir>/vi-imitation-<実効ユーザー ID>/<名前>/`
//
// 「使えない」のは、ソケットのパスが長さの上限を超える、ディレクトリを作れない、ディレクトリかその親が
// 他の利用者のものか他の利用者の権限がある（PrepareDir）場合である。
// 一時ディレクトリで他の利用者が先に同じ名前を作っていた場合も 2 が使えず、どちらも使えなければ両方の誤りを返す。
// 呼び出し側は、そのことを知らせて 1 つのプロセスで動かす（FR-60 の構成。基本設計 1.2）。
//
// runtimeDir の下の vi-imitation/ は、MCP のソケットのディレクトリ（internal/exthost の NewMCPSocket）と同じ親である。
func SocketPathForUnit(unit, runtimeDir, tempDir string) (string, error) {
	name := UnitDirName(unit)
	var bases []string
	if filepath.IsAbs(runtimeDir) {
		if fi, err := os.Stat(runtimeDir); err == nil && fi.IsDir() {
			bases = append(bases, filepath.Join(runtimeDir, "vi-imitation"))
		}
	}
	bases = append(bases, filepath.Join(tempDir, "vi-imitation-"+strconv.Itoa(os.Geteuid())))
	var errs []error
	for _, base := range bases {
		dir := filepath.Join(base, name)
		path := filepath.Join(dir, SocketName)
		if len(path) > maxSocketPath {
			errs = append(errs, fmt.Errorf("server: socket path %s is too long (%d bytes, max %d)", path, len(path), maxSocketPath))
			continue
		}
		if err := PrepareDir(dir); err != nil {
			errs = append(errs, err)
			continue
		}
		return path, nil
	}
	return "", errors.Join(errs...)
}
