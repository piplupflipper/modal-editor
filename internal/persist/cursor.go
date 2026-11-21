package persist

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

// StateFormat は、状態のファイル（StateFileName）の形式の版である。ファイルの "version" に書く。
// 項目を足すだけなら版を変えない（知らない項目は読み飛ばし、書き換えるときに残す）。既存の項目の意味を変えたら 1 増やす。
const StateFormat = 1

// MaxCursors は、状態のファイルに残すカーソルの位置の件数の上限である。
// Vim の 'viminfo' の既定（'100）と同じく 100 件とし、超えたら古いもの（最後に閉じたのが前のもの）から捨てる（FR-166）。
const MaxCursors = 100

// ErrStateFormat は、状態のファイルの版が、この本体の知る版より新しいことを表す。
// 新しい本体が書いたファイルを古い本体が書き換えて、新しい項目の意味を壊さないように、読みも書きもしない。
var ErrStateFormat = errors.New("persist: state file was written by a newer version")

// lockTimeout は、状態のファイルのロックを待つ時間の上限である。
// ロックは読み直して書き換える間（ミリ秒の単位）しか持たないので、超えたら持ち主が止まっているとみなし、書かずに誤りにする。
const lockTimeout = 2 * time.Second

// Position は、ファイルの中のカーソルの位置である。行と、行の先頭からのバイト数で、どちらも 0 から数える（基本設計 4.1）。
type Position struct {
	Line int
	Byte int
}

// stateFile は、状態のファイルの中身である。
//
//	{
//	  "version": 1,
//	  "files": [
//	    {"path": "/home/u/a.txt", "line": 12, "col": 3, "time": 1763700000}
//	  ]
//	}
//
// files は、最後に閉じたのが新しいものから順に並ぶ。line は 1 から、col は行の先頭からのバイト数で 0 から数える
// （Vim の viminfo の印と同じ数え方）。time は閉じた時刻の Unix 秒である。
// 知らない最上位の項目と、files の各項目の知らない項目は、読み飛ばし、書き換えるときに残す（後でレジスターやジャンプの一覧、
// ファイルごとのほかの印を足せるようにする）。
type stateFile struct {
	// top は、最上位の項目である。version と files もここに入れたまま持ち、書くときに置き換える。
	top map[string]json.RawMessage
	// files は、files の各項目である。
	files []map[string]json.RawMessage
}

// cursorEntry は、files の 1 項目のうち、このパッケージが読み書きする項目である。
type cursorEntry struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Col  int    `json:"col"`
	Time int64  `json:"time"`
}

// LoadCursor は、状態のファイルから、ファイル file（絶対パス）を最後に閉じたときのカーソルの位置を読む（FR-166）。
// 記録が無ければ ok は false である。状態のファイルが無い、読めない、壊れている、新しい版のものであるときも、記録が無いとして扱い、
// 無い以外の誤りを err で返す（呼び出し側は、開くことを止めずに、位置を戻さないだけにする）。
//
// パスは文字列の一致で探す（シンボリックリンクは解決しない。開くときに使ったパスで記録する）。
//
// req: FR-166
func LoadCursor(dir, file string) (pos Position, ok bool, err error) {
	st, err := readState(filepath.Join(dir, StateFileName))
	if err != nil {
		return Position{}, false, err
	}
	for _, f := range st.files {
		var e cursorEntry
		if decodeEntry(f, &e) != nil || e.Path != file {
			continue
		}
		if e.Line < 1 || e.Col < 0 {
			return Position{}, false, nil
		}
		return Position{Line: e.Line - 1, Byte: e.Col}, true, nil
	}
	return Position{}, false, nil
}

