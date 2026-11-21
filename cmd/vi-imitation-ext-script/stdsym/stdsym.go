// Package stdsym は、スクリプトの拡張機能（cmd/vi-imitation-ext-script）が yaegi に渡す、標準ライブラリの記号の表の並びを持つ。
//
// 実行ファイル（package main）の newInterpreter と、本体に埋め込む文書の生成器（cmd/docgen。vi-imitation --docs script-symbols）が、
// どちらもこの並びを使う。表を足し引きすると、スクリプトから使える記号と文書の一覧が一緒に変わり、
// 文書を生成し直し忘れると make docs-check が失敗する。
package stdsym

import (
	"reflect"

	"github.com/traefik/yaegi/stdlib"
	"github.com/traefik/yaegi/stdlib/unrestricted"
)

// Table は、yaegi に Use させる 1 つの記号の表と、読み込めなかったときの誤りの文言に出す名前である。
type Table struct {
	Name    string
	Symbols map[string]map[string]reflect.Value
}

// Tables は、newInterpreter が Use する標準ライブラリの記号の表を、Use する順に返す。
// 制限なしのモードで動かすので、標準ライブラリ（stdlib.Symbols）の後に、os/exec と os.Exit などを持つ unrestricted.Symbols を重ねる。
func Tables() []Table {
	return []Table{
		{Name: "stdlib", Symbols: stdlib.Symbols},
		{Name: "unrestricted", Symbols: unrestricted.Symbols},
	}
}
