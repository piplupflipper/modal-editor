package input

import (
	"testing"
	"unicode"

	"github.com/piplupflipper/vi-imitation/internal/editcmd"
)

// caseRelation は、大文字のキーの操作が、小文字のキーの操作の何に当たるかである（FR-144）。
type caseRelation int

const (
	caseReverse   caseRelation = iota + 1 // 向きを逆にしたもの（n と N）
	caseLarger                            // 単位を大きくしたもの（w と W）
	caseLineEdge                          // 行の端まで広げたもの（d と D）
	caseException                         // 例外（J と K。要件定義 FR-144）
)

// uppercaseKey は、既定のキーの表の、英字 1 文字の大文字のキー 1 つである。
type uppercaseKey struct {
	mode  MapMode
	upper rune
	// lower は、対になる小文字の側のキーの列である。lowerMode のモードで、本体の既定のキーとして登録してあることを確かめる。
	// 空なら確かめない（例外のキー）。
	lower     string
	lowerMode MapMode
	rel       caseRelation
}

// uppercaseKeys は、本体の既定のキーの、英字 1 文字の大文字のキーのすべてと、小文字のキーとの関係である（FR-144）。
// 大文字のキーを足すときは、ここに行を足す。どの関係にも当たらないキーは、要件定義の FR-144 の例外に書いてから caseException にする。
//
// 次の点は、#280 の issue の表に無いので、実装のときに決めた。
//   - G は gg と向きが逆（最後の行へ、最初の行へ）とした
//   - ノーマルモードの s は囲みの操作の接頭辞で、既定の操作が無い（要件定義 1.1）。S（cc）の対は、ビジュアルモードの s
//     （選択を消して入力する。Vim のノーマルモードの s は cl）とした
//   - ビジュアルモードの u と U は、小文字にする操作と大文字にする操作の組なので、向きが逆とした
//   - ビジュアルモードの I と A は、ノーマルモードの i と a を選んだ行ごとの行の端まで広げたもの（FR-141）とした
//     （ビジュアルモードの i と a は、テキストオブジェクトの接頭辞である）
var uppercaseKeys = []uppercaseKey{
	{MapNormal, 'N', "n", MapNormal, caseReverse},
	{MapNormal, 'F', "f", MapNormal, caseReverse},
	{MapNormal, 'T', "t", MapNormal, caseReverse},
	{MapNormal, 'G', "gg", MapNormal, caseReverse},
	{MapNormal, 'P', "p", MapNormal, caseReverse},
	{MapNormal, 'O', "o", MapNormal, caseReverse},
	{MapNormal, 'X', "x", MapNormal, caseReverse},
	{MapNormal, 'U', "u", MapNormal, caseReverse},
	{MapNormal, 'W', "w", MapNormal, caseLarger},
	{MapNormal, 'B', "b", MapNormal, caseLarger},
	{MapNormal, 'E', "e", MapNormal, caseLarger},
	{MapNormal, 'V', "v", MapNormal, caseLarger},
	{MapNormal, 'S', "s", MapVisual, caseLarger},
	{MapNormal, 'A', "a", MapNormal, caseLineEdge},
	{MapNormal, 'I', "i", MapNormal, caseLineEdge},
	{MapNormal, 'D', "d", MapNormal, caseLineEdge},
	{MapNormal, 'C', "c", MapNormal, caseLineEdge},
	{MapNormal, 'Y', "y", MapNormal, caseLineEdge},
	{MapNormal, 'J', "", 0, caseException},
	{MapNormal, 'K', "", 0, caseException},
	{MapVisual, 'N', "n", MapVisual, caseReverse},
	{MapVisual, 'F', "f", MapVisual, caseReverse},
	{MapVisual, 'T', "t", MapVisual, caseReverse},
	{MapVisual, 'G', "gg", MapVisual, caseReverse},
	{MapVisual, 'U', "u", MapVisual, caseReverse},
	{MapVisual, 'W', "w", MapVisual, caseLarger},
	{MapVisual, 'B', "b", MapVisual, caseLarger},
	{MapVisual, 'E', "e", MapVisual, caseLarger},
	{MapVisual, 'V', "v", MapVisual, caseLarger},
	{MapVisual, 'D', "d", MapVisual, caseLarger},
	{MapVisual, 'C', "c", MapVisual, caseLarger},
	{MapVisual, 'S', "s", MapVisual, caseLarger},
	{MapVisual, 'Y', "y", MapVisual, caseLarger},
	{MapVisual, 'I', "i", MapNormal, caseLineEdge},
	{MapVisual, 'A', "a", MapNormal, caseLineEdge},
}

// 本体の既定のキーの、ノーマルモードとビジュアルモードの英字 1 文字の大文字のキーは、どれも小文字のキーとの関係
// （向きが逆、単位が大きい、行の端まで）を uppercaseKeys に持ち、例外は J と K だけである。
// 対になる小文字のキーも、既定のキーとして登録してある。
//
// req: FR-144
func TestUppercaseKeys(t *testing.T) {
	km := DefaultKeymap()
	type key struct {
		mode  MapMode
		upper rune
	}
	bound := map[key]Binding{}
	for _, b := range km.Bindings() {
		if len(b.Keys) != 1 || !b.Keys[0].Plain() || b.Keys[0].Rune > unicode.MaxASCII || !unicode.IsUpper(b.Keys[0].Rune) {
			continue
		}
		for _, mode := range []MapMode{MapNormal, MapVisual} {
			if b.Modes&mode != 0 {
				bound[key{mode, b.Keys[0].Rune}] = b
			}
		}
	}
	listed := map[key]bool{}
	for _, u := range uppercaseKeys {
		k := key{u.mode, u.upper}
		if listed[k] {
			t.Errorf("%s の %c が uppercaseKeys に 2 行ある", ModeName(u.mode), u.upper)
		}
		listed[k] = true
		if _, ok := bound[k]; !ok {
			t.Errorf("%s の %c が既定のキーに無い", ModeName(u.mode), u.upper)
		}
		if u.rel == caseException {
			if u.mode != MapNormal || (u.upper != 'J' && u.upper != 'K') {
				t.Errorf("%s の %c は FR-144 の例外ではない", ModeName(u.mode), u.upper)
			}
			continue
		}
		if _, ok := km.Lookup(u.lowerMode, editcmd.MustParseKeys(u.lower)); !ok {
			t.Errorf("%s の %c の対の %s（%s）が既定のキーに無い", ModeName(u.mode), u.upper, u.lower, ModeName(u.lowerMode))
		}
	}
	for k, b := range bound {
		if !listed[k] {
			t.Errorf("%s の %c（%s）の、小文字のキーとの関係が uppercaseKeys に無い（FR-144）", ModeName(k.mode), k.upper, b.Ref.ID)
		}
	}
}