// SaveCursor は、ファイル file（絶対パス）を閉じたときのカーソルの位置 pos を、状態のファイルへ残す（FR-166）。
// now は閉じた時刻である。
//
// 同じ状態のファイルを、複数の server（tmux の window ごとに 1 つ。FR-59）が書く。
// このため、ロックのファイル（state.json.lock）に排他のロックを掛けてから、状態のファイルを読み直し、
// その中身に file の記録を合わせて（同じパスの記録を除き、先頭に足し、MaxCursors 件を超えた古い記録を捨てて）、
// 一時ファイルと rename で書き換える。ほかの server が書いた記録は消さない。
//
// 状態のファイルが壊れているときは、捨てて書き直す。新しい版のものなら、書かずに ErrStateFormat を返す。
// 正しくない UTF-8 を含むパスは、JSON の文字列で表せないので残さない（誤りにもしない）。
//
// req: FR-166
func SaveCursor(dir, file string, pos Position, now time.Time) error {
	if !utf8.ValidString(file) {
		return nil
	}
	if err := ensureDir(dir); err != nil {
		return err
	}
	unlock, err := lockState(filepath.Join(dir, StateFileName+lockSuffix))
	if err != nil {
		return err
	}
	defer unlock()
	path := filepath.Join(dir, StateFileName)
	st, err := readState(path)
	switch {
	case errors.Is(err, ErrStateFormat):
		return err
	case err != nil:
		// 無い、または壊れている。空の中身から書き直す。
		st = stateFile{top: map[string]json.RawMessage{}}
	}
	entry := map[string]json.RawMessage{}
	kept := make([]map[string]json.RawMessage, 0, len(st.files)+1)
	for _, f := range st.files {
		var e cursorEntry
		if decodeEntry(f, &e) == nil && e.Path == file {
			// 同じパスの記録の、知らない項目を引き継ぐ。
			entry = f
			continue
		}
		kept = append(kept, f)
	}
	for k, v := range map[string]any{"path": file, "line": max(pos.Line, 0) + 1, "col": max(pos.Byte, 0), "time": now.Unix()} {
		raw, merr := json.Marshal(v)
		if merr != nil {
			return fmt.Errorf("persist: %w", merr)
		}
		entry[k] = raw
	}
	st.files = append([]map[string]json.RawMessage{entry}, kept...)
	if len(st.files) > MaxCursors {
		st.files = st.files[:MaxCursors]
	}
	data, err := st.encode()
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data)
}

// readState は、状態のファイルを読む。無ければ os.ErrNotExist を包んだ誤りを、JSON として読めないか形が違えば誤りを、
// 版が StateFormat より新しければ ErrStateFormat を返す。
func readState(path string) (stateFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return stateFile{}, fmt.Errorf("persist: %w", err)
	}
	var st stateFile
	if err := json.Unmarshal(data, &st.top); err != nil {
		return stateFile{}, fmt.Errorf("persist: %s: %w", path, err)
	}
	if st.top == nil {
		return stateFile{}, fmt.Errorf("persist: %s: not an object", path)
	}
	var version int
	if err := json.Unmarshal(st.top["version"], &version); err != nil {
		return stateFile{}, fmt.Errorf("persist: %s: version: %w", path, err)
	}
	if version > StateFormat {
		return stateFile{}, ErrStateFormat
	}
	if version < 1 {
		return stateFile{}, fmt.Errorf("persist: %s: bad version %d", path, version)
	}
	if raw, ok := st.top["files"]; ok {
		if err := json.Unmarshal(raw, &st.files); err != nil {
			return stateFile{}, fmt.Errorf("persist: %s: files: %w", path, err)
		}
	}
	return st, nil
}

// encode は、状態のファイルの中身を JSON にする。version と files を今の値で置き換え、ほかの最上位の項目は残す。
func (st stateFile) encode() ([]byte, error) {
	top := make(map[string]json.RawMessage, len(st.top)+2)
	for k, v := range st.top {
		top[k] = v
	}
	var err error
	if top["version"], err = json.Marshal(StateFormat); err != nil {
		return nil, fmt.Errorf("persist: %w", err)
	}
	if top["files"], err = json.Marshal(st.files); err != nil {
		return nil, fmt.Errorf("persist: %w", err)
	}
	data, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("persist: %w", err)
	}
	return append(data, '\n'), nil
}

// decodeEntry は、files の 1 項目から、このパッケージの読む項目を取り出す。
func decodeEntry(f map[string]json.RawMessage, e *cursorEntry) error {
	for k, dst := range map[string]any{"path": &e.Path, "line": &e.Line, "col": &e.Col, "time": &e.Time} {
		raw, ok := f[k]
		if !ok {
			if k == "path" {
				return errors.New("no path")
			}
			continue
		}
		if err := json.Unmarshal(raw, dst); err != nil {
			return err
		}
	}
	return nil
}

// lockState は、ロックのファイル path を、無ければ 0600 で作り、排他の flock を掛ける。掛けたら外す関数を返す。
// 他のプロセスが持っていれば、lockTimeout の間だけ待つ。
func lockState(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|unix.O_NOFOLLOW|unix.O_CLOEXEC, filePerm)
	if err != nil {
		return nil, fmt.Errorf("persist: lock: %w", err)
	}
	deadline := time.Now().Add(lockTimeout)
	for {
		err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return func() { _ = f.Close() }, nil // 閉じるとロックも外れる。閉じる誤りは扱えない
		}
		if !errors.Is(err, unix.EWOULDBLOCK) || time.Now().After(deadline) {
			_ = f.Close() // ロックを掛けられなかったファイルを閉じる。誤りは扱えない
			return nil, fmt.Errorf("persist: lock: %w", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
