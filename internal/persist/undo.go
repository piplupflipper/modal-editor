package persist

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Sum は、ファイルの中身の SHA-256 のハッシュである。
// アンドゥの履歴のファイルに、履歴を書いた時点のファイルの中身のハッシュとして入れ、読むときに今の中身のハッシュと比べる
// （Vim の undofile が中身のハッシュを入れて比べるのと同じ。FR-165）。
type Sum = [sha256.Size]byte

// undoMagic は、アンドゥの履歴のファイルの先頭に置く印である。
const undoMagic = "vi-imitation undo\n"

// UndoFormat は、アンドゥの履歴のファイルの形式の版である。印の直後に 4 バイトのビッグエンディアンで書く。
// ファイルの形（印、版、ハッシュ、履歴のバイト列の並び）を変えたら 1 増やす。
// 違う版のファイルは ReadUndo が ErrUndoFormat で断る（本体の版が変わっても、読めない形式を黙って誤読しない）。
// 履歴のバイト列の中身の形式は、internal/buffer が自分の版を持って確かめる。
const UndoFormat = 1

// ErrUndoFormat は、アンドゥの履歴のファイルが、この本体の書く形式でない（印が無い、版が違う、途中で切れている）ことを表す。
var ErrUndoFormat = errors.New("persist: unknown undo file format")

// ErrUndoStale は、アンドゥの履歴のファイルを書いた後に、ファイルの中身が外で変わった（ハッシュが一致しない）ことを表す。
var ErrUndoStale = errors.New("persist: file changed after the undo history was written")

// UndoPath は、ファイル file（絶対パス）のアンドゥの履歴を置くパスを返す。
//
// 名前は、file のパスの / を % に替えたものである（Vim と Neovim の 'undodir' に置くときの名前と同じ。例: /home/u/a.txt は %home%u%a.txt）。
// file がシンボリックリンクなら、リンクの指す先のパスから名前を決める（Vim 9.1 と Neovim 0.12.5 と同じ。2026-11-21 に確かめた）。
// 同じファイルをリンクと元のパスのどちらで開いても、同じ履歴を使う。
// 指す先が無いなど、リンクを解決できないときは file のまま決める。
func UndoPath(dir, file string) string {
	if target, err := filepath.EvalSymlinks(file); err == nil {
		file = target
	}
	return filepath.Join(dir, UndoDirName, strings.ReplaceAll(file, string(filepath.Separator), "%"))
}

// WriteUndo は、ファイル file のアンドゥの履歴 history を、中身のハッシュ sum と一緒に UndoPath のパスへ書く（FR-165）。
// sum は、履歴を書く時点のファイルの中身（書き出した中身）のハッシュである。
// 置き場のディレクトリが無ければ 0700 で作り、ファイルは 0600 で、一時ファイルと rename で置き換える。
//
// req: FR-165
func WriteUndo(dir, file string, sum Sum, history []byte) error {
	data := make([]byte, 0, len(undoMagic)+4+len(sum)+len(history))
	data = append(data, undoMagic...)
	data = binary.BigEndian.AppendUint32(data, UndoFormat)
	data = append(data, sum[:]...)
	data = append(data, history...)
	return writeFileAtomic(UndoPath(dir, file), data)
}

// ReadUndo は、ファイル file のアンドゥの履歴を UndoPath のパスから読み、履歴のバイト列を返す（FR-165）。
// sum は、今のファイルの中身（開くときに読んだ中身）のハッシュである。
//
// 履歴のファイルが無ければ os.ErrNotExist を包んだ誤りを、形式が違えば ErrUndoFormat を、
// 書いた時点のハッシュが sum と違えば ErrUndoStale を返す。どの場合も、呼び出し側は履歴を読まずに捨てる。
// 断った履歴のファイルは消さない（Vim と同じく、次に履歴を書くときに置き換えるか、消す。RemoveUndo）。
//
// req: FR-165
func ReadUndo(dir, file string, sum Sum) ([]byte, error) {
	data, err := os.ReadFile(UndoPath(dir, file))
	if err != nil {
		return nil, fmt.Errorf("persist: %w", err)
	}
	rest, ok := bytes.CutPrefix(data, []byte(undoMagic))
	if !ok || len(rest) < 4+len(sum) {
		return nil, ErrUndoFormat
	}
	if v := binary.BigEndian.Uint32(rest); v != UndoFormat {
		return nil, fmt.Errorf("%w: version %d", ErrUndoFormat, v)
	}
	rest = rest[4:]
	if !bytes.Equal(rest[:len(sum)], sum[:]) {
		return nil, ErrUndoStale
	}
	return rest[len(sum):], nil
}

// RemoveUndo は、ファイル file のアンドゥの履歴のファイルを消す。無ければ何もしない。
// 履歴の無いバッファを保存したときに呼ぶ（Vim 9.1 と Neovim 0.12.5 も、アンドゥの履歴の無いバッファを書くと、
// 古い履歴のファイルを消す。2026-11-21 に確かめた）。古い履歴が、外で変わる前の中身に戻ったファイルへ後から当てはまらないようにする。
//
// req: FR-165
func RemoveUndo(dir, file string) error {
	return removeIfExists(UndoPath(dir, file))
}
