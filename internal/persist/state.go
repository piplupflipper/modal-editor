package persist

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// ErrNewerItem は、state.json の項目が、この本体の知る版より新しい版で書かれていることを表す
// （パッケージの doc の「state.json の項目と版」）。その項目は読まず、書き換えずに残す。
// 新しい本体の server が書いた項目を、古い本体の server が壊さないためである。利用者には直せないので、呼び出し側は警告にしない。
var ErrNewerItem = errors.New("persist: state item was written by a newer version")

// lockTimeout は、状態のファイルのロックを待つ時間の上限である。
// ロックは読み直して書き換える間（ミリ秒の単位）しか持たないので、超えたら持ち主が止まっているとみなし、書かずに誤りにする。
const lockTimeout = 2 * time.Second

// State は、state.json の中身である。項目の名前から、項目の JSON（{"version": 版, "data": 中身}）への対応を持つ。
// 読んだ項目のうち、State.SetItem で書き換えなかったものは、書くときにそのまま書き戻す（知らない名前と新しい版の項目を消さない）。
type State struct {
	items map[string]json.RawMessage
}

// item は、state.json の 1 項目の形である。
type item struct {
	Version int             `json:"version"`
	Data    json.RawMessage `json:"data"`
}

// Item は、名前が name の項目を、版が version のものとして v に読む（v は json.Unmarshal に渡す先）。
// 項目が無いか、壊れているか（JSON の形が違う、版が 1 より小さい）、version より古い版なら、ok を false にして誤りを返さない
// （古い版と壊れた項目は、SetItem で書き直す）。version より新しい版なら ErrNewerItem を返す。
func (s *State) Item(name string, version int, v any) (ok bool, err error) {
	raw, found := s.items[name]
	if !found {
		return false, nil
	}
	var it item
	if json.Unmarshal(raw, &it) != nil || it.Version < 1 || it.Version < version {
		return false, nil //nolint:nilerr // 壊れた項目と古い版の項目は無いものとして扱い、SetItem で書き直す（Item の doc）
	}
	if it.Version > version {
		return false, ErrNewerItem
	}
	// 壊れた中身は無いものとして扱い、SetItem で書き直す（Item の doc）。
	return json.Unmarshal(it.Data, v) == nil, nil
}

// SetItem は、名前が name の項目を、版が version の v（json.Marshal に渡すもの）にする。
// 今の項目が version より新しい版なら、書き換えずに ErrNewerItem を返す。
func (s *State) SetItem(name string, version int, v any) error {
	var probe struct{}
	if _, err := s.Item(name, version, &probe); errors.Is(err, ErrNewerItem) {
		return err
	}
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("persist: %w", err)
	}
	raw, err := json.Marshal(item{Version: version, Data: data})
	if err != nil {
		return fmt.Errorf("persist: %w", err)
	}
	if s.items == nil {
		s.items = map[string]json.RawMessage{}
	}
	s.items[name] = raw
	return nil
}

// ReadState は、置き場 dir の state.json を読む。ロックは掛けない（書く側が rename で置き換えるので、前か後の全体を読む）。
// 無ければ os.ErrNotExist を包んだ誤りを、JSON のオブジェクトとして読めなければ誤りを返す。
func ReadState(dir string) (*State, error) {
	path := filepath.Join(dir, StateFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("persist: %w", err)
	}
	var items map[string]json.RawMessage
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, fmt.Errorf("persist: %s: %w", path, err)
	}
	if items == nil {
		return nil, fmt.Errorf("persist: %s: not an object", path)
	}
	return &State{items: items}, nil
}

// UpdateState は、置き場 dir の state.json を、fn で書き換える（項目に依らない、読み直して合わせて書く手順）。
//
// 同じ state.json を、複数の server（tmux の window ごとに 1 つ。FR-59）が書く。
// このため、ロックのファイル（state.json.lock）に排他の flock を掛け（lockTimeout まで待つ）、その中で state.json を読み直し、
// 読み直した中身を fn に渡して合わせさせ、一時ファイルと rename で書き換える。ほかの server が書いた項目と記録は、fn が消さない限り残る。
// state.json が無いか壊れているときは、空の中身を fn に渡す（壊れた中身は捨てて書き直す）。
// fn が誤りを返したら、書かずにその誤りを返す。置き場のディレクトリが無ければ 0700 で作る。
func UpdateState(dir string, fn func(*State) error) error {
	if err := ensureDir(dir); err != nil {
		return err
	}
	unlock, err := lockState(filepath.Join(dir, StateFileName+lockSuffix))
	if err != nil {
		return err
	}
	defer unlock()
	st, err := ReadState(dir)
	if err != nil {
		st = &State{items: map[string]json.RawMessage{}}
	}
	if ferr := fn(st); ferr != nil {
		return ferr
	}
	data, err := json.MarshalIndent(st.items, "", "  ")
	if err != nil {
		return fmt.Errorf("persist: %w", err)
	}
	return writeFileAtomic(filepath.Join(dir, StateFileName), append(data, '\n'))
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
