package input

import (
	"testing"

	"github.com/piplupflipper/modal-editor/internal/editcmd"
)

// 設定の leader として受け付けるキーで始まる列は、どのモードでも操作の表に登録でき（先に読むキーの表を共有する）、
// 先に読むキーで始まる leader は誤りになる。本体の既定の表と拡張機能の <Leader> のキーが、leader の値で登録できなくならない。
//
// req: FR-97
func TestLeaderNotReserved(t *testing.T) {
	ref := editcmd.Ref{ID: "x.op", Extension: "x"}
	for r := rune(0x21); r < 0x7f; r++ {
		k := editcmd.Rune(r)
		_, leaderErr := editcmd.ParseLeader(editcmd.KeysString([]editcmd.Key{k}))
		reserved := reservedFirstKey(k, MapNormal|MapVisual|MapOperatorPending)
		if reserved != (leaderErr != nil) {
			t.Errorf("%q: 先に読むキー = %v, ParseLeader の誤り = %v", r, reserved, leaderErr)
		}
		if leaderErr != nil {
			continue
		}
		keys, err := editcmd.ParseKeysLeader("<Leader>zz", []editcmd.Key{k})
		if err != nil {
			t.Fatal(err)
		}
		km := NewKeymap()
		if err := km.Bind(Binding{Kind: BindMotion, Keys: keys, Ref: ref}); err != nil {
			t.Errorf("leader %q: %v", r, err)
		}
	}
}
