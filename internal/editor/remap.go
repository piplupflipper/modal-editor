package editor

import (
	"fmt"
	"slices"
	"sort"

	"github.com/piplupflipper/modal-editor/internal/editcmd"
)

// mapMode は、利用者のキーマップを引くモードである。
type mapMode int

const (
	mapNone mapMode = iota
	mapNormal
	mapVisual
	mapInsert
	mapCommandLine
)

// mapModeNames は、設定のモードの名前（基本設計 7 章の keymap のキー）である。
var mapModeNames = map[string]mapMode{
	"normal":      mapNormal,
	"visual":      mapVisual,
	"insert":      mapInsert,
	"commandLine": mapCommandLine,
}

// mapping は、利用者のキーマップの 1 件である。
type mapping struct {
	lhs, rhs []editcmd.Key
}

// remapper は、利用者のキーマップ（基本設計 7 章の keymap）で、キーの列を別のキーの列に展開する。
// internal/input の Parser の前段の層である（input.Keymap の doc）。
//
// 展開した列は、もう一度展開しない（Vim の noremap）。
// 登録の接頭辞を受け取っている間はキーを溜め、続くキーで登録に進めなくなった時点か、時間切れ（flush）で確定させる。
// マクロには展開した後のキーが記録される。再生は Parser へ直接渡すので、展開の層を通らなくても同じ結果になる。
type remapper struct {
	maps    map[mapMode][]mapping
	pending []editcmd.Key
	mode    mapMode
}

// newRemapper は、設定のキーマップから remapper を作る。表記の誤りのある項目は捨て、誤りを返す。
// キーマップは本体が持つので、拡張機能の起動を待たずに効く（FR-41）。
//
// req: FR-41
func newRemapper(cfg map[string]map[string]string) (*remapper, []error) {
	r := &remapper{maps: map[mapMode][]mapping{}}
	var errs []error
	names := make([]string, 0, len(cfg))
	for n := range cfg {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		mode, ok := mapModeNames[name]
		if !ok {
			errs = append(errs, fmt.Errorf("keymap: 未知のモード %q", name))
			continue
		}
		for lhsText, rhsText := range cfg[name] {
			lhs, err := editcmd.ParseKeys(lhsText)
			if err != nil {
				errs = append(errs, fmt.Errorf("keymap.%s: キーの列 %q を読めない: %w", name, lhsText, err))
				continue
			}
			if len(lhs) == 0 {
				errs = append(errs, fmt.Errorf("keymap.%s: キーの列が空です", name))
				continue
			}
			rhs, err := editcmd.ParseKeys(rhsText)
			if err != nil {
				errs = append(errs, fmt.Errorf("keymap.%s: 展開する列 %q を読めない: %w", name, rhsText, err))
				continue
			}
			r.maps[mode] = append(r.maps[mode], mapping{lhs: lhs, rhs: rhs})
		}
	}
	return r, errs
}

// pendingKeys は、登録の接頭辞を溜めているかを返す。
func (r *remapper) pendingKeys() bool { return len(r.pending) > 0 }

// reset は、溜めたキーを捨てる。
func (r *remapper) reset() { r.pending = nil }

// lookup は、mode で keys と一致する登録と、keys より長く keys で始まる登録があるかを返す。
func (r *remapper) lookup(mode mapMode, keys []editcmd.Key) (exact *mapping, longer bool) {
	for i := range r.maps[mode] {
		m := &r.maps[mode][i]
		switch {
		case slices.Equal(m.lhs, keys):
			exact = m
		case len(m.lhs) > len(keys) && slices.Equal(m.lhs[:len(keys)], keys):
			longer = true
		}
	}
	return exact, longer
}

// feed は、キーを 1 個受け取り、command.State へ渡すキーの列を返す。登録の途中なら空を返す。
func (r *remapper) feed(mode mapMode, k editcmd.Key) []editcmd.Key {
	var out []editcmd.Key
	if len(r.pending) > 0 && mode != r.mode {
		out = r.flush()
	}
	if mode == mapNone || len(r.maps[mode]) == 0 {
		return append(out, k)
	}
	r.mode = mode
	seq := append(slices.Clone(r.pending), k)
	exact, longer := r.lookup(mode, seq)
	switch {
	case longer:
		r.pending = seq
		return out
	case exact != nil:
		r.pending = nil
		return append(out, exact.rhs...)
	case len(r.pending) == 0:
		return append(out, k)
	}
	// 溜めた列は登録の接頭辞だったが、k で進めなくなった。溜めた列を確定させてから k を読み直す。
	out = append(out, r.flush()...)
	return append(out, r.feed(mode, k)...)
}

// flush は、溜めた列を確定させる（時間切れ、または続くキーで登録に進めなくなったとき）。
// 溜めた列の最も長い接頭辞が登録と一致すれば展開し、残りを読み直す。一致しなければ先頭の 1 個をそのまま渡し、残りを読み直す。
func (r *remapper) flush() []editcmd.Key {
	p := r.pending
	r.pending = nil
	if len(p) == 0 {
		return nil
	}
	var out []editcmd.Key
	rest := p[1:]
	head := p[:1]
	for i := len(p); i > 0; i-- {
		if m, _ := r.lookup(r.mode, p[:i]); m != nil {
			head, rest = m.rhs, p[i:]
			break
		}
	}
	out = append(out, head...)
	for _, k := range rest {
		out = append(out, r.feed(r.mode, k)...)
	}
	return out
}
