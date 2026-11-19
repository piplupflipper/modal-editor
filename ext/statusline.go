package ext

import "strings"

// MethodStatusLineSet は、拡張機能から本体への通知である。params は StatusLineSetParams。
// 宣言ファイルの contributes.statusline で足した部品の中身を送る（基本設計 5.3、7.1。FR-92）。
//
// 本体は、部品の ID ごとに最後に届いた中身を持ち、利用者が設定の statusline か tabline に並べた部品だけを描く。
// 並べていない部品の中身も持つので、拡張機能は設定を知らずに送ってよい。
// 宣言ファイルで足していない部品の ID と、他の拡張機能が足した部品の ID の通知は捨てる。
// 拡張機能が異常で終わるか止まったら、その拡張機能の部品の中身をすべて消す（他の表示のデータと同じ。基本設計 6.7）。
//
// 中身はバッファに依らず、どの client のどのバッファのステータス行にも同じものを出す（仮。lualine の mrreview の状態と、
// 本体のメモリの使用量のような、エディタ全体の状態を出す部品を想定した）。
const MethodStatusLineSet = "statusline/set"

// StatusLineSetParams は、statusline/set の params である。
type StatusLineSetParams struct {
	// Part は、部品の ID である。宣言ファイルの contributes.statusline で足した ID を書く。
	Part string `json:"part"`
	// Text は、部品の中身の文字列である。空なら部品を出さない（lualine の cond が偽の部品と同じ）。
	// 本体は 1 行で描くので、改行とタブなどの制御文字は空白 1 つに置き換える（StatusLineText）。
	Text string `json:"text"`
	// Style は、部品を描く色の名前（本体の色の名前のどれか。基本設計 7.1。docs/usage.md の「見た目の設定」）である。
	// 本体は、ステータス行の色の上に、この名前の色を重ねる（本体の部品の色と同じ規則）。
	// 空なら、利用者の設定の statusline.ext の色を使う。本体の知らない名前なら、色を重ねない。
	// 状態に応じて色を変える部品（lualine の mrreview の drift の警告）は、送るたびに名前を変える。
	Style string `json:"style,omitempty"`
}

// StatusLineText は、statusline/set の Text を、本体がステータス行に描く 1 行の文字列にする。
// 改行、タブ、その他の制御文字（Unicode の Cc）を空白 1 つに置き換える。
func StatusLineText(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return ' '
		}
		return r
	}, s)
}

// StatusLinePart は、宣言ファイルの contributes.statusline の 1 つの部品である（基本設計 5.1、7.1。FR-92）。
type StatusLinePart struct {
	// ID は、部品の ID である。拡張機能の ID と "." を前に付け、その後には "." を含めない（拡張機能の ID が "mrreview" なら "mrreview.status"）。
	// 利用者は、設定の statusline と tabline の left と right にこの ID を書いて並べる。
	// 本体の部品の ID は "." を含まないので、本体の部品と重ならない。拡張機能の ID は一意で、最後の "." より前が拡張機能の ID に決まるので、
	// 拡張機能の ID が "." を含んでも、他の拡張機能の部品と重ならない。
	ID string `json:"id"`
	// Description は、部品の説明文である。本体はヘルプとキーの一覧に出さないが、利用者が宣言ファイルを読んで並べる部品を選べるように書く。
	Description string `json:"description,omitempty"`
}
