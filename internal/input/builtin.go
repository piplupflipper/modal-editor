package input

import "fmt"

// spec は、本体の操作の表の 1 行である。
type spec struct {
	keys      string // ParseKeys の表記
	id        string
	takesChar bool
	next      Mode // 実行の後のモード
}

// 本体の操作の一覧。
// 一覧は issue #9 の「3.」の案（段階 1）で、裁定待ちである。
// 追加・削除はこの表の行の変更だけで済み、Parser には手を入れなくてよい。
// ID は本体の中の名前で、実行の側（internal/command）がこの ID で実装を引く。
//
// ~ は Vim の既定（tildeop が off）に合わせ、対象を取らない操作にした。
// オペレーターの ~（g~）、gu、gU は #9 の案のとおり後回しにする。
// マクロ（q、@）とドットリピート（.）は、Parser が記録中かどうかで読み方を変えるので、表ではなく Parser が持つ。
// 有効なモードは種類ごとの既定（defaultModes）である。
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
		{keys: "c", id: "change", next: ModeInsert},
		{keys: "y", id: "yank"},
		{keys: ">", id: "indent"},
		{keys: "<", id: "dedent"},
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
		{keys: "i(", id: "inner-paren"},
		{keys: "a(", id: "a-paren"},
		{keys: "i[", id: "inner-bracket"},
		{keys: "a[", id: "a-bracket"},
		{keys: "i{", id: "inner-brace"},
		{keys: "a{", id: "a-brace"},
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
		{keys: "J", id: "join-lines"},
		{keys: "o", id: "open-below", next: ModeInsert},
		{keys: "O", id: "open-above", next: ModeInsert},
		{keys: "i", id: "insert", next: ModeInsert},
		{keys: "a", id: "append", next: ModeInsert},
		{keys: "I", id: "insert-line-start", next: ModeInsert},
		{keys: "A", id: "append-line-end", next: ModeInsert},
		// Ex コマンド（:w など）の一覧と解釈はコマンドラインの担当で、ここでは切り替えだけを持つ。
		{keys: ":", id: "command-line", next: ModeCommandLine},
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
				Keys:      MustParseKeys(s.keys),
				Ref:       Ref{ID: s.id},
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
