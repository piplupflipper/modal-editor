package persist

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"
)

// CursorsItem は、state.json のうち、ファイルごとのカーソルの位置を持つ項目の名前である（FR-166）。
const CursorsItem = "cursors"

// CursorsFormat は、CursorsItem の項目の形式の版である。項目の中身の意味を変えたら 1 増やす
// （パッケージの doc の「state.json の項目と版」）。
const CursorsFormat = 1

// MaxCursors は、残すカーソルの位置の件数の上限である。
// Vim の 'viminfo' の既定（'100）と同じく 100 件とし、超えたら古いもの（最後に閉じたのが前のもの）から捨てる（FR-166）。
const MaxCursors = 100

// Position は、ファイルの中のカーソルの位置である。行と、行の先頭からのバイト数で、どちらも 0 から数える（基本設計 4.1）。
type Position struct {
	Line int
	Byte int
}

// Cursor は、1 つのファイル（絶対パス）を閉じたときのカーソルの位置である。
type Cursor struct {
	Path string
	Pos  Position
}

// CursorsItem の項目の中身は、最後に閉じたのが新しいものから順に並んだ記録の配列である。
//
//	{
//	  "cursors": {
//	    "version": 1,
//	    "data": [
//	      {"path": "/home/u/a.txt", "line": 12, "col": 3, "time": 1763700000}
//	    ]
//	  }
//	}
//
// line は 1 から、col は行の先頭からのバイト数で 0 から数える（Vim の viminfo の印と同じ数え方）。time は閉じた時刻の Unix 秒である。
// 記録の知らない項目は、読み飛ばし、同じパスの記録を書き換えるときに残す（後でファイルごとのほかの印を足せるようにする）。
type cursorRecords []map[string]json.RawMessage

// cursorEntry は、記録の 1 件のうち、このパッケージが読み書きする項目である。
type cursorEntry struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Col  int    `json:"col"`
	Time int64  `json:"time"`
}

// LoadCursor は、置き場 dir の state.json から、ファイル file（絶対パス）を最後に閉じたときのカーソルの位置を読む（FR-166）。
// 記録が無ければ ok は false である。state.json が無い、読めない、壊れている、項目が新しい版のもの（ErrNewerItem）であるときも、
// 記録が無いとして扱い、無い以外の誤りを err で返す（呼び出し側は、開くことを止めずに、位置を戻さないだけにする）。
//
// パスは文字列の一致で探す（シンボリックリンクは解決しない。開くときに使ったパスで記録する）。
//
// req: FR-166
func LoadCursor(dir, file string) (pos Position, ok bool, err error) {
	st, err := ReadState(dir)
	if err != nil {
		return Position{}, false, err
	}
	var recs cursorRecords
	if ok, err := st.Item(CursorsItem, CursorsFormat, &recs); !ok {
		return Position{}, false, err
	}
	for _, r := range recs {
		var e cursorEntry
		if decodeEntry(r, &e) != nil || e.Path != file {
			continue
		}
		if e.Line < 1 || e.Col < 0 {
			return Position{}, false, nil
		}
		return Position{Line: e.Line - 1, Byte: e.Col}, true, nil
	}
	return Position{}, false, nil
}

// SaveCursor は、ファイル file（絶対パス）を閉じたときのカーソルの位置 pos を残す（SaveCursors の 1 件の形）。
//
// req: FR-166
func SaveCursor(dir, file string, pos Position, now time.Time) error {
	return SaveCursors(dir, []Cursor{{Path: file, Pos: pos}}, now)
}

// SaveCursors は、閉じたファイルのカーソルの位置 cursors を、1 回の書き換えでまとめて残す（FR-166）。now は閉じた時刻である。
// server が終わるときに残っていたバッファの位置を、バッファの数だけロックと書き換えを繰り返さずに残すためにまとめる。
//
// UpdateState の中で、読み直した記録から同じパスの記録を除き、cursors を先頭に足し（cursors の中では前のものを新しいとする）、
// MaxCursors 件を超えた古い記録を捨てる。ほかの server が書いた記録と、ほかの項目は消さない。
// 項目が新しい版で書かれていれば、書かずに ErrNewerItem を返す（呼び出し側は警告にしない）。
// 正しくない UTF-8 を含むパスは、JSON の文字列で表せない（encoding/json が U+FFFD に替える）ので残さない（誤りにもしない）。
//
// req: FR-166
func SaveCursors(dir string, cursors []Cursor, now time.Time) error {
	cursors = validCursors(cursors)
	if len(cursors) == 0 {
		return nil
	}
	return UpdateState(dir, func(st *State) error {
		var old cursorRecords
		if _, err := st.Item(CursorsItem, CursorsFormat, &old); err != nil {
			return err
		}
		// 同じパスの古い記録の、知らない項目を引き継ぐ。
		prev := map[string]map[string]json.RawMessage{}
		kept := make(cursorRecords, 0, len(old)+len(cursors))
		saving := map[string]bool{}
		for _, c := range cursors {
			saving[c.Path] = true
		}
		for _, r := range old {
			var e cursorEntry
			if decodeEntry(r, &e) == nil && saving[e.Path] {
				if _, dup := prev[e.Path]; !dup {
					prev[e.Path] = r
				}
				continue
			}
			kept = append(kept, r)
		}
		recs := make(cursorRecords, 0, len(cursors)+len(kept))
		for _, c := range cursors {
			r := prev[c.Path]
			if r == nil {
				r = map[string]json.RawMessage{}
			}
			if err := setEntry(r, c, now); err != nil {
				return err
			}
			recs = append(recs, r)
		}
		recs = append(recs, kept...)
		if len(recs) > MaxCursors {
			recs = recs[:MaxCursors]
		}
		return st.SetItem(CursorsItem, CursorsFormat, recs)
	})
}

// validCursors は、cursors から、正しくない UTF-8 を含むパスと、同じパスの 2 件目以降を除いた写しを返す。
func validCursors(cursors []Cursor) []Cursor {
	out := make([]Cursor, 0, len(cursors))
	seen := map[string]bool{}
	for _, c := range cursors {
		if !utf8.ValidString(c.Path) || seen[c.Path] {
			continue
		}
		seen[c.Path] = true
		out = append(out, c)
	}
	return out
}

// setEntry は、記録 r の、このパッケージが読み書きする項目を c と now にする。ほかの項目は残す。
func setEntry(r map[string]json.RawMessage, c Cursor, now time.Time) error {
	for k, v := range map[string]any{"path": c.Path, "line": max(c.Pos.Line, 0) + 1, "col": max(c.Pos.Byte, 0), "time": now.Unix()} {
		raw, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("persist: %w", err)
		}
		r[k] = raw
	}
	return nil
}

// decodeEntry は、記録の 1 件から、このパッケージの読む項目を取り出す。path の無い記録は誤りにする。
func decodeEntry(r map[string]json.RawMessage, e *cursorEntry) error {
	for k, dst := range map[string]any{"path": &e.Path, "line": &e.Line, "col": &e.Col, "time": &e.Time} {
		raw, ok := r[k]
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
