package input

import (
	"fmt"

	"github.com/piplupflipper/modal-editor/internal/editcmd"
)

// spec は、本体の操作の表の 1 行である。
type spec struct {
	keys      string // ParseKeys の表記
	id        string
	takesChar bool
	next      editcmd.Mode // 実行の後のモード
	modes     MapMode      // 有効なモード。0 なら種類ごとの既定（defaultModes）
}

// 本体の操作の一覧。
// 一覧は issue #9 の「3.」の案（段階 1）で、裁定待ちである。
// 追加・削除はこの表の行の変更だけで済み、Parser には手を入れなくてよい。
// ID は本体の中の名前で、実行の側（internal/command）がこの ID で実装を引く。
//
// ~ は Vim の既定（tildeop が off）に合わせ、対象を取らない操作にした。大文字と小文字のオペレーターは g~、gu、gU である。
// マクロ（q、@）とドットリピート（.）は、Parser が記録中かどうかで読み方を変えるので、表ではなく Parser が持つ。
// 有効なモードは、modes を書かない行では種類ごとの既定（defaultModes）である。
// ビジュアルモードでは、オペレーター（d、c、y など）は選択を対象にする。ビジュアルモードだけの登録は modes に MapVisual を書く。
//
// req: FR-03
var (
	builtinMotions = []spec{
		{keys: "h", id: "left"},
		{keys: "j", id: "down"},
		{keys: "k", id: "up"},
		{keys: "l", id: "right"},
		{keys: "w", id: "word-next"},
		{keys: "b", id: "word-prev"},
		{keys: "e", id: "word-end"},
		{keys: "W", id: "bigword-next"},
		{keys: "B", id: "bigword-prev"},
		{keys: "E", id: "bigword-end"},
		{keys: "0", id: "line-start"},
		{keys: "^", id: "line-first-nonblank"},
		{keys: "$", id: "line-end"},
		{keys: "gg", id: "file-start"},
		{keys: "G", id: "file-end"},
		{keys: "f", id: "find-forward", takesChar: true},
		{keys: "F", id: "find-backward", takesChar: true},
		{keys: "t", id: "till-forward", takesChar: true},
		{keys: "T", id: "till-backward", takesChar: true},
		{keys: ";", id: "find-repeat"},
		{keys: ",", id: "find-repeat-reverse"},
		{keys: "%", id: "match-pair"},
		{keys: "{", id: "paragraph-prev"},
		{keys: "}", id: "paragraph-next"},
	}

	builtinOperators = []spec{
		{keys: "d", id: "delete"},
		{keys: "c", id: "change", next: editcmd.ModeInsert},
		{keys: "y", id: "yank"},
		{keys: ">", id: "indent"},
		{keys: "<", id: "dedent"},
		{keys: "g~", id: "toggle-case"},
		{keys: "gu", id: "lowercase"},
		{keys: "gU", id: "uppercase"},
		// ビジュアルモードだけの別名。x は d、s は c に当たる。~、u、U は選択の大文字と小文字を変える。
		{keys: "x", id: "delete", modes: MapVisual},
		{keys: "s", id: "change", next: editcmd.ModeInsert, modes: MapVisual},
		{keys: "~", id: "toggle-case", modes: MapVisual},
		{keys: "u", id: "lowercase", modes: MapVisual},
		{keys: "U", id: "uppercase", modes: MapVisual},
	}

	builtinTextObjects = []spec{
		{keys: "iw", id: "inner-word"},
		{keys: "aw", id: "a-word"},
		{keys: "iW", id: "inner-bigword"},
		{keys: "aW", id: "a-bigword"},
		{keys: `i"`, id: "inner-double-quote"},
		{keys: `a"`, id: "a-double-quote"},
		{keys: "i'", id: "inner-single-quote"},
		{keys: "a'", id: "a-single-quote"},
		// 括弧のテキストオブジェクトは、Vim と同じく閉じ括弧と b、B の別名を持つ。
		{keys: "i(", id: "inner-paren"},
		{keys: "i)", id: "inner-paren"},
		{keys: "ib", id: "inner-paren"},
		{keys: "a(", id: "a-paren"},
		{keys: "a)", id: "a-paren"},
		{keys: "ab", id: "a-paren"},
		{keys: "i[", id: "inner-bracket"},
		{keys: "i]", id: "inner-bracket"},
		{keys: "a[", id: "a-bracket"},
		{keys: "a]", id: "a-bracket"},
		{keys: "i{", id: "inner-brace"},
		{keys: "i}", id: "inner-brace"},
		{keys: "iB", id: "inner-brace"},
		{keys: "a{", id: "a-brace"},
		{keys: "a}", id: "a-brace"},
		{keys: "aB", id: "a-brace"},
		{keys: "ip", id: "inner-paragraph"},
		{keys: "ap", id: "a-paragraph"},
	}

	builtinActions = []spec{
		{keys: "~", id: "toggle-case"},
		{keys: "p", id: "put-after"},
		{keys: "P", id: "put-before"},
		{keys: "x", id: "delete-char"},
		{keys: "X", id: "delete-char-before"},
		{keys: "r", id: "replace-char", takesChar: true},
		// 省略形のコマンド。D は d$、C は c$、S は cc、s は cl、Y は yy に当たり、実行の側（internal/command）が
		// オペレーターと対象の組に置き換えて実行する。オペレーターの表ではなく操作の表に置くのは、
		// 対象を待たずに 1 キーで完結するからである。ビジュアルモードの D、C、S、Y（行単位の操作）は未登録である。
		{keys: "D", id: "delete-to-line-end"},
		{keys: "C", id: "change-to-line-end", next: editcmd.ModeInsert},
		{keys: "S", id: "substitute-line", next: editcmd.ModeInsert},
		{keys: "s", id: "substitute-char", next: editcmd.ModeInsert},
		{keys: "Y", id: "yank-line"},
		{keys: "J", id: "join-lines"},
		{keys: "o", id: "open-below", next: editcmd.ModeInsert},
		{keys: "O", id: "open-above", next: editcmd.ModeInsert},
		{keys: "i", id: "insert", next: editcmd.ModeInsert},
		{keys: "a", id: "append", next: editcmd.ModeInsert},
		{keys: "I", id: "insert-line-start", next: editcmd.ModeInsert},
		{keys: "A", id: "append-line-end", next: editcmd.ModeInsert},
		{keys: "u", id: "undo"},
		{keys: "<C-r>", id: "redo"},
		// Ex コマンド（:w など）の一覧と解釈はコマンドラインの担当で、ここでは切り替えだけを持つ。
		// ビジュアルモードの : は、選択した行の範囲（'<,'>）を入力した状態で始める。
		{keys: ":", id: "command-line", next: editcmd.ModeCommandLine, modes: MapNormal | MapVisual},
		// ビジュアルモードへの出入り。ビジュアルモードの中では、同じ種類なら抜け、違う種類なら切り替える。
		{keys: "v", id: "visual", modes: MapNormal | MapVisual},
		{keys: "V", id: "visual-line", modes: MapNormal | MapVisual},
		// ビジュアルモードの o は、カーソルを選択の反対の端へ移す。
		{keys: "o", id: "visual-swap", modes: MapVisual},
	}
)

// DefaultKeymap は、本体の操作を登録したキーマップを返す。
// 呼ぶたびに新しい値を返すので、拡張機能の操作を足しても他の呼び出し元に影響しない。
//
// req: FR-03
func DefaultKeymap() *Keymap {
	m := NewKeymap()
	tables := []struct {
		kind  BindingKind
		specs []spec
	}{
		{BindMotion, builtinMotions},
		{BindOperator, builtinOperators},
		{BindTextObject, builtinTextObjects},
		{BindAction, builtinActions},
	}
	for _, t := range tables {
		for _, s := range t.specs {
			err := m.Bind(Binding{
				Kind:      t.kind,
				Modes:     s.modes,
				Keys:      editcmd.MustParseKeys(s.keys),
				Ref:       editcmd.Ref{ID: s.id},
				TakesChar: s.takesChar,
				NextMode:  s.next,
			})
			if err != nil {
				// 表の誤りはプログラムの誤りで、テストで検出する。
				panic(fmt.Sprintf("本体の操作の表の誤り: %v", err))
			}
		}
	}
	return m
}
