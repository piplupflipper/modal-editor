<!-- AI エージェント向けの文書（vi-imitation --docs commands）。印の間は make docs が生成する。印の間を手で編集しない。 -->
# Ex コマンド

`:` の後に打つコマンドの名前の一覧である。
範囲（`:3,5d`、`%`、`'<,'>`）の書き方は Vim と同じで、パターンは Go の `regexp` の文法で書く。
`|` で続けて書ける（`:g`、`:!`、`:option.set` などは `|` を引数に含める）。
各コマンドの動きは `vi-imitation --docs usage Ex コマンドの一覧` にある。

## 本体の Ex コマンド

省略形は、名前の先頭からこの長さまで縮められることを表す（`:substitute` は `:s` から `:substitute` まで、どの長さでも打てる）。
点を含む名前（`:option.set` など）は省略できない。
`:set` と `:nohlsearch` は別名で、`:set` は `:option.set`、`:option.get`、`:option.toggle` に、`:nohlsearch` は `:search.clearHighlight` に変換して実行する。

<!-- generated:ex-commands:begin -->
| コマンド | 最短の省略形 |
|---|---|
| `:write` | `:w` |
| `:wq` | 省略できない |
| `:xit` | `:x` |
| `:quit` | `:q` |
| `:edit` | `:e` |
| `:substitute` | `:s` |
| `:global` | `:g` |
| `:vglobal` | `:v` |
| `:delete` | `:d` |
| `:output` | `:out` |
| `:outclose` | `:outc` |
| `:checktime` | `:checkt` |
| `:buffer` | `:b` |
| `:bnext` | `:bn` |
| `:bNext` | `:bN` |
| `:bprevious` | `:bp` |
| `:buffers` | 省略できない |
| `:ls` | 省略できない |
| `:jumps` | `:ju` |
| `:read` | `:r` |
| `:registers` | `:reg` |
| `:readonly` | `:reado` |
| `:wall` | `:wa` |
| `:qall` | `:qa` |
| `:wqall` | `:wqa` |
| `:xall` | `:xa` |
| `:bdelete` | `:bd` |
| `:tabmove` | `:tabm` |
| `:tab.new` | 省略できない |
| `:popup` | `:popu` |
| `:tabopen` | `:tabop` |
| `:diffsplit` | `:diffs` |
| `:diffthis` | `:difft` |
| `:diffoff` | `:diffo` |
| `:diffupdate` | `:diffu` |
| `:option.set` | 省略できない |
| `:option.get` | 省略できない |
| `:option.toggle` | 省略できない |
| `:search.clearHighlight` | 省略できない |
| `:terminal.popup` | 省略できない |
| `:set` | `:se` |
| `:nohlsearch` | `:noh` |
<!-- generated:ex-commands:end -->

次のコマンドは、上の表と別の口から呼ぶ。
表に並べた名前でだけ呼べ、途中までの省略（`:he`）はできない。

<!-- generated:host-commands:begin -->
| コマンド | ほかに打てる名前 |
|---|---|
| `:ext` |  |
| `:help` | `:h` |
| `:keys` |  |
| `:messages` | `:mes`、`:mess`、`:messa`、`:messag`、`:message` |
<!-- generated:host-commands:end -->

範囲と `!` のほかに、`:{行番号}`（その行へ移る）、`:{範囲}!コマンド`（範囲の行を外部のコマンドの出力で置き換える）も打てる。

## LSP と DAP の操作

次の名前は、その操作を引き受ける拡張機能（宣言ファイルの `provides`）を読み込んだときだけ、Ex コマンドとして呼べる（`:rename 新しい名前`）。
同梱の LSP と DAP の拡張機能が引き受ける。

<!-- generated:provider-commands:begin -->
`:definition`、`:implementation`、`:typeDefinition`、`:hover`、`:references`、`:documentSymbols`、`:workspaceSymbols`、`:format`、`:rename`、`:codeAction`、`:completion`、`:breakpoint.toggle`、`:breakpoint.conditional`、`:breakpoint.clear`、`:debug.continue`、`:debug.pause`、`:debug.terminate`、`:debug.restart`、`:debug.runLast`、`:debug.stepOver`、`:debug.stepIn`、`:debug.stepOut`、`:debug.toggleUI`
<!-- generated:provider-commands:end -->

## 拡張機能のコマンド

拡張機能が宣言ファイルの `contributes.commands` に書いたコマンドは、ID をそのまま名前にして呼ぶ（`:finder.files`、`:acp.prompt 説明して`）。
ID の後の空白の後から行末までが引数になる。
読み込んだ拡張機能のコマンドの一覧は、エディタの中で `:help 読み込んだ拡張機能の操作` で見る。
本体の Ex コマンドと同じ名前のコマンドは、本体のコマンドが勝つ。

## キーと別名から呼ぶ

`keymap` の右辺に `{ "command": "<名前>", "args": "<引数>" }` と書くと、キーで Ex コマンドを実行する（`vi-imitation --docs keymap`）。
`command` には点を含む正規の名前、拡張機能のコマンドの ID、LSP と DAP の操作の名前を書く。

設定ファイルの `commandAliases` に、名前を付け替えるだけの別名を書ける。

```jsonc
{
  "commandAliases": {
    // :tabe ファイル は :tab.new ファイル と同じ（4 文字の :tabe まで縮められる）
    "tabedit": { "command": "tab.new", "min": 4 },
    // min を省くと省略できない
    "tabnew": { "command": "tab.new" },
    // 拡張機能のコマンドにも付けられる
    "ff": { "command": "finder.files", "min": 1 },
  },
}
```

キーは英字だけの名前、`command` は付け替える先の正規の名前か拡張機能のコマンドの ID、`min` は縮められる最短の長さである。
名前か省略形が、本体の Ex コマンド、別名、拡張機能のコマンドと重なると、設定の誤りになる（`vi-imitation --docs usage Ex コマンドの別名を足す`）。
