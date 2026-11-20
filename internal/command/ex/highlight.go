package ex

import "github.com/piplupflipper/vi-imitation/internal/option"

// 検索の一致の色付け（FR-137、FR-138。基本設計 6.7 の「検索の一致の色付け」）。
//
// 色を付けるかは設定の search.highlight で決まり、色を付けるパターンは直前の検索のパターン（State.LastPattern）である。
// どちらも描画（internal/editor）が描くたびに読む。このファイルは、その間に挟まる「一致の色を消した状態」を持つ。
// 消した状態は、次にパターンを使う操作か、search.highlight を設定し直したときに効かなくなる（Vim 9.1 の :nohlsearch と同じ）。

// clearHighlightName は、一致の色を消す Ex コマンドの正規の名前である。点を含むので省略できない。
// Vim の名前 :nohlsearch は、名前を付け替えるだけの別名（aliases）にする。
const clearHighlightName = "search.clearHighlight"

// ClearedHighlight は、一致の色を消した状態（State.ClearedHighlight）である。
type ClearedHighlight struct {
	// Revision は、消したときの、設定の search.highlight を設定した回数（Options.Revision）である。
	// 設定の値を読み書きする口が無ければ 0 にする。
	Revision uint64
}

// UsePattern は、パターン pat を直前の検索のパターンにし、一致の色を消した状態を解く（FR-138）。
// パターンを使う操作（/、?、n、N、*、#、:s、:g、パターンのアドレス）は、どれもこれを通る。
// Vim 9.1 と同じく、パターンが見つからなかった場合も解く（2026-11-20 に :nohlsearch の後の /zzz、n、:s/qqq/、:g/qqq/ の後の
// v:hlsearch が 1 になることを確かめた）。n と N は、パターンを変えずにこれを通る。
//
// req: FR-138
func (st *State) UsePattern(pat string) {
	st.LastPattern = pat
	st.ClearedHighlight = nil
}

// HighlightPattern は、一致の色を付けるパターンを返す。色を消していれば空を返す。
// rev は、設定の search.highlight の今の回数（option.Values.Revision）で、消したときの回数と違えば、消した状態は効かない
// （:nohlsearch の後に :set hlsearch を打ち直すと色が戻る。Vim 9.1 と同じ）。
// search.highlight の値そのものは見ない（呼び出し側が読む）。
//
// req: FR-137, FR-138
func (st *State) HighlightPattern(rev uint64) string {
	if c := st.ClearedHighlight; c != nil && c.Revision == rev {
		return ""
	}
	return st.LastPattern
}

// runClearHighlight は :search.clearHighlight（別名 :noh[lsearch]）を実行する（FR-138）。
// 次にパターンを使う操作か、search.highlight を設定し直すまで、一致の色を消す。設定の値と直前のパターンは変えない。
// 引数は取らない（付ければ E488）。範囲と "!" も取らない（Vim 9.1 の :nohlsearch と同じ）。
//
// req: FR-138
func runClearHighlight(c *execCtx, inv *invocation) error {
	if err := noArg(inv); err != nil {
		return err
	}
	var rev uint64
	if opts := c.st.Options; opts != nil {
		if d, ok := optionLookup(option.SearchHighlight); ok {
			rev = opts.Revision(d)
		}
	}
	c.st.ClearedHighlight = &ClearedHighlight{Revision: rev}
	return nil
}
