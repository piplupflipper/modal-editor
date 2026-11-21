<!-- AI エージェント向けの文書（vi-imitation --docs commands）。印の間は make docs が生成する。印の間を手で編集しない。 -->
# Ex コマンド

`:` の後に打つコマンドの名前の一覧である。
各コマンドの動き、範囲とパターンの書き方は `vi-imitation --docs usage Ex コマンドの一覧` にある。

## 本体の Ex コマンド

「最短の省略形」は、名前の先頭からこの長さまで縮められることを表す（`:substitute` は `:s` から `:substitute` まで、どの長さでも打てる）。

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

次のコマンドは、表に並べた名前でだけ呼べる（`:he` のような途中までの省略はできない）。

<!-- generated:host-commands:begin -->
| コマンド | ほかに打てる名前 |
|---|---|
| `:ext` |  |
| `:help` | `:h` |
| `:keys` |  |
| `:messages` | `:mes`、`:mess`、`:messa`、`:messag`、`:message` |
<!-- generated:host-commands:end -->

## LSP と DAP の操作

次の名前は、その操作を引き受ける拡張機能（宣言ファイルの `provides`）を読み込んだときだけ、Ex コマンドとして呼べる（`:rename 新しい名前`）。

<!-- generated:provider-commands:begin -->
`:definition`、`:implementation`、`:typeDefinition`、`:hover`、`:references`、`:documentSymbols`、`:workspaceSymbols`、`:format`、`:rename`、`:codeAction`、`:completion`、`:breakpoint.toggle`、`:breakpoint.conditional`、`:breakpoint.clear`、`:debug.continue`、`:debug.pause`、`:debug.terminate`、`:debug.restart`、`:debug.runLast`、`:debug.stepOver`、`:debug.stepIn`、`:debug.stepOut`、`:debug.toggleUI`
<!-- generated:provider-commands:end -->

拡張機能のコマンドの呼び方は `vi-imitation --docs usage 拡張機能のコマンドを呼ぶ` にある。

## キーと別名から呼ぶ

```jsonc
{
  "keymap": {
    "normal": {
      // 点を含む正規の名前、拡張機能のコマンドの ID、LSP と DAP の操作の名前を書く
      "<C-l>": { "command": "search.clearHighlight" },
      "<Leader>f": { "command": "finder.files" },
    },
  },
  "commandAliases": {
    // :tabe ファイル は :tab.new ファイル と同じ（4 文字の :tabe まで縮められる）
    "tabedit": { "command": "tab.new", "min": 4 },
    // min を省くと省略できない
    "tabnew": { "command": "tab.new" },
    "ff": { "command": "finder.files", "min": 1 },
  },
}
```

`keymap` の `command` の規則は `vi-imitation --docs usage keymap から Ex コマンドを呼ぶ` に、`commandAliases` の規則は `vi-imitation --docs usage Ex コマンドの別名を足す` にある。
