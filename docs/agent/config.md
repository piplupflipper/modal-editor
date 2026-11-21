<!-- AI エージェント向けの文書（vi-imitation --docs config）。印の間は make docs が生成する。印の間を手で編集しない。 -->
# 設定ファイル

設定ファイルは `$XDG_CONFIG_HOME/vi-imitation/config.jsonc` に置く。
`XDG_CONFIG_HOME` が空なら `~/.config/vi-imitation/config.jsonc` である。
JSONC（コメントと末尾のカンマを許す JSON）で書く。
ファイルが無ければ、すべての項目が既定値になる。
設定を変えたら、本体を起動し直す。

誤りのある項目は、起動後にメッセージの行へ `config.jsonc:2:19: fixendofline は true か false にする` の形で出し、その項目だけを使わない。
誤りが複数あれば、行には最初の 1 件と件数を出す。
`:messages` で全部を読める。
知らない項目も誤りにする。

配布物の `config.example.jsonc` は、キーの割り当て、配色、ステータス行、字下げの例である。

## 例

```jsonc
{
  // 設定の項目は、点で区切った名前を入れ子のキーにして書く（lineNumber.show）
  "lineNumber": { "show": true },
  "search": { "highlight": true, "showCount": true },
  "indent": { "useSpaces": true, "width": 4, "auto": true },
  "tab": { "width": 4 },
  // ファイルの種類ごとの値。キーは filetype の値で、中は最上位と同じ形で書く。範囲がバッファごとの項目だけを書ける
  "fileTypeOptions": {
    "go": { "indent": { "useSpaces": false, "width": 0 } },
  },
  // 拡張子かファイル名から、ファイルの種類への対応
  "fileTypes": { ".tmpl": "html" },
  "leader": "<Space>",
  "keymap": {
    "normal": {
      "<Leader>w": ":w<CR>",
      "<Leader>n": { "command": "option.toggle", "args": "lineNumber.show" },
    },
    "insert": {
      "<C-a>": { "action": "insert.line-start" },
    },
  },
  // 拡張機能の ID ごとの設定。中身は拡張機能ごとに違う
  "extensions": {
    "settings": {
      "acp": { "agent": { "command": "opencode", "args": ["acp"] } },
    },
  },
}
```

`keymap` の書き方は `vi-imitation --docs keymap` にある。
`extensions` の書き方は `vi-imitation --docs extensions` と、標準の拡張機能ごとの節（`vi-imitation --docs usage 標準の拡張機能`）にある。

## 最上位の項目

設定の項目（下の表）のほかに、最上位に次の項目を書ける。

<!-- generated:config-keys:begin -->
`keymap`、`leader`、`fixendofline`、`autoread`、`checkIntervalMs`、`fileTypes`、`extensions`、`registers`、`readonlyPaths`、`colors`、`colorscheme`、`symbols`、`icons`、`statusline`、`tabline`、`elevateCommand`、`commandAliases`、`fileTypeOptions`
<!-- generated:config-keys:end -->

それぞれの値の形と既定値は `vi-imitation --docs usage 設定ファイルの項目` にある。
見た目の項目（`colorscheme`、`colors`、`symbols`、`icons`、`statusline`、`tabline`）は `vi-imitation --docs usage 見た目の設定` に、`registers` は `vi-imitation --docs usage レジスターと履歴` に、`readonlyPaths` は `vi-imitation --docs usage 編集の禁止` に、`commandAliases` は `vi-imitation --docs commands` にある。

## 設定の項目

設定の項目は、名前を点で区切った入れ子のキーにして書く（`lineNumber.show` は `"lineNumber": { "show": true }`）。
範囲が「エディタ全体」の項目は、エディタの初めの値になる。
範囲が「バッファごと」の項目は、バッファを開くときの初めの値になり、`fileTypeOptions` でファイルの種類ごとにも書ける。
バッファごとの値は、本体の既定、最上位の値、本体のファイルの種類ごとの既定（`go` と `make` はタブ文字で字下げする）、`fileTypeOptions` の値、`.editorconfig` の値の順に当て、後の値が勝つ（`vi-imitation --docs usage 値の決まり方`）。

実行中は、`:option.set 名前 値`、`:option.get 名前`、`:option.toggle 名前`（真偽の項目を反転する）で変える、読む。
Vim の `:set` の書き方の一部も使える（`:set number`、`:set et sw=4`）。

<!-- generated:options:begin -->
| 名前 | 型 | 範囲 | 既定値 | 取れる値と注意 |
|---|---|---|---|---|
| `filetype` | 文字列 | バッファごと | `""` | 設定ファイルに書けない（開いたファイルから判定する） |
| `lineNumber.show` | 真偽 | エディタ全体 | `false` |  |
| `lineNumber.minWidth` | 整数 | エディタ全体 | `4` | 1 から 20 |
| `lineWrap.enabled` | 真偽 | エディタ全体 | `false` |  |
| `search.highlight` | 真偽 | エディタ全体 | `false` |  |
| `search.incrementalTimeoutMs` | 整数 | エディタ全体 | `500` | 1 から 10000 |
| `search.showCount` | 真偽 | エディタ全体 | `false` |  |
| `yank.highlightMs` | 整数 | エディタ全体 | `0` | 0 から 10000 |
| `preview.minWindowWidth` | 整数 | エディタ全体 | `100` | 0 から 10000 |
| `preview.widthPercent` | 整数 | エディタ全体 | `60` | 1 から 99 |
| `preview.windowWidthPercent` | 整数 | エディタ全体 | `90` | 10 から 100 |
| `icons.show` | 真偽 | エディタ全体 | `true` |  |
| `indent.useSpaces` | 真偽 | バッファごと | `false` |  |
| `indent.width` | 整数 | バッファごと | `8` | 0 から 9999 |
| `tab.width` | 整数 | バッファごと | `8` | 1 から 9999 |
| `indent.auto` | 真偽 | バッファごと | `false` |  |
| `editorConfig.enabled` | 真偽 | エディタ全体 | `true` |  |
<!-- generated:options:end -->

各項目の説明と `:set` で使える Vim の名前は `vi-imitation --docs usage 設定の名前の一覧` にある。
