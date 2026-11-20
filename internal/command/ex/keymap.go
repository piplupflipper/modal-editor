package ex

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// KeymapCommandLine は、設定の keymap の右辺に書いた Ex コマンド（{ "command": name, "args": args }。FR-154）を確かめ、
// 実行するコマンドラインの文字列を返す。コマンドラインは name と args を空白でつないだもの（args が空なら name だけ）で、
// 呼び出し側（internal/command の State）が、コマンドラインで打ったときと同じく Execute で実行する。
// t は Ex コマンドの別名の表（State.Aliases に入れるもの。nil なら本体の行だけ）、extCommands は拡張機能のコマンドの ID
// （宣言ファイルの contributes.commands と provides の操作の名前）である。
//
// name に書けるのは、正規の名前だけである。
//
//   - 本体の Ex コマンドの正規の名前（点を含み、省略しない名前。option.set、search.clearHighlight、tab.new）
//   - 拡張機能のコマンドの ID（finder.files。コマンドラインと同じく、ID と完全に一致する名前だけ）
//
// 次の名前は誤りにする。
//
//   - 正規の名前の無い本体の Ex コマンドと、その省略形（write、w。正規の名前を付けた後に書けるようになる）
//   - 別名（set、nohlsearch、設定ファイルの commandAliases の別名）。別名は、コマンドラインで打つための入口であるため
//   - 本体の Ex コマンドか別名と同じ名前の、拡張機能のコマンドの ID（コマンドラインでは本体が先に当たり、そのコマンドを呼べないため）
//   - 知らない名前と、空白や "|" や "!" を含む名前（コマンドラインで 1 つの名前として読めない）
//
// 実行するのは 1 つのコマンドだけである。本体のコマンドのうち "|" で次のコマンドへ続ける（barSplit の）ものは、args に "\" の付かない "|" を
// 書くと誤りにする（書けない名前のコマンドを "|" の後ろで呼ぶ抜け道を塞ぐため）。"|" を引数の一部として読むコマンド（:option.set）と、
// 拡張機能のコマンド（引数は行末まで）は、コマンドラインと同じく "|" を引数として渡す。
// args のそれ以外の中身は確かめない。誤りは、実行したときにコマンドラインで打ったときと同じ文言で出る。
//
// req: FR-154
func KeymapCommandLine(t *Aliases, name, args string, extCommands []string) (string, error) {
	if name == "" {
		return "", errors.New("コマンドの名前が空である")
	}
	if extCommandName(name) != name {
		return "", fmt.Errorf("コマンドの名前 %q に空白、|、! を書けない", name)
	}
	cmd := lookup(name)
	switch {
	case cmd != nil && cmd.name == name && strings.Contains(name, "."):
		// 1 つのコマンドだけを実行する。"|" で次のコマンドへ続けると、書けない名前のコマンドを "|" の後ろで呼べてしまう。
		// "|" を引数の一部として読むコマンド（barNone の :option.set、barSelf）と、"\|" は、コマンドラインと同じく引数にする。
		if _, _, more := splitBar(args); cmd.bar == barSplit && more {
			return "", fmt.Errorf("args に、コマンドの区切りになる | を書けない（1 つのコマンドだけを実行する。| そのものは \\| と書く）: %s", args)
		}
	case cmd != nil:
		// 点を含む正規の名前は省略できないので、ここに来るのは正規の名前の無いコマンドの名前か省略形である。
		return "", fmt.Errorf("%s は正規の名前の無い Ex コマンド（:%s）なので書けない（キーの列 \":%s<CR>\" で割り当てる）", name, cmd.name, name)
	case t.lookup(name) != nil:
		a := t.lookup(name)
		if a.target != "" {
			return "", fmt.Errorf("%s は別名なので書けない（正規の名前 %s を書く）", name, a.target)
		}
		return "", fmt.Errorf("%s は別名なので書けない（正規の名前のコマンドを書く）", name)
	case slices.Contains(extCommands, name):
	default:
		return "", fmt.Errorf("%s は、本体の Ex コマンドの正規の名前でも拡張機能のコマンドの ID でもない", name)
	}
	if args == "" {
		return name, nil
	}
	return name + " " + args, nil
}
