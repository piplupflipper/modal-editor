# 設定と標準の拡張機能の使い方

設定ファイルの書き方と、配布物に入っている標準の拡張機能の使い方を書く。
本体の起動と基本の操作は [README](../README.md) の「使い方」にある。

この文書の手順は、2026-11-16 に `make dist` で作った配布物の本体を tmux 3.4 の中で起動し、上から順にたどって確かめた。
外部のプログラムは、gopls v0.24.0、Delve 1.27.2、opencode 1.19.4（`opencode acp`）、Claude Code 2.1.325 を使った。
設定の項目の名前とコマンドの名前は、どれも仮である（基本設計 7 章、6.7）。

## 配布物の本体を起動する

標準の拡張機能は、`make dist` が作る配布物の本体からだけ使える。
`make build` の `bin/vi-imitation` は宣言ファイルを読まないので、標準の拡張機能が動かない（README の「使い方」）。

```sh
make dist
dist/vi-imitation-<コミット>-<OS>-<アーキテクチャ>/vi-imitation main.go
```

配布物のディレクトリは、本体、MCP の中継コマンド（`vi-imitation-mcp-bridge`）、スクリプトの拡張機能を動かす実行ファイル（`vi-imitation-ext-script`）と、標準の拡張機能ごとのディレクトリ（`extensions/<ID>/`）と、herdr の同梱のプラグイン（`herdr-plugin/`。[herdr のプラグインを登録する](#herdr-のプラグインを登録する)）からなる。
本体は、自分の実行ファイルの隣の `extensions/` から宣言ファイルを探す（`internal/config` の `ExtensionDirs`）。

本体と、tree-sitter 以外の標準の拡張機能は静的にリンクした実行ファイルで、C の標準ライブラリの種類と版に依らずに動く。
tree-sitter の拡張機能（`extensions/treesitter/`）だけは glibc に動的にリンクしている（要件定義の NFR-01）。
2026-11-20 に Ubuntu の glibc 2.43 の上で作った配布物では、この拡張機能が要求する glibc のシンボルの版は `GLIBC_2.34` までだった（`objdump -T`）。
古い glibc（AlmaLinux 8 の 2.28 など）と musl（Alpine など）の環境では、この拡張機能が起動せず、構文の色付けと tree-sitter のテキストオブジェクトを使えない。本体とほかの拡張機能は動く。

## 設定ファイル

設定ファイルは `$XDG_CONFIG_HOME/vi-imitation/config.jsonc` に置く。
`XDG_CONFIG_HOME` が空なら `~/.config/vi-imitation/config.jsonc` である。
ファイルは JSONC（コメントと末尾のカンマを許す JSON）で書く。
ファイルが無ければ、すべての項目が既定値になる。
設定を変えたら、本体を起動し直す。

次の例は、F キーの代わりに使うキーを LSP と DAP の操作に割り当て、ACP のエージェントに opencode を使う設定である。
LSP と DAP の操作には既定のキー（[LSP と DAP の既定のキー](#lsp-と-dap-の既定のキー)）があり、この例のキーは足したものである。

```jsonc
{
  // 拡張機能のコマンドと LSP と DAP の操作は、Ex コマンドと同じく : で呼べる
  "keymap": {
    "normal": {
      "<Space>h": ":hover<CR>",
      "<Space>c": ":debug.continue<CR>",
      // 右辺をオブジェクトにすると、キーの列ではなく本体の操作を ID で割り当てる
      "<Space>H": { "action": "hover" },
    },
    "insert": {
      "<C-a>": { "action": "insert.line-start" },
      "<C-e>": { "action": "insert.line-end" },
    },
  },
  "extensions": {
    "settings": {
      "acp": {
        "agent": { "command": "opencode", "args": ["acp"] },
      },
    },
  },
}
```

`keymap` は、モードの名前から、キーの列とその割り当ての対応である。
モードの名前は `normal`、`visual`、`insert`、`commandLine` で、この文書が確かめたのは `normal` と `insert` である（`insert` は 2026-11-19 に同梱の `config.example.jsonc` の Emacs 風のキーで確かめた）。
右辺には、キーの列か、本体の操作（[操作の ID](#操作の-id)）を書く。
キーの列は、Vim の `:map` の右辺と同じく展開先のキーの列である。
展開した列は、もう一度 `keymap` の文字列の項目で展開しない（Vim の `noremap` と同じ）。
ただし、展開した列は、`keymap` で操作を割り当てた後のキーとして解釈する。`"x": { "action": "line-end" }` と `"<C-x>": "x"` を書くと、`<C-x>` は行末へ移る（Vim の `nnoremap <C-x> x` は本体の `x` を使うので、この点が違う）。
`":hover<CR>"` は、`:` でコマンドラインに入り、`hover` を打って `<CR>` で実行するキーの列である。
`{ "action": "hover" }` は、ホバーの操作そのものを割り当てる（FR-126）。

Ex コマンドの別名（`commandAliases`）の書き方は、[Ex コマンドの別名を足す](#ex-コマンドの別名を足す)にある。

### 操作の ID

`keymap` の右辺に `{ "action": "<ID>" }` と書くと、キーの列に展開せずに、本体の操作をそのキーに割り当てる（FR-126）。
同じモードの同じキーに本体の既定の割り当てがあれば、設定の割り当てが勝つ（`"insert": { "<BS>": { "action": "insert.delete-char" } }` と書くと、`<BS>` はカーソルの文字を消す）。
書ける ID は、下の[操作の ID の一覧](#操作の-id-の一覧)にあるものだけである。
拡張機能が宣言したコマンドの ID は書けない。拡張機能のコマンドは、キーの列（`":acp.prompt<CR>"`）で割り当てる。
オブジェクトに書ける項目は `action` だけで、他の項目を書くと誤りになる。

ノーマルモードの既定の `Y` は行末までのヤンク（`yank-to-line-end`。`y$`）、`U` はリドゥ（`redo`。`<C-r>` と同じ）で、Vim と違う（要件定義 FR-145、FR-146）。
Vim と同じく `Y` で行をヤンクするには、`"normal": { "Y": { "action": "yank-line" } }` と書く。
ビジュアルモードの `U`（選択を大文字にする）と `Y`（選んだ行をヤンクする）は Vim と同じである。

ID は、名前を変えずに保つ（利用者の設定が壊れるため）。
回数、レジスター、マクロ、ドットリピート（`3`、`"a`、`q`、`@`、`.`）には ID が無く、割り当てられない。

割り当てられるモードは、一覧の「モード」の列にあるものである。
`commandLine` には操作を書けない。
`normal` に割り当てたモーションは、オペレーターの後（`d` の後など）には効かない（Vim の `nmap` と同じ）。

インサートモードの割り当ての左辺は、1 つのキーにする。
修飾キーの付かない文字、`<Esc>`、`<CR>`、`<Tab>` は左辺に書けない。
これらのキーは、1 文字の入力を速く保つために操作の表を通さず、今までどおり文字の入力とモードの切り替えに使う。
`jk` で抜けるようなキーの列は、右辺をキーの列で書く（`"insert": { "jk": "<Esc>" }`）。
同梱の `config.example.jsonc` には、インサートモードの Emacs 風のキー（`<C-a>`、`<C-e>`、`<C-f>`、`<C-b>`、`<C-n>`、`<C-p>`、`<C-d>`、`<C-k>`、`<C-w>`、`<C-u>`）の例がある。
`<C-w>` はノーマルモードではウィンドウの操作の前置キーだが、`keymap.insert` の割り当てはインサートモードでだけ効くので、ノーマルモードの `<C-w>` は変わらない。
Ctrl-H は、端末からも tmux の中でも `^H`（0x08）で届き、本体は `<BS>` と読む（本体は、Ctrl-H を区別して送る端末の機能を有効にしない）。
このため `<C-h>` に操作を割り当てても効かず、Ctrl-H は `<BS>` の割り当てのとおりに働く。既定では `insert.delete-prev-char` で前の文字を消す。
`<BS>` を別の操作に割り当てると（`"<BS>": { "action": "insert.delete-char" }`）、Ctrl-H も同じ操作に変わる。
tmux の prefix が既定の `C-b` のままなら、Ctrl-B は tmux が受け取るので、例の `<C-b>`（`insert.left`）は本体に届かない。

左辺の表記が違っても同じキーになる項目（`<Leader>w` と `<Space>w`）を、キーの列と操作の両方に書くと、キーの列の項目を使い、誤りとして知らせる。
知らない ID、そのモードで使えない操作、書けない左辺も、誤りとして位置と内容を知らせ、その項目だけを使わない。

### 操作の ID の一覧

次の表は、本体の操作の表から作る（`internal/input` のテストが照合する）。
ID は一意で、1 つの ID は 1 つの操作を指す（`0` と `<Home>` のように、同じ操作を複数のキーに載せているものはある）。
「モード」は、`keymap` のどのモードに割り当てられるかである。

ID の書き方は、今は次のとおりである。

- 本体の操作は、小文字の英単語を `-` でつなぐ（`line-start`、`page-down`）
- LSP と DAP の操作は、Ex コマンドの名前と同じ（`typeDefinition`、`debug.stepOver`）。語のつなぎは `-` ではなく大文字（camelCase）である
- `.` は、前の部分で分類を表す。`peek.`（ポップアップで見る）、`diagnostic.`（診断）、`debug.` と `breakpoint.`（DAP）、`insert.`（インサートモードの操作）がある
- `insert.` で始まる ID はインサートモードの操作である。ID `insert`（`.` が無いもの）は、ノーマルモードの `i`（カーソルの前から入力を始める）で、インサートモードの操作ではない
- ノーマルモードの `~`（カーソルの文字を入れ替える）は `toggle-case-char`、オペレーターの `g~` は `toggle-case` である
インサートモードの操作（`insert.` で始まるもの）の既定のキーは、一覧の順に `<Left>`、`<Right>`、`<Up>`、`<Down>`、`<Home>`、`<End>`、`<PageUp>`、`<PageDown>`、`<BS>`、`<Del>` である。
`insert.delete-to-line-end` は既定のキーが無く、行末の位置で使うと次の行をつなげる（Emacs の `C-k`）。
`insert.delete-prev-word` と `insert.delete-to-line-start` も既定のキーが無い（同梱の `config.example.jsonc` では `<C-w>` と `<C-u>` に割り当てている）。
`insert.delete-prev-word` は、ノーマルモードの `db` と同じ単語の区切りで、カーソルの前の空白とその前の単語を消す。
行の先頭で使うと `<BS>` と同じく前の行とつなげ、それ以外では行をまたいで消さない。
`insert.delete-to-line-start` は、カーソルから行の先頭までを、字下げも含めて消す。行の先頭では何もしない。
どちらも、このインサートで入力した文字より前の文字も消す（Vim の `<C-w>` と `<C-u>` との違いは [vim-differences.md](vim-differences.md) にある）。
インサートモードで消した文字は、`<BS>` と同じくレジスターにも履歴にも入らない。

`select-all` は、文字単位のビジュアルモードでバッファの全体（最初の行の最初の文字から、最後の行の最後の文字まで）を選ぶ。
既定のキーは無い。次のように割り当てる。

```jsonc
{
  "keymap": {
    "normal": {
      "<Leader>a": { "action": "select-all" },
    },
  },
}
```

Vim の `ggVG` と違い、カーソルは動かず、画面もスクロールせず、ジャンプリストにも記録しない（Windows の Ctrl+A に近い）。
続けて `y` を打つと全体をヤンクし、カーソルは `<Leader>a` を打った位置のままである（ビジュアルモードの `y` はカーソルを動かさない。[vim-differences.md](vim-differences.md)）。
ヤンクは履歴の先頭に積むので、`clipboard` の設定（既定は `osc52`）のとおりにホストのクリップボードへも送る（[ホストのクリップボードへの送り出し](#ホストのクリップボードへの送り出し)）。
最後の行の改行は選ばないので、`y` で取ったテキストの最後に改行は付かない（Vim の `ggvG$y` と同じ）。
その後のキーは、普段のビジュアルモードと同じ規則で働く。

- `d`、`x`、`c` は全体を消す（`c` はその後インサートモードに入る）
- `>`、`<` は全体をずらし、選択とカーソルの位置を保つ。選択の両端は、ずらす前の行とバイトの位置に戻る（Vim の `>gv` と同じ）。このため `>` を重ねると、2 回目から字下げ 1 段ごとに最後の行の最後の文字が 1 文字ずつ選択から外れる（字下げはすべての行に入る）
- `j`、`w` などのモーションは、カーソルから動く。選択は 1 行目の最初の文字から動いた先までになる
- `o` は、カーソルを 1 行目の最初の文字へ移す
- `<Esc>` はノーマルモードへ戻る。カーソルは元の位置のままである
- `:` は、`'<,'>` がバッファの全体を指す

上の例は `keymap.normal` にだけ割り当てているので、ビジュアルモードの中の `<Leader>a` は `select-all` にならない。
ビジュアルモードの中でも使うには、`keymap.visual` にも同じ行を書く。
`keymap.visual` に割り当てると、行単位のビジュアルモードの中で使ったときは、文字単位のビジュアルモードに切り替えて全体を選ぶ。

`select-all` の後に拡張機能がバッファを書き換えたとき（MCP の `edit_buffer`、ACP の書き込みなど）は、選択の両端がそれぞれ編集に合わせてずれる。
バッファの先頭に足した行は選択の外になり、途中に足した行は選択の中になる。

<!-- action-ids:begin -->
| ID | 種類 | モード | 説明 |
|---|---|---|---|
| `left` | モーション | `normal`、`visual` | 左へ |
| `down` | モーション | `normal`、`visual` | 下の行へ |
| `up` | モーション | `normal`、`visual` | 上の行へ |
| `right` | モーション | `normal`、`visual` | 右へ |
| `word-next` | モーション | `normal`、`visual` | 次の単語の先頭へ |
| `word-prev` | モーション | `normal`、`visual` | 前の単語の先頭へ |
| `word-end` | モーション | `normal`、`visual` | 単語の末尾へ |
| `bigword-next` | モーション | `normal`、`visual` | 次の空白区切りの単語の先頭へ |
| `bigword-prev` | モーション | `normal`、`visual` | 前の空白区切りの単語の先頭へ |
| `bigword-end` | モーション | `normal`、`visual` | 空白区切りの単語の末尾へ |
| `line-start` | モーション | `normal`、`visual` | 行の先頭へ |
| `line-first-nonblank` | モーション | `normal`、`visual` | 行の最初の空白でない文字へ |
| `line-end` | モーション | `normal`、`visual` | 行の末尾へ |
| `file-start` | モーション | `normal`、`visual` | 最初の行へ（回数があればその行へ） |
| `file-end` | モーション | `normal`、`visual` | 最後の行へ（回数があればその行へ） |
| `find-forward` | モーション | `normal`、`visual` | 行の中で右の {文字} へ |
| `find-backward` | モーション | `normal`、`visual` | 行の中で左の {文字} へ |
| `till-forward` | モーション | `normal`、`visual` | 行の中で右の {文字} の手前へ |
| `till-backward` | モーション | `normal`、`visual` | 行の中で左の {文字} の手前へ |
| `find-repeat` | モーション | `normal`、`visual` | 直前の f、F、t、T を繰り返す |
| `find-repeat-reverse` | モーション | `normal`、`visual` | 直前の f、F、t、T を逆向きに繰り返す |
| `match-pair` | モーション | `normal`、`visual` | 対応する括弧へ |
| `paragraph-prev` | モーション | `normal`、`visual` | 前の段落へ |
| `paragraph-next` | モーション | `normal`、`visual` | 次の段落へ |
| `search-forward` | モーション | `normal`、`visual` | 前方へ検索する |
| `search-backward` | モーション | `normal`、`visual` | 後方へ検索する |
| `search-next` | モーション | `normal`、`visual` | 直前の検索を同じ向きに繰り返す |
| `search-prev` | モーション | `normal`、`visual` | 直前の検索を逆向きに繰り返す |
| `delete` | オペレーター | `normal`、`visual` | 消す |
| `change` | オペレーター | `normal`、`visual` | 消して入力する |
| `yank` | オペレーター | `normal`、`visual` | ヤンクする |
| `toggle-case` | オペレーター | `normal`、`visual` | 大文字と小文字を入れ替える |
| `lowercase` | オペレーター | `normal`、`visual` | 小文字にする |
| `uppercase` | オペレーター | `normal`、`visual` | 大文字にする |
| `surround-add` | オペレーター | `normal` | 対象を {文字} で囲む（sa{対象}{文字}） |
| `indent` | オペレーター | `visual` | 選んだ行を右へずらす（選択は残す） |
| `dedent` | オペレーター | `visual` | 選んだ行を左へずらす（選択は残す） |
| `delete-lines` | オペレーター | `visual` | 選んだ行を消す |
| `change-lines` | オペレーター | `visual` | 選んだ行を消して入力する |
| `yank-lines` | オペレーター | `visual` | 選んだ行をヤンクする |
| `inner-word` | テキストオブジェクト | `visual` | 単語 |
| `a-word` | テキストオブジェクト | `visual` | 単語と後ろの空白 |
| `inner-bigword` | テキストオブジェクト | `visual` | 空白区切りの単語 |
| `a-bigword` | テキストオブジェクト | `visual` | 空白区切りの単語と後ろの空白 |
| `inner-double-quote` | テキストオブジェクト | `visual` | "…" の中 |
| `a-double-quote` | テキストオブジェクト | `visual` | "…" の全体 |
| `inner-single-quote` | テキストオブジェクト | `visual` | '…' の中 |
| `a-single-quote` | テキストオブジェクト | `visual` | '…' の全体 |
| `inner-paren` | テキストオブジェクト | `visual` | (…) の中 |
| `a-paren` | テキストオブジェクト | `visual` | (…) の全体 |
| `inner-bracket` | テキストオブジェクト | `visual` | […] の中 |
| `a-bracket` | テキストオブジェクト | `visual` | […] の全体 |
| `inner-brace` | テキストオブジェクト | `visual` | {…} の中 |
| `a-brace` | テキストオブジェクト | `visual` | {…} の全体 |
| `inner-paragraph` | テキストオブジェクト | `visual` | 段落 |
| `a-paragraph` | テキストオブジェクト | `visual` | 段落と後ろの空行 |
| `toggle-case-char` | 操作 | `normal` | カーソルの文字の大文字と小文字を入れ替える |
| `put-after` | 操作 | `normal` | カーソルの後に貼る |
| `put-before` | 操作 | `normal` | カーソルの前に貼る |
| `delete-char` | 操作 | `normal` | カーソルの文字を消す |
| `delete-char-before` | 操作 | `normal` | カーソルの前の文字を消す |
| `replace-char` | 操作 | `normal` | カーソルの文字を {文字} に置き換える |
| `delete-to-line-end` | 操作 | `normal` | 行の末尾まで消す（d$） |
| `change-to-line-end` | 操作 | `normal` | 行の末尾まで消して入力する（c$） |
| `substitute-line` | 操作 | `normal` | 行を消して入力する（cc） |
| `yank-to-line-end` | 操作 | `normal` | 行の末尾までヤンクする（y$） |
| `yank-line` | 操作 | `normal` | 行をヤンクする（yy） |
| `indent-line` | 操作 | `normal` | 行を右へずらす（>>。回数は行数） |
| `dedent-line` | 操作 | `normal` | 行を左へずらす（<<。回数は行数） |
| `surround-delete` | 操作 | `normal` | {文字} の囲みを消す（sd{文字}） |
| `surround-replace` | 操作 | `normal` | 囲みを替える（sr{前の文字}{後の文字}） |
| `join-lines` | 操作 | `normal` | 行をつなげる |
| `open-below` | 操作 | `normal` | 下に行を足して入力する |
| `open-above` | 操作 | `normal` | 上に行を足して入力する |
| `insert` | 操作 | `normal` | カーソルの前から入力する |
| `append` | 操作 | `normal` | カーソルの後から入力する |
| `insert-line-start` | 操作 | `normal`、`visual` | 行の最初の空白でない文字の前から入力する（ビジュアルモードでは選んだ行ごと） |
| `append-line-end` | 操作 | `normal`、`visual` | 行の末尾から入力する（ビジュアルモードでは選んだ行ごと） |
| `undo` | 操作 | `normal` | アンドゥ |
| `redo` | 操作 | `normal` | リドゥ |
| `command-line` | 操作 | `normal`、`visual` | コマンドラインに入る |
| `visual` | 操作 | `normal`、`visual` | 文字単位のビジュアルモード |
| `visual-line` | 操作 | `normal`、`visual` | 行単位のビジュアルモード |
| `visual-swap` | 操作 | `visual` | 選択の反対の端へ移る |
| `select-all` | 操作 | `normal`、`visual` | カーソルを動かさずにバッファの全体を選ぶ |
| `page-down` | 操作 | `normal`、`visual` | 1 画面下へスクロールする |
| `page-up` | 操作 | `normal`、`visual` | 1 画面上へスクロールする |
| `output-next` | 操作 | `normal` | 出力の欄を開く、次の欄へ切り替える |
| `output-close` | 操作 | `normal` | 出力の欄を閉じる |
| `output-scroll-up` | 操作 | `normal` | 出力の欄を上へスクロールする |
| `output-scroll-down` | 操作 | `normal` | 出力の欄を下へスクロールする |
| `popup-focus` | 操作 | `normal` | ポップアップへフォーカスを入れる、元のタブへ戻る |
| `jump-older` | 操作 | `normal` | ジャンプリストを戻る |
| `jump-newer` | 操作 | `normal` | ジャンプリストを進む |
| `alternate-buffer` | 操作 | `normal` | 裏のタブへ移る |
| `tab-next` | 操作 | `normal` | 次のタブへ移る |
| `tab-prev` | 操作 | `normal` | 前のタブへ移る |
| `put-older` | 操作 | `normal` | 貼った直後に、履歴の古い項目に貼り直す |
| `put-newer` | 操作 | `normal` | 貼った直後に、履歴の新しい項目に貼り直す |
| `diff-next` | 操作 | `normal` | 見比べで、次の差分へ |
| `diff-prev` | 操作 | `normal` | 見比べで、前の差分へ |
| `search-word-forward` | 操作 | `normal`、`visual` | カーソルの単語を前方への検索のパターンにする（移らない） |
| `search-word-backward` | 操作 | `normal`、`visual` | カーソルの単語を後方への検索のパターンにする（移らない） |
| `insert.left` | 操作 | `insert` | 左へ |
| `insert.right` | 操作 | `insert` | 右へ |
| `insert.up` | 操作 | `insert` | 上の行へ |
| `insert.down` | 操作 | `insert` | 下の行へ |
| `insert.line-start` | 操作 | `insert` | 行の先頭へ |
| `insert.line-end` | 操作 | `insert` | 行末の位置へ |
| `insert.page-up` | 操作 | `insert` | 1 画面上へスクロールする |
| `insert.page-down` | 操作 | `insert` | 1 画面下へスクロールする |
| `insert.delete-prev-char` | 操作 | `insert` | 前の文字を消す |
| `insert.delete-char` | 操作 | `insert` | カーソルの文字を消す |
| `insert.delete-to-line-end` | 操作 | `insert` | カーソルから行末まで消す（行末では次の行をつなげる） |
| `insert.delete-prev-word` | 操作 | `insert` | 前の単語を消す（行の先頭では前の行とつなげる） |
| `insert.delete-to-line-start` | 操作 | `insert` | カーソルから行の先頭まで消す |
| `hover` | 操作 | `normal` | ホバー（LSP） |
| `peek.definition` | 操作 | `normal` | 定義をポップアップで見る（LSP） |
| `definition` | 操作 | `normal` | 定義へ移る（LSP） |
| `peek.implementation` | 操作 | `normal` | 実装をポップアップで見る（LSP） |
| `implementation` | 操作 | `normal` | 実装へ移る（LSP） |
| `peek.typeDefinition` | 操作 | `normal` | 型の定義をポップアップで見る（LSP） |
| `typeDefinition` | 操作 | `normal` | 型の定義へ移る（LSP） |
| `references` | 操作 | `normal` | 参照の一覧（LSP） |
| `rename` | 操作 | `normal` | 名前を変える（LSP） |
| `codeAction` | 操作 | `normal` | コードアクション（LSP） |
| `documentSymbols` | 操作 | `normal` | ファイルのシンボルの一覧（LSP） |
| `workspaceSymbols` | 操作 | `normal` | ワークスペースのシンボルの一覧（LSP） |
| `diagnostic.list` | 操作 | `normal` | ファイルの診断の一覧 |
| `diagnostic.listAll` | 操作 | `normal` | ワークスペースの診断の一覧 |
| `diagnostic.next` | 操作 | `normal` | 次の診断へ移る |
| `diagnostic.prev` | 操作 | `normal` | 前の診断へ移る |
| `format` | オペレーター | `normal`、`visual` | 整形する（LSP） |
| `debug.continue` | 操作 | `normal` | デバッグを始める、続行する（DAP） |
| `debug.pause` | 操作 | `normal` | 一時停止する（DAP） |
| `debug.terminate` | 操作 | `normal` | デバッグを終える（DAP） |
| `debug.restart` | 操作 | `normal` | デバッグをやり直す（DAP） |
| `breakpoint.toggle` | 操作 | `normal` | ブレークポイントを切り替える（DAP） |
| `breakpoint.conditional` | 操作 | `normal` | 条件付きのブレークポイントを置く（DAP） |
| `breakpoint.clear` | 操作 | `normal` | ブレークポイントをすべて消す（DAP） |
| `debug.stepOver` | 操作 | `normal` | ステップオーバー（DAP） |
| `debug.stepIn` | 操作 | `normal` | ステップイン（DAP） |
| `debug.stepOut` | 操作 | `normal` | ステップアウト（DAP） |
| `debug.runLast` | 操作 | `normal` | 前回の設定で実行する（DAP） |
| `debug.toggleUI` | 操作 | `normal` | DAP のペインを切り替える（DAP） |
<!-- action-ids:end -->

`extensions.settings` は、拡張機能の ID から、その拡張機能に渡す設定への対応である。
中身は拡張機能ごとに違い、それぞれの設定の型の doc にある（[標準の拡張機能](#標準の拡張機能)の各節）。

設定ファイルの他の項目（`fixendofline`、`fileTypes`、`extensions.syncTimeoutMs` など）は `go doc ./internal/config Config` にある。
設定に誤りがあると、起動後にコマンドラインの行へ位置と内容を出し、誤りのある項目だけを既定値にする。
誤りが複数あれば、行には最初の 1 件と件数を出す。
`:messages`（`:mes`）で、すべての誤りと、拡張機能の知らせ（言語サーバーの進み具合など）を古い順に読める。
行に出た誤りは、次のキーを打つまで、拡張機能の知らせで上書きされない。

### `<Leader>` と `leader`

`keymap` の左辺と右辺には、`<Leader>` と書ける（FR-97）。
`<Leader>` は、設定の `leader` に書いたキーの列に置き換わる。
`leader` の既定は `<Space>` なので、`"<Leader>d"` は `"<Space>d"` と同じである。
`<Leader>` の大文字と小文字は区別しない（`<leader>` でもよい）。

```jsonc
{
  // <Leader> を , にする。既定は "<Space>"
  "leader": ",",
  "keymap": {
    "normal": {
      "<Leader>w": ":w<CR>",
    },
  },
}
```

`leader` は `keymap` と同じ表記のキーの列で、`","`、`"<Space>"`、`"<C-k>"` のように書く。
次の値は誤りで、起動後に誤りの位置と内容を出し、既定の `<Space>` を使う。

- 空の値と、`<Leader>` を含む値
- 回数、レジスター、マクロ、ドットリピートとして先に読まれるキー（`1`〜`9`、`"`、`q`、`@`、`.`）で始まる値。`<Leader>` で始まるキーを割り当てられなくなるため
- `<Esc>` か `<CR>` で始まる値。モードを抜ける、コマンドラインを実行するキーが、leader の続きを待つようになるため

`<Leader>w`、`<leader>w`、`<Space>w`（leader が既定のとき）のように、表記は違っても同じキーの列になる左辺を同じモードに書くと、誤りとして知らせる。
効くのは、左辺の表記を文字コードの順に並べて最初の項目（この例では `<Leader>w`）で、残りは使わない。
拡張機能の宣言ファイル（`manifest.json`）の `contributes` の `keys` に書いた `<Leader>` も、同じく置き換わる。

`leader` を変えると、`<Leader>` を書いたキーの割り当てがすべて新しい `leader` で始まる。
設定のどこに `leader` を書いても結果は同じである。
Vim の `mapleader` は、マッピングを定義した時点の値で置き換えるので、この点が違う（[vim-differences.md](vim-differences.md)）。

## 見た目の設定

設定ファイルの `colorscheme`、`colors`、`symbols`、`statusline` で、配色、記号、ステータス行の並びを変えられる（要件定義 FR-91〜FR-93、FR-125、基本設計 7.1）。
同梱の配色 iceberg を選び、その上に `colors` を重ねる例を、リポジトリの直下と配布物に `config.example.jsonc` として同梱している。
例をそのまま使うときは、`config.example.jsonc` を設定ファイルの場所へ写す。

```jsonc
{
  "colorscheme": "iceberg",
  "colors": {
    "nontext": { "fg": "#444b71" },
    "diagnostic.error": { "sp": "#e27878", "undercurl": true },
    "statusline.mode": { "bold": true },
  },
  "symbols": { "readonly": "[読]", "sign.breakpoint": "●", "popup.border": "╭─╮│╯─╰│" },
  "statusline": {
    "left": ["mode", "name", "modified", "readonly"],
    "right": ["filetype", "ruler", "position"],
  },
}
```

設定に誤り（知らない名前、読めない色、使えない記号）があると、起動後にコマンドラインの行へ位置と内容を出し、その項目だけを使わない。

### 配色の選択

`colorscheme` は、配色の名前の文字列である。
選んだ配色は本体の既定の色を名前ごとに置き換え、`colors` に書いた名前はその上でさらに置き換わる。
`colorscheme` を書かなければ、本体の既定の色（端末の 16 色の番号）に `colors` を重ねるだけである。

配色は、次の順に探す。

1. 設定ファイルと同じディレクトリの `colors/<名前>.jsonc`（`$XDG_CONFIG_HOME/vi-imitation/colors/<名前>.jsonc`。未設定なら `~/.config/vi-imitation/colors/<名前>.jsonc`）。設定ファイルが symlink でも、symlink の先ではなく symlink を置いた側のディレクトリを探す
2. 本体に同梱した配色

同じ名前なら 1 のファイルを使うので、同梱の配色を同じ名前のファイルで置き換えられる。
同梱しているのは `iceberg`（[iceberg.vim](https://github.com/cocopon/iceberg.vim) の暗い背景の色）だけで、値と写した元は `internal/config/colors/iceberg.jsonc` にある。
同梱の iceberg は、iceberg.vim の配色そのもの（`colors/iceberg.vim`）の色だけを持つ。
`colors/iceberg.vim` に対応するハイライトグループが無い名前（`message.info`、`popup.border`、`statusline.` で始まる部品の色、`sign.executionPoint`、`sign.breakpoint`、`sign.breakpointUnverified`）は、同梱の iceberg に無いので本体の既定の色になる。
ステータス行は Vim の iceberg と同じく `StatusLine` の反転（明るい灰色の帯）になる。
`config.example.jsonc` は、iceberg.vim のリポジトリの lightline のテーマの色を `colors` に書き、ステータス行を lightline に近い見た目にしている。
色は 24 ビットの `#rrggbb` で書いてあるので、24 ビットの色を出せる端末で使う。

配色のファイルは、`colors` の値と同じ形（色を付ける対象の名前から属性のオブジェクトへの対応）を最上位に書いた JSONC である。

```jsonc
// ~/.config/vi-imitation/colors/mine.jsonc
{
  "keyword": { "fg": "#84a0c6" },
  "statusline": { "fg": "#c6c8d1", "bg": "#0f1117" },
}
```

名前に使えるのは英数字と `_` と `-` だけである。
見つからない名前と使えない名前は、起動後に `colorscheme` の位置の誤りとして知らせ、配色を使わずに起動する。
配色のファイルの中の誤り（知らない名前、読めない色）は、配色のファイルの位置で知らせ、その名前だけを使わない。
`:colorscheme` に当たるコマンドは無く、配色を変えたら起動し直す。

### 配色

`colors` は、色を付ける対象の名前から、属性のオブジェクトへの対応である。
属性のオブジェクトには次の項目を書ける。

| 項目 | 値 |
|---|---|
| `fg`、`bg`、`sp` | 文字の色、背景の色、下線と波線の色。`"#rrggbb"` の 24 ビットの色か、0〜255 の 256 色の番号。書かなければ端末の既定の色。本体は端末が 24 ビットの色を出せるかを確かめずに送るので、24 ビットの色を出せない端末では 256 色の番号で書く |
| `bold`、`italic`、`underline`、`undercurl`、`reverse` | 太字、斜体、下線、波線、文字と背景の色の入れ替え。真偽値 |

名前ごとに既定の属性を置き換え、既定の属性とは混ぜない。
例えば既定の `statusline` は反転と太字なので、`{ "bg": "#0f1117" }` だけを書くと反転と太字は消える。
ステータス行の部品の色（`statusline.` で始まる名前）だけは、`statusline` の属性の上に重ねる。
部品の色に `fg` か `bg` を書くと、`statusline` の反転は外す（既定の `statusline` は反転なので、外さないと部品の文字の色と背景の色が入れ替わって出る）。
このため、`statusline` に `bg` を書いていれば、部品の色に `fg` だけを書いても背景は `statusline` の色のままになる。
既定の `statusline`（反転だけで `bg` が無い）のまま部品の色に `fg` だけを書くと、その部品の背景は端末の既定の背景になる。
`statusline` の太字は外せないので、部品を太字にしたくないときは `statusline` 自体を書き換える。

色を付ける対象の名前は次のとおりである。
構文の色の 10 個の名前は、拡張機能（tree-sitter など）が `decoration/set` の `style` で使う名前と同じで、前に `syntax.` を付けない。
表に無い名前の範囲には色を付けない。

| 名前 | 対象 | 既定 |
|---|---|---|
| `comment`、`keyword`、`string`、`number`、`constant`、`function`、`type`、`variable`、`operator`、`punctuation` | 構文の色（基本設計 6.1） | 端末の 16 色の一部。`variable` と `punctuation` は色なし |
| `visual` | ビジュアルモードの選択の範囲 | 反転 |
| `statusline` | ステータス行 | 反転と太字 |
| `statuslinenc` | 出力の欄の下の行 | 反転 |
| `statusline.<部品の ID>` | ステータス行の部品（`statusline.mode`、`statusline.name`、`statusline.modified`、`statusline.readonly`、`statusline.filetype`、`statusline.recording`、`statusline.ruler`、`statusline.position`）。`statusline` の上に重ねる | なし |
| `statusline.mode.normal`、`statusline.mode.insert`、`statusline.mode.visual`、`statusline.mode.commandline` | モードごとのモードの部品。`statusline.mode` の上に重ねる。`visual` は文字単位と行単位の両方 | なし |
| `statusline.ext` | 拡張機能が足した部品。`statusline`（タブの行では `tabline.fill`）の上に重ね、その上に拡張機能が部品ごとに選んだ色を重ねる | なし |
| `nontext` | バッファの最終行より後の `~` | 端末の 16 色の 12 |
| `specialkey` | `^A` や `<80>` の形で出した文字 | 端末の 16 色の 12 |
| `linenr` | 行番号の列（[行番号](#行番号)）。番号の右の空けた 1 列を含む列の全体 | 端末の 16 色の 11（Vim の暗い背景の既定と同じ番号） |
| `search` | 直前の検索のパターンに一致する範囲（[検索の一致の色付け](#検索の一致の色付け)）。構文の色と診断の上、`visual` の下に重ねる | 文字は端末の 16 色の 0、背景は 11（Vim の暗い背景の既定と同じ番号） |
| `yank` | `y` でヤンクした範囲に一時的に付ける色（[ヤンクした範囲の色付け](#ヤンクした範囲の色付け)）。`search` の上、`visual` の下に重ねる。`visual` と重なったところで選択の色を上に見せたいときは、反転でなく背景の色で書く | 背景が端末の 16 色の 6（Vim の暗い背景の `MatchParen` の既定と同じ番号）。同梱の iceberg では iceberg.vim の `PmenuSel` の色 |
| `message.info`、`message.warning`、`message.error` | コマンドラインの行のメッセージ | 順に色なし、黄色、赤の背景 |
| `diagnostic.error`、`diagnostic.warning`、`diagnostic.info`、`diagnostic.hint` | 診断の範囲 | 波線か下線 |
| `popup` | 浮かべる窓（ホバーの説明、`:registers` の一覧） | 灰色の背景 |
| `popup.selected` | `:registers` の一覧の選んでいる項目 | 水色の背景 |
| `popup.border` | 浮かべる窓の枠線。書かなければ `popup` の色 | なし |
| `finder.match` | ファジーファインダーの一覧の、入力に一致した文字 | 太字と下線 |
| `sign.executionPoint`、`sign.breakpoint`、`sign.breakpointUnverified` | 印の列のデバッガーの印 | 順に黄色、赤、灰色 |
| `sign.diagnostic.error`、`sign.diagnostic.warning`、`sign.diagnostic.info`、`sign.diagnostic.hint` | 印の列の診断の印 | 順に赤、黄色、青、灰色 |
| `tabline`、`tabline.selected`、`tabline.fill` | タブの行の、ほかのタブ、いまのタブ、タブの右の空いた列 | 順に灰色の背景と下線、太字、反転 |
| `diff.add`、`diff.change`、`diff.delete`、`diff.text` | 見比べの、片側にだけある行、内容が違う行、もう片側にだけある行の位置の詰め物、違う行の中の違う文字 | 順に紺、紫、水色の背景（文字は青）、赤の背景と太字（Vim の暗い背景の既定と同じ番号） |

既定の属性の値は `internal/ui/theme.go` の `DefaultTheme` にある。

### 記号

`symbols` は、本体が描く記号の名前から、文字列への対応である。
書かなかった名前は既定の文字を使う。

| 名前 | 対象 | 既定 | 書ける文字列 |
|---|---|---|---|
| `sign.executionPoint` | 止まった位置の印 | `=>` | 表示幅が 1 か 2 の文字列 |
| `sign.breakpoint`、`sign.breakpointUnverified` | ブレークポイントの印 | `B`、`b` | 同上 |
| `sign.diagnostic.error`、`sign.diagnostic.warning`、`sign.diagnostic.info`、`sign.diagnostic.hint` | 診断の印 | `E`、`W`、`I`、`H` | 同上 |
| `readonly` | ステータス行の編集の禁止の印 | `[RO]` | 任意の文字列 |
| `modified` | ステータス行の未保存の印 | `[+]` | 任意の文字列 |
| `popup.border` | 浮かべる窓（編集の画面の中に描く窓。端末多重化ソフトのポップアップの枠線は端末多重化ソフトが描くので効かない）の枠線 | 空（枠線なし） | 空か、表示幅が 1 の文字を 8 つ。並びは左上、上、右上、右、右下、下、左下、左 |

どの記号にも、タブと制御文字は書けない。
印の列は 2 列なので、表示幅が 1 の印は右に空白を置いて描く。
既定の記号は ASCII にしてある。
罫線（`─`）や `●` のような、Unicode で幅が曖昧と定められた文字は、本体は 1 列と数える（Vim の `'ambiwidth'` の既定の `single` と同じ）。
端末がそれらの文字を 2 列で描く設定だと、列がずれる。

### ステータス行の並び

`statusline` は、`left` と `right` の 2 つの項目を持つオブジェクトで、どちらも部品の ID の配列である。
`left` の部品は左の端から、`right` の部品は右の端に寄せて、配列の順に並べる。
並べなかった部品は出さない。
同じ部品を 2 回並べると 2 回出す。
`statusline` を書かなければ、次の既定の並びを使う。
`statusline` を書いて `left` か `right` を省くと、省いた側には何も出さない。

```jsonc
{
  "statusline": {
    "left": ["mode", "name", "modified", "readonly"],
    "right": ["recording", "ruler", "position"],
  },
}
```

| 部品の ID | 中身 |
|---|---|
| `mode` | モードの名前（`NORMAL`、`INSERT`、`VISUAL`、`VISUAL LINE`、`COMMAND`） |
| `name` | バッファの名前（ファイルのパス）。無ければ `[No Name]` |
| `modified` | 未保存の変更があるときの印（`symbols` の `modified`） |
| `readonly` | 編集を禁止しているときの印（`symbols` の `readonly`） |
| `filetype` | ファイルの種類（`go` など）。判定できなければ出さない |
| `recording` | マクロの記録中の表示（`recording @q`） |
| `ruler` | カーソルの位置。Vim のルーラーと同じ「行,バイトの列-表示の列」 |
| `position` | 表示している範囲の位置（`All`、`Top`、`Bot`、`NN%`） |

部品は、中身の左右に空白を 1 つずつ置き、部品の色で描く。
`modified` と `readonly` は、前に部品があれば左の空白を置かずに続ける（`a.go [+]`）。
幅が足りなければ、右の部品を優先する。
左の部品が収まらなければ、Vim と同じく名前の先頭を `<` で切り、名前の後の `[+]` と `[RO]` を残す（`<4030-b80ff77dac4f/find.go [RO]`）。
名前を `<` だけにしても収まらなければ、名前を出さずに印を残し、それでも収まらなければ左の部品を右の端で切る。

### 拡張機能の部品

拡張機能は、宣言ファイルの `contributes.statusline` で部品を足せる（基本設計 7.1）。
部品の ID は拡張機能の ID と `.` で始まり、その後に `.` を含まない（`mrreview.status`）。
本体の部品と同じく、`statusline` と `tabline` の `left` と `right` に ID を書いて並べる。

```jsonc
{
  "statusline": {
    "left": ["mode", "mrreview.status", "name", "modified", "readonly"],
    "right": ["memusage.rss", "ruler", "position"],
  },
}
```

並べた部品の拡張機能は、起動の契機を待たずに本体の起動時に起動する。
部品の中身は拡張機能が送る。
中身が届くまでと、拡張機能が中身を空にした間は、その部品を出さない。
拡張機能が異常で終わるか止まると、その拡張機能の部品は消え、再起動した拡張機能が送り直すまで出ない。
中身はバッファに依らず、どのバッファのステータス行にも同じものを出す。
`:ext sync` で部品の拡張機能を入れ替えたときも、起動し直して部品を送り直させる。
幅が足りなければ、本体の部品（モード、名前の末尾の 20 列、位置など）より先に、拡張機能の部品を幅の広いものから切り詰め（末尾を `>` にする）、それでも足りなければ落とす。
1 つの部品の中身は 256 文字までで、超えた分は捨てる。
`.` を含む ID を、どの拡張機能も足していなければ、起動時に設定の誤りとして知らせて出さない。

### タブの行

タブの行は、既定ではタブが 1 つでも出る。タブが 2 つ以上のときだけ出すには、`tabline` の `show` を `"multiple"` にする（既定は `"always"`。[タブとジャンプ](#タブとジャンプ)）。

```jsonc
{ "tabline": { "show": "multiple" } }
```

タブの行も、`statusline` と同じく `left` と `right` で部品を並べられる。
タブの見出しの並びは部品 `tabs` で、他の部品を描いた残りの幅を使う。
`left` も `right` も書かなければ、`tabs` だけを左に置く（`{ "left": ["tabs"], "right": [] }`）。
どちらかを書くと、省いた側には何も出さない。`tabs` を並べなければタブの見出しは出ない。
`tabs` の他に、ステータス行の本体の部品（`mode`、`name` など。いまのバッファのもの）と拡張機能の部品を並べられる。
部品の色は `tabline.fill` の上に重ねる。

```jsonc
{ "tabline": { "show": "always", "left": ["tabs"], "right": ["memusage.rss"] } }
```

`tabs` は 2 回並べても、左、右の順で最初の 1 つだけを出す。
`tabs` をステータス行に並べると、未知の部品として設定の誤りにする。

## Ex コマンドの一覧

本体の Ex コマンドは次のとおりである（`internal/command/ex` の表と、`:ext`、`:help`、`:keys`）。
括弧の中は、打てる最も短い省略形である。
範囲（`:3,5d` の `3,5`、`%`、`'<,'>`）の書き方は Vim と同じで、パターンは Go の `regexp` の文法で書く（[vim-differences.md](vim-differences.md)）。
`|` で続けて書ける。

| コマンド | 動作 |
|---|---|
| `:w`、`:write`（`:w`） | バッファを書き出す。`:w ファイル` は別のファイルへ書く。`:w!` は既にあるファイルへも書く。範囲はバッファの全体（`:%w`）だけを書ける |
| `:wq`（`:wq`） | 書き出してタブを閉じる |
| `:x`、`:xit`（`:x`） | 変更があるか、ファイルを書いたときだけ書き出し、タブを閉じる |
| `:q`、`:quit`（`:q`） | タブを閉じる。未保存の変更があれば E37 で断り、`:q!` は変更を捨てる。client の最後のタブを閉じると、その client が終わる |
| `:wa`、`:wall`（`:wa`） | 未保存の変更のあるバッファをすべて書き出す |
| `:qa`、`:qall`（`:qa`） | この client のタブをすべて閉じる。`:qa!` は変更を捨てる |
| `:wqa`、`:wqall`、`:xa`、`:xall`（`:wqa`、`:xa`） | この client のタブの未保存の変更を書き出して、タブをすべて閉じる |
| `:e`、`:edit`（`:e`） | `:e ファイル` は、ファイルを新しいタブで開く。引数を省くと、ファイルを読み直す（`:e!` は変更を捨てる） |
| `:b`、`:buffer`（`:b`） | `:b 番号` か `:b 名前の一部` で、そのバッファのタブへ移る |
| `:bn`、`:bnext`、`:bp`、`:bprevious`、`:bN`（`:bn`、`:bp`、`:bN`） | 番号の順に次、前のバッファのタブへ移る。`:bn 3` は 3 個先 |
| `:bd`、`:bdelete`（`:bd`） | バッファを閉じる。引数はバッファの番号か名前の一部 |
| `:ls`、`:buffers`（`:ls`） | 開いているバッファの一覧を出す |
| `:tabmove`（`:tabm`） | タブを動かす。`:tabm 0` は先頭、`:tabm` は末尾、`:tabm +1` と `:tabm -1` は 1 つ右と左 |
| `:tab.new`（省略できない） | 名前の無い空のバッファを、いまのタブの右の新しいタブで開く。`:tab.new ファイル` は `:e ファイル` と同じ。Vim の `:tabnew` の名前は、設定ファイルの別名で付ける（[Ex コマンドの別名を足す](#ex-コマンドの別名を足す)） |
| `:popup`（`:popu`） | カーソルのあるタブをポップアップへ縮める（[ポップアップ](#ポップアップ)） |
| `:tabopen`（`:tabop`） | ポップアップをタブへ広げる（[ポップアップ](#ポップアップ)） |
| `:jumps`（`:ju`） | ジャンプリストを出す |
| `:registers`（`:reg`） | レジスターと履歴の一覧を出す（[レジスターと履歴](#レジスターと履歴)） |
| `:readonly`（`:reado`） | バッファの編集の禁止を切り替える（[編集の禁止](#編集の禁止)） |
| `:s`、`:substitute`（`:s`） | `:[範囲]s/パターン/置換/[フラグ]` で置き換える。範囲を省くとカーソルの行 |
| `:g`、`:global`、`:v`、`:vglobal`（`:g`、`:v`） | `:[範囲]g/パターン/コマンド` で、パターンに一致する行（`:v` は一致しない行）ごとにコマンドを実行する |
| `:d`、`:delete`（`:d`） | `:[範囲]d [レジスター] [回数]` で行を消す |
| `:!`（`:!コマンド`） | 端末をコマンドに渡して実行する（[外部のコマンドとエディタの中のエディタ](#外部のコマンドとエディタの中のエディタ)） |
| `:{範囲}!コマンド` | 範囲の行を外部のコマンドの標準入力に渡し、出力で置き換える |
| `:r !コマンド`、`:read !コマンド`（`:r`） | 外部のコマンドの出力を、カーソルの行（`:0r` は先頭）の後に入れる |
| `:{行番号}` | その行へ移る（`:$` は最後の行） |
| `:output`（`:out`）、`:outclose`（`:outc`） | 出力の欄を開く、閉じる（[出力の欄](#出力の欄)） |
| `:checktime`（`:checkt`） | 開いているバッファのファイルが外で変わっていないかを確かめる |
| `:option.set`（省略できない） | `:option.set 名前 値` で設定の項目の値を変える（[実行中に設定を変える](#実行中に設定を変える)） |
| `:option.get`（省略できない） | `:option.get 名前` で設定の項目の今の値を出す（[実行中に設定を変える](#実行中に設定を変える)） |
| `:set`（`:se`） | Vim の `:set` の書き方の一部を、`:option.set` と `:option.get` に変換して実行する別名（[実行中に設定を変える](#実行中に設定を変える)） |
| `:search.clearHighlight`（省略できない） | 次の検索まで、検索の一致の色を消す（[検索の一致の色付け](#検索の一致の色付け)） |
| `:nohlsearch`（`:noh`） | `:search.clearHighlight` の別名 |
| `:ext`（`:ext`） | `:ext sync`、`:ext update` で拡張機能を取得する（[拡張機能の取得](#拡張機能の取得ext-sync)） |
| `:help`、`:h`（`:h`） | この文書を開く（[ヘルプとキーの一覧](#ヘルプとキーの一覧)） |
| `:keys`（`:keys`） | キーの一覧を出す（[ヘルプとキーの一覧](#ヘルプとキーの一覧)） |

拡張機能のコマンドと、LSP と DAP の操作の名前（`:hover` など）も、`:` の後に打って呼べる（[拡張機能のコマンドを呼ぶ](#拡張機能のコマンドを呼ぶ)）。

### Ex コマンドの別名を足す

設定ファイルの `commandAliases` に、Ex コマンドの名前を付け替えるだけの別名を書ける。
同梱の `config.example.jsonc` は、Vim の名前の `:tabedit`（`:tabe` まで省略できる）と `:tabnew` を、どちらも `:tab.new` の別名にしている。
`:tab.new ファイル` は `:e ファイル` と同じく新しいタブでファイルを開き、引数なしの `:tab.new` は空のタブを開くので、どちらの名前も Vim と同じに使える。

```jsonc
{
  "commandAliases": {
    // :tabe ファイル は :e ファイル と同じく、ファイルを新しいタブで開く。引数なしの :tabe は空のタブを開く
    "tabedit": { "command": "tab.new", "min": 4 },
    // :tabnew は空のタブを開く
    "tabnew": { "command": "tab.new" },
    // 拡張機能のコマンドにも別名を付けられる（:ff か :f でファイルを探す）
    "ff": { "command": "finder.files", "min": 1 },
  },
}
```

キーは別名の名前で、英字だけで書く。
`command` は付け替える先で、本体の Ex コマンドの省略しない名前（`edit`、`tab.new`）か、拡張機能のコマンドの ID（`finder.files`、`help`）を書く。
`min` は省略できる最短の長さで、省くと省略できない（名前をすべて打つ）。
範囲、`!`、引数の扱いは、付け替える先のコマンドと同じである。
足した別名は、コマンドの一覧（`:finder.commands`）と `:help` の「設定ファイルの Ex コマンドの別名」の節に出る。

次の別名は設定の誤りとして知らせ、その別名だけを使わない。

- 付け替える先が、本体の Ex コマンドの名前でも拡張機能のコマンドの ID でもない（省略形の `e` と、別名の `set` も書けない）
- 名前か省略形が、本体の Ex コマンド、別名（`:set`、`:nohlsearch` と、先に書いた別名）、拡張機能のコマンドと重なる。例えば `"editor": { "command": "write", "min": 1 }` は、`:e` が `:edit` と重なる
- `min` が名前の長さより大きい、名前に英字でない文字がある

## 実行中に設定を変える

[設定の名前の一覧](#設定の名前の一覧)にある設定の項目は、実行中に Ex コマンドで変えたり読んだりできる。

| コマンド | 動作 |
|---|---|
| `:option.set 名前 値` | 項目の値を変える。真偽は `true` か `false`、整数は 10 進、文字列は名前の後の空白の後の行末まで（`\|` も値に含む。空も書ける）。値の末尾の空白は、型によらず除く |
| `:option.get 名前` | 今の値を `名前=値` の形でコマンドラインの行に出す（`filetype=go`） |

名前は一覧の正規の名前で書き、省略できない。
範囲がバッファごとの項目は、カーソルのあるバッファの値を変える、または読む。
知らない名前は `E518`、真偽の項目に `true` と `false` でない値を書くと `E474`、整数の項目に整数でない値を書くと `E521` のエラーになる。
範囲のある整数の項目（`lineNumber.minWidth`）に範囲の外の値を書くと、下限より小さければ `E487`、上限より大きければ `E474` のエラーになる。

`:set`（`:se`）は、Vim の `:set` の書き方の一部を受け付ける別名で、`:option.set` と `:option.get` の呼び出しに変換して実行する。
名前は、一覧の「`:set` の名前」の列の Vim の名前で書く。

| 書き方 | 変換先 |
|---|---|
| `:set 名前` | 真偽の項目なら `:option.set 正規の名前 true`。それ以外の項目は今の値を出す（`:option.get 正規の名前`。Vim と同じ） |
| `:set no名前` | `:option.set 正規の名前 false`。真偽の項目だけに書ける |
| `:set 名前!` | 今の値を読み、反対の値で `:option.set`。真偽の項目だけに書ける |
| `:set 名前=値` | `:option.set 正規の名前 値` |
| `:set 名前?` | `:option.get 正規の名前` |
| `:set 名前 名前 …` | 空白で区切った引数ごとに上のどれかへ変換し、左から順に実行する。途中の引数が誤りなら、そこで止まる（前の引数の設定は残る） |

`:set` は Vim の書き方をすべて受け付けるわけではない。
Vim の短い名前（`ft`）、`inv` の接頭辞、`+=`、`-=`、`^=`、`&`（既定値に戻す）は `E518`（知らない設定）、引数の無い `:set` は `E471`、`:setlocal` は `E492`（コマンドではない）になる（[vim-differences.md](vim-differences.md)）。

例えば、空のバッファから Go のコードを書き始めるときに `:set filetype=go` を打つと、ステータス行の `filetype` の部品（[ステータス行の並び](#ステータス行の並び)）が `go` を出し、ファイルの種類を契機にする拡張機能（LSP、tree-sitter）が起動する。
`:option.set filetype go` も同じである。
ただし、LSP の拡張機能は名前の無いバッファを言語サーバーへ送らないので、診断、ホバーなどの言語サーバーの機能は、`:w ファイル` で名前を付けた後に使える（名前の無いバッファで `:hover` を打つと「このバッファには言語サーバーが無い」と出る）。
構文の色（tree-sitter）は、名前の無いバッファでも付く。

### 設定の名前の一覧

| 名前 | 型 | 範囲 | `:set` の名前 | 内容 |
|---|---|---|---|---|
| `filetype` | 文字列 | バッファごと | `filetype` | バッファのファイルの種類。初めの値は、開いたときに設定の `fileTypes` の対応表と自動の判定で決まる。名前の無いバッファで決めた種類は、`:w ファイル` で名前を付けても残る。`:e` と `:e!` でファイルを開き直すと、開いたときの判定に戻る（外での変更の読み直しでは戻らない。Vim と同じ） |
| `lineNumber.show` | 真偽 | エディタ全体 | `number` | バッファの表示領域の左に行番号の列を出すか。既定は `false`（出さない。Vim と同じ）。列は印の列の右、本文の左に置き、色の名前は `linenr` である（[配色](#配色)） |
| `lineNumber.minWidth` | 整数 | エディタ全体 | `numberwidth` | 行番号の列の最小の列数（番号の右の空けた 1 列を含む）。既定は `4`、範囲は 1 から 20。列の幅は、この値と、バッファの最後の行の番号の桁数に 1 を足した数の大きい方になる（Vim の `'numberwidth'` と同じ） |
| `search.highlight` | 真偽 | エディタ全体 | `hlsearch` | 直前の検索のパターンに一致する範囲に色を付けるか（[検索の一致の色付け](#検索の一致の色付け)）。既定は `false`（付けない。Vim と同じ）。色の名前は `search` である（[配色](#配色)） |
| `yank.highlightMs` | 整数 | エディタ全体 | 無し | `y` でヤンクした範囲に色を付けておくミリ秒（[ヤンクした範囲の色付け](#ヤンクした範囲の色付け)）。既定は `0`（付けない）、範囲は 0 から 10000。色の名前は `yank` である（[配色](#配色)）。Vim にこの設定は無いので、`:set` では変えられず、`:option.set yank.highlightMs 300` で変える |

範囲がエディタ全体の項目は、設定ファイルにも書け、点で区切った名前を入れ子のキーにする（名前が `a.b` の項目なら `"a": { "b": 値 }`）。
範囲がバッファごとの項目は、初めの値がバッファを開くときに決まるので、設定ファイルには書けない（書くと誤りとして知らせる）。

### 行番号

行番号の列は、設定ファイルに次のように書くと、起動したときから出る。
同梱の `config.example.jsonc` もこの値にしている。

```jsonc
{
  "lineNumber": { "show": true, "minWidth": 5 },
}
```

実行中は `:set number`（出す）、`:set nonumber`（消す）、`:set number!`（切り替える）で変え、`:set numberwidth=5` で最小の列数を変える。
`:option.set lineNumber.show true` と `:option.set lineNumber.minWidth 5` も同じである。
変えた値は、次に画面を描くときから効く。

列の幅は、画面に見えている行ではなく、バッファの最後の行の番号で決まる。
このため、スクロールしても幅は変わらず、行数の桁が上がったとき（`minWidth` が `4` なら 999 行から 1000 行になったとき）に 1 列広がって本文の位置がずれる。
ずれを避けたいときは `minWidth` を大きくする（`5` なら 9999 行まで幅が変わらない）。

`minWidth` の範囲の外の値は誤りにして、値を変えない。
`:set numberwidth=0` のように 1 より小さい値は `E487`、21 以上の値は `E474` のエラーになる（Vim と同じ番号）。
整数に収まらない大きな値（`:set numberwidth=99999999999999999999`）も、正なら `E474`、負なら `E487` になる。
設定ファイルに範囲の外の値を書くと、起動後に位置と内容を知らせ、既定値の `4` を使う。

行番号を出すのはバッファの表示領域だけで、浮かべた窓（ホバーの説明、ファインダーなど）、出力の欄、拡張機能のペインには出さない。
見比べている間（[2 つのファイルを見比べる](#2-つのファイルを見比べる)）の詰め物の行と、最終行より後の `~` の行にも番号を出さない（詰め物の行の行番号の列は、Vim と同じく `linenr` の色の空白で埋める）。

### 検索の一致の色付け

直前の検索のパターンに一致する範囲に色を付けられる（Vim の `'hlsearch'`）。
設定ファイルに次のように書くと、起動したときから付ける。
同梱の `config.example.jsonc` もこの値にしている。本体の既定は Vim と同じく付けない。

```jsonc
{
  "search": { "highlight": true },
}
```

実行中は `:set hlsearch`（付ける）、`:set nohlsearch`（付けない）、`:set hlsearch!`（切り替える）で変える。
`:option.set search.highlight true` も同じである。
変えた値は、次に画面を描くときから効く。

色を付けるパターンは、直前の検索のパターン（`/`、`?`、`*`、`#`、`:s`、`:g` で使ったもの）で、文法は検索と同じ Go の `regexp` である。
色の名前は `search` である（[配色](#配色)）。

- 画面に見えている行のうち、パターンに一致する範囲のすべてに付ける。1 行に複数の一致があれば、どれにも付ける
- パターンは行ごとに当てるので、行をまたいで一致することは無い
- 空の一致（`/^`、`/$` など）は、その位置の 1 文字に付ける。行末の位置なら、行末の 1 列に付ける
- ビジュアルモードの選択と重なったところは、選択の色（`visual`）を上にする
- 付けるのはバッファの表示領域だけで、行番号の列、浮かべた窓（ホバーの説明、ファインダーなど）、出力の欄、拡張機能のペインには付けない

`:search.clearHighlight`（別名 `:nohlsearch`、`:noh`）は、次の検索まで一致の色を消す。
消しても設定の値は変わらない（`:set hlsearch?` は `search.highlight=true` を出す）。
消した色は、次のどれかで戻る（Vim の `:nohlsearch` と同じ）。

- 検索のパターンを使う操作（`/`、`?`、`n`、`N`、`*`、`#`、`:s`、`:g`、`:/foo/` のようなパターンの範囲）。パターンが見つからなかったときも戻る
- `:set hlsearch` などで `search.highlight` を設定し直したとき（同じ `true` を設定し直しても戻る）

カーソルを動かしただけと、`search.highlight` でない設定（`:set number` など）を変えただけでは戻らない。
正しくないパターン（`/(`）を打ったときは、パターンを記録しないので、色も戻らない。
`<C-l>` で色を消すには、設定ファイルの `keymap` に `"normal": { "<C-l>": ":nohlsearch<CR>" }` と書く。

`*` と `#` は、カーソルの単語を、その単語の全体に一致するパターン（`foo` なら `\bfoo\b`）にして、直前の検索にする。
向きは、`*` が前向き、`#` が後ろ向きである。
カーソルは動かさず、ジャンプリストにも記録しない（Vim の `*` と `#` は次の一致へ移る。[vim-differences.md](vim-differences.md)）。
`search.highlight` が `true` なら、その単語の一致にすぐ色が付き、`:nohlsearch` で消していた色も戻る。
次の一致へは `n`、前の一致へは `N` で移る（`#` の後の `n` は後ろへ移る）。
カーソルが単語の途中にあると、後ろ向きの最初の一致はカーソルのある単語そのものなので、`#` の後の `n` と `*` の後の `N` は、その単語の先頭へ移る。
例えば `foo bar foo baz` の 3 文字目（`fo` の後の `o`）で `#` を打ってから `n` を打つと、1 文字目へ移る。
Vim で同じ位置から `*N` を打ったときと同じ位置である（Vim の `#` は単語の先頭から探すので、`#` だけで 9 文字目の `foo` へ移る。2026-11-20 に Vim 9.1 で確かめた）。
回数は使わない（`2*` も `*` と同じ）。
`*` と `#` はモーションではないので、オペレーターの後には使えない（`d*` は何もしない）。
ビジュアルモードでは、選択を変えずにパターンだけを決める。

### ヤンクした範囲の色付け

`y` でヤンクした範囲に、決まった時間だけ色を付けられる（nvim の `vim.hl.on_yank` に当たる。Vim には無い）。
設定ファイルに次のように書くと、ヤンクした範囲に 300 ミリ秒だけ色が付いて消える。
同梱の `config.example.jsonc` もこの値にしている。本体の既定は `0` で付けない。

```jsonc
{
  "yank": { "highlightMs": 300 },
}
```

実行中は `:option.set yank.highlightMs 300` で変え、`0` にすると付けない。
変えた値は、次のヤンクから効く。
範囲（0 から 10000）の外の値は誤りにして、値を変えない（負の値は `E487`、10001 以上は `E474`）。
色の名前は `yank` である（[配色](#配色)）。

- 色を付けるのは `y` のオペレーターでヤンクしたときだけで、`yy`、`Y`、`yiw`、ビジュアルモードの `y` と `Y` を含む。`d`、`c`、`x` で消したテキストには付けない
- マクロの再生と `.` の繰り返しの中のヤンクには付けない（nvim の `vim.hl.on_yank` の既定と同じ）
- ブラックホールのレジスターへのヤンク（`"_yy`）には付けない（nvim と同じ）
- 色は、ヤンクした範囲の文字に付ける。行単位のヤンク（`yy`）は行の文字の全体に付き、空の行と行末より右には付かない（nvim と同じ）
- 時間が過ぎる前でも、そのバッファが変わったら（編集、拡張機能による編集、アンドゥ）消える。続けてヤンクすると、別のタブのバッファでのヤンクでも、前のヤンクの色は消えて新しい範囲に付く（色はエディタの全体で 1 つだけ。nvim と同じ）
- 検索の一致の色（`search`）と重なったところは、ヤンクした範囲の色を上にし、ビジュアルモードの選択の色（`visual`）は、ヤンクした範囲の色の上にする
- 付けるのはバッファの表示領域だけで、浮かべた窓（ホバーの説明、ファインダーなど）の中には付けない

## 拡張機能のコマンドを呼ぶ

拡張機能のコマンドは、コマンドラインで `:` の後にコマンドの ID を打って呼ぶ（`:acp.prompt`、`:mcp.status`）。
ID の後に空白を挟んで書いた文字列は、引数としてコマンドに渡る（`:acp.prompt この関数を説明して`）。
キーに割り当てるときは、[設定ファイル](#設定ファイル)の例のとおり `keymap` に書く。

LSP と DAP の操作（定義へ移動、ホバー、ブレークポイントなど）は、拡張機能のコマンドではなく本体の操作である（#203）。
拡張機能は宣言ファイルの `provides` で操作を引き受け、本体は、カーソルのあるバッファのファイルの種類で引き受ける拡張機能を選んで問い合わせる。
操作は既定のキー（次の節）で呼ぶほか、コマンドラインから操作の名前で呼べる（`:hover`、`:rename 新しい名前`、`:breakpoint.conditional i == 3`）。
コマンドラインから呼べるのは、どれかの拡張機能が `provides` で引き受けた操作だけである。
引き受ける拡張機能が無いバッファで呼ぶと、「hover の提供者がありません（ファイルの種類 text）」のように出る。

### LSP と DAP の既定のキー

`<leader>` は設定の `leader`（既定は `<Space>`）である。
どのキーも、設定の `keymap` で別のキーの列に変えられる。

| キー | 操作の名前 | 操作 |
|---|---|---|
| `<leader>lh`、`K` | `hover` | カーソルの位置の識別子の説明をポップアップで出す |
| `<leader>ld` / `<leader>lD`、`<C-]>` | `definition` | 定義をポップアップで見る / 定義へ移る |
| `<leader>li` / `<leader>lI` | `implementation` | 実装をポップアップで見る / 実装へ移る |
| `<leader>lt` / `<leader>lT` | `typeDefinition` | 型の定義をポップアップで見る / 型の定義へ移る |
| `<leader>lr` | `references` | 参照の一覧から選んで移る |
| `<leader>ln` | `rename` | 名前の変更。新しい名前は `:rename 新しい名前` で渡す。キーで呼ぶと渡し方を知らせる |
| `<leader>la` | `codeAction` | コードアクション（標準の LSP の拡張機能はまだ引き受けない） |
| `<leader>ls`、`<leader>lS` | `documentSymbols`、`workspaceSymbols` | シンボルの一覧（ファイル、ワークスペース）から選んで移る。ワークスペースはカーソルの位置の単語か `:workspaceSymbols 名前` の名前で探す |
| `<leader>le`、`<leader>lE` | （本体） | 診断の一覧（ファイル、開いているすべてのファイル）をポップアップで出す |
| `<leader>lj`、`<leader>lk` | （本体） | 次の診断、前の診断へ移る。端で折り返す |
| `gq{モーション}` | `format` | 範囲を整形する。言語サーバーが範囲の整形に対応しなければ、全体を整形する |
| `<leader>dc` | `debug.continue` | 続行。デバッグが始まっていなければ始める |
| `<leader>dp`、`<leader>dt`、`<leader>dr` | `debug.pause`、`debug.terminate`、`debug.restart` | 一時停止、終了、終了してから前回の設定で始め直す |
| `<leader>db` / `<leader>dB` | `breakpoint.toggle` / `breakpoint.conditional` | ブレークポイントの切り替え / 条件付きのブレークポイント。条件は `:breakpoint.conditional 式` で渡す |
| `<leader>dX` | `breakpoint.clear` | ブレークポイントをすべて消す |
| `<leader>dn`、`<leader>di`、`<leader>do` | `debug.stepOver`、`debug.stepIn`、`debug.stepOut` | ステップオーバー、ステップイン、ステップアウト |
| `<leader>dl` | `debug.runLast` | 前回の設定で始める |
| `<leader>du` | `debug.toggleUI` | DAP のペインの開け閉め（標準の DAP の拡張機能はまだ引き受けない） |
| `<leader>l?`、`<leader>d?` | （本体） | LSP、DAP のキーの一覧を出す（[ヘルプとキーの一覧](#ヘルプとキーの一覧)） |

### 段階 2 のコマンドからの移行

段階 2 の LSP と DAP の拡張機能のコマンドは、#203 で本体の操作に置き換えたので、もう無い。
古いコマンドをコマンドラインで打つと、`E492: エディタのコマンドではありません: lsp.definition` のように出る。
設定の `keymap` の右辺に古いコマンドを書いていれば、起動したときに設定の誤りとして知らせる。
そのまま使うと、左辺のキーが既定のキーを隠す。
たとえば `"<Space>d": ":lsp.definition<CR>"` があると、`<Space>db` の `<Space>d` が先に展開されて E492 になり、残りの `b` は単語の後退として動く。
その項目は消すか、右辺を新しい名前に書き換える。

| 段階 2 のコマンド | 操作の名前（`:` で呼ぶ） | 既定のキー |
|---|---|---|
| `lsp.definition` | `definition` | `<C-]>`、`<leader>lD` |
| `lsp.hover` | `hover` | `K`、`<leader>lh` |
| `lsp.format` | `format` | `gq{モーション}` |
| `dap.start` | `debug.continue`（始まっていなければ始める） | `<leader>dc` |
| `dap.stop` | `debug.terminate` | `<leader>dt` |
| `dap.toggleBreakpoint` | `breakpoint.toggle` | `<leader>db` |
| `dap.continue` | `debug.continue` | `<leader>dc` |
| `dap.next` | `debug.stepOver` | `<leader>dn` |
| `dap.stepIn` | `debug.stepIn` | `<leader>di` |
| `dap.stepOut` | `debug.stepOut` | `<leader>do` |

### 操作の補足

「ポップアップで見る」は、移らずに、移動先の行の 3 行前から 20 行の写しをカーソルの近くに出す（[ポップアップ](#ポップアップ)）。
写しは編集できない。`<C-w>w` でフォーカスを入れて `:tabopen` を打つと、本物のファイルを開いて移動先へ移る。
ドットリピート（`.`）は、`gq` を除く LSP と DAP の操作を記録しない。`x` の後に `K` を打ってから `.` を打つと、`x` を繰り返す。
一覧（参照、シンボル）は、選択肢の問いで番号を打って選ぶ。番号を振れるのは 9 件までである。
補完は、まだ無い。

## 出力の欄

デバッグ対象の出力やエージェントの返答のように、拡張機能が後から書き足すテキストは、画面の下に開く出力の欄に出る。
欄は拡張機能ごとに名前を持つ（DAP は `dap`、ACP は `acp`）。
欄を開いていなければ、書き足されても画面には出ない。

| コマンド、キー | 動作 |
|---|---|
| `:output [名前]`（`:out`） | 欄を開く。名前を省くと、最後に書き足された欄を出す |
| `:outclose`（`:outc`） | 欄を閉じる |
| `<C-w>P` | 閉じていれば欄を開き、開いていれば次の欄に切り替える |
| `<C-w>z` | 欄を閉じる |
| `<C-w><PageUp>`、`<C-w><PageDown>` | 欄を上と下へスクロールする |

欄の下の行に、`[出力] dap` のように欄の名前が出る。
`:output` の後には、`|` で拡張機能のコマンドを続けられる（`:output|acp.prompt ...`）。
欄の動作の詳細は基本設計 6.7 の「表示のデータ」にある。

## タブとジャンプ

開いたファイルは、それぞれ 1 つのタブになる。
`:e <パス>` や定義へ移動で開いたファイルは、新しいタブとしていまのタブの右に開き、元のタブも残る。
画面の一番上の行にタブの行が出る（設定の `tabline.show` で、タブが 2 つ以上のときだけにもできる。未保存のタブには `[+]`、編集を禁止したタブには `[RO]` が付く）。
タブを閉じると、そのファイルのバッファも閉じる（隠れたバッファは無い）。

| コマンド、キー | 動作 |
|---|---|
| `<Tab>`、`<S-Tab>` | 次と前のタブへ移る。端の次は反対の端へ回る。`2<Tab>` のように回数を書ける |
| `:bn`、`:bp`（`:bN`） | `<Tab>` と `<S-Tab>` と同じ。`:bn 2` のように回数を書ける |
| `:b <番号>`、`:b <名前>` | そのバッファのタブへ移る。名前は一部でよく、1 つに決まらなければ E93 になる |
| `<C-^>` | 直前のタブへ移る。`2<C-^>` は 2 番のバッファのタブへ移る |
| `:tabmove`（`:tabm`） | いまのタブを動かす。引数なしか `$` で末尾へ、`0` で先頭へ、`N` で N 番目のタブの後ろへ、`+N` と `-N` で右と左へ動かす（Vim と同じ） |
| `:tab.new` | 名前の無い空のバッファを、いまのタブの右の新しいタブで開く。`:w ファイル` で名前を付けて書ける。同梱の設定の例では `:tabnew` と `:tabe` でも打てる |
| `:ls`（`:buffers`） | 開いているバッファの一覧を出す。`%` はいまのタブ、`#` は直前のタブ（`<C-^>` の移り先）、`a` はどれかの client で表示しているタブ、`h` はどの client でも表示していないタブ（タブの行には残っている。隠れたバッファではない）かどのタブでもないバッファ、`+` は未保存の変更があるバッファである。別の client のタブには `[client N]` が、どのタブでもないバッファには `[タブなし]` が付く |
| `<C-o>`、`<C-i>` | ジャンプの前の位置へ戻り、戻った位置から進む。別のタブへもまたがる。`<Tab>` はタブの移動に使うので、ジャンプを進めるには `<C-i>` を区別して送る端末を使うか、`keymap` で別のキーを `<C-i>` に割り当てる |
| `:jumps`（`:ju`） | ジャンプリストを出す |
| `:q`、`:bd` | いまのタブを閉じる。`:bd <番号>`、`:bd <名前>` は指定したバッファのタブを閉じる。未保存の変更があれば E37（`:q`）か E89（`:bd`）を出し、`!` を付けると変更を捨てて閉じる |
| `:wa` | 未保存の変更のあるバッファを、別の client のタブも含めてすべて書き出す |
| `:qa`、`:qa!` | この client のタブをすべて閉じて終わる。`:qa!` は未保存の変更を捨てる |
| `:wqa`（`:xa`） | この client のタブのうち未保存の変更のあるものを書き出してから終わる |

最後のタブを閉じると、その client（ペインの `vi-imitation`）が終わる。
タブを移るときは、未保存の変更があっても E37 を出さない（離れたタブもタブの行に残るため）。
最後のタブを閉じると server も終わるとき（同じ window にほかの client が無いとき）は、未保存の変更のある別のバッファがあれば、`:q` と `:qa` は E162 でそのバッファの名前を出し、そのタブへ移る。
レジスターとその履歴、ドットリピート（`.`）、`;`、`@@`、マクロの記録は、タブを切り替えても引き継ぐ（`qa:bn<CR>...q` で、複数のファイルにまたがる編集を記録できる）。

### 同じ window の複数の client

tmux の同じ window（herdr の同じタブ）の別のペインで `vi-imitation` を起動すると、同じ server の 2 つ目の client になる。
client ごとに自分のタブの行を持ち、1 つのファイルはどれか 1 つの client のタブになる。

- 別の client のタブになっているファイルを `:e` や `:b` で開こうとすると、開かずに、その client のペインへフォーカスを移し、そのタブを表示する。その client が入力の途中（インサートモードなど）なら、タブは替えずにフォーカスだけを移す
- 拡張機能の結果（定義へ移動で開くファイル、メッセージ、選択肢の問い）は、その操作を打った client に出る
- レジスターとその履歴、直前の検索、マクロ、ドットリピートは client の間で共有する。あるペインでヤンクしたテキストを、別のペインで `p` で貼れる
- マクロの記録は、記録を始めた client のキーだけを記録する。記録中の表示（`recording @q`）も、その client のステータス行にだけ出る
- ジャンプリストは client ごとに持つ
- 確認の問い（`:3,1d` の範囲の入れ替えの問い、外で変わったファイルへの `:w` の問いなど）と、拡張機能の選択肢の問いは、問いを出した client にだけ出る。答えるまで、ほかの client のキーは処理しない（ほかの client には、待っていることが出る）。問いを出した client が終わったら、問いは取り下げる。
- 外部のコマンドは同時に 1 つだけ動かせる。ほかの client の実行中に `:!`、`:{範囲}!`、`:r !` を打つか、権限を上げた保存をすると E319 になる
- `:!`、`:{範囲}!`、`:r !` の外部のコマンドの実行中は、そのコマンドを打った client のキーだけを待たせる。`:{範囲}!` と `:r !` を `<C-c>` で中断できるのも、その client だけである（`:!` の `<C-c>` は端末からコマンドへ届く）
- ペインを閉じるなどで client が異常に終わったときは、その client のタブのうち未保存の変更のあるバッファを、どのタブでもないバッファとして残す。ほかの client で `:ls` で確かめ、`:b` でタブにできる

ジャンプとして記録するのは、`G`、`gg`、検索（`/`、`?`、`n`、`N`）、`%`、`{`、`}`、`:s`、`:g`、`:d`、バッファの切り替え、定義へ移動などの拡張機能による移動である。
`*` と `#` はカーソルを動かさないので記録しない（[検索の一致の色付け](#検索の一致の色付け)）。
`j` や `w` のような短い移動は記録しない。

### 2 つのファイルを見比べる

2 つの client のタブを並べて見比べる（Vim の `vim -d` に当たる）。
見比べのための専用のタブは作らず、2 つのペインの client のタブに、それぞれ差分の色と詰め物（もう片側にだけある行の位置の `-` の行）を出す。

| コマンド、キー | 動作 |
|---|---|
| `:diffsplit <パス>`（`:diffs`） | いまのペインの右に新しいペインを開き、そこで起動した client にファイルのタブを開いて、いまのタブと見比べる。ファイルが既に別の client のタブなら、ペインを開かずにそのタブと見比べる |
| `:diffthis`（`:difft`） | 2 つの client のタブで打つと、その 2 つを見比べる |
| `:diffoff`（`:diffo`） | いまのタブの見比べを終える（相手のタブも終わる） |
| `:diffupdate`（`:diffu`） | 差分を計算し直す（ふだんは編集のたびに計算し直す） |
| `]c`、`[c` | 次と前の差分の始まりへ移る。`3]c` のように回数を書ける |
| `do` | カーソルの差分を、相手の内容で置き換える（`u` で戻せる） |
| `dp` | 相手の差分を、カーソルの差分の内容で置き換える。相手のタブが編集を禁止していれば E21 になる |

- 片方をスクロールすると、もう片方も同じ位置へそろい、カーソルも対応する行へ移る（Vim の `scrollbind` と `cursorbind`）。もう片方の client が入力の途中（インサートモードなど）なら、そろえるのは入力を終えてからになる
- 端末多重化ソフト（tmux、herdr）の外では、client が 1 つなので見比べられない（`:diffsplit` と `:diffthis` は誤りになる）
- `do` と `dp` は、Vim と同じく `d` の後の `o` と `p` として読むので、`d` の後に時間を置いてもよい。見比べていないタブでは E99 になる
- 1 つのタブは 1 つの組でだけ見比べる。見比べているタブを別のタブと見比べ始めると、前の組は終わり、前の相手の client にそのことが出る
- 差分の位置が Vim と違うことがある（基本設計 6.8）

## 囲みと行のずらし

囲みの操作は、vim-sandwich と同じキーで本体が持つ（要件定義 FR-73〜FR-75、基本設計 4.8）。
どれもノーマルモードのキーで、ドットリピート（`.`）とマクロで繰り返せ、1 回の `u` で取り消せる。

| キー | 動作 | 例（`\|` はカーソル） |
|---|---|---|
| `sa{モーションかテキストオブジェクト}{文字}` | 範囲の前と後ろに囲みを足す | `foo b\|ar` で `saiw)` → `foo (bar)` |
| `sd{文字}` | カーソルを含む最も内側のその文字の囲みを消す。`2sd(` は内側から 2 番目 | `(fo\|o)` で `sd)` → `foo` |
| `sr{前の文字}{後の文字}` | カーソルを含む最も内側の前の文字の囲みを、後の文字の囲みに替える | `"fo\|o"` で `sr"'` → `'foo'` |

- `(` と `)`、`[` と `]`、`{` と `}`、`<` と `>` は、開きと閉じのどちらの文字でも同じ組を指し、空白は足さない
- それ以外の文字は、同じ文字で前と後ろを囲む。引用符（`"`、`'`、`` ` ``）の囲みは 1 行の中だけで探し、`\"` のように逃がした引用符を数えない
- `sd<Space>` と `sr<Space>…` は、空白とタブの続きを 1 つの囲みとして扱う
- `saj(` や `saip(` のように行単位の対象では、開きと閉じをそれぞれ独立した行として足す。引用符（`saj"`）は行にせず、最初の行の字下げの後と最後の行の末尾に足す
- `sa` の前の回数は、他のオペレーターと同じく対象の回数に掛ける（`2saiw(` は `sa2iw(` と同じ）

ノーマルモードの `s`（1 文字を消して入力する）は、既定では何にも割り当てない（要件定義 1.1）。
使いたいときは、設定のキーマップで `s` を `cl` に割り当てる。
キーマップの展開は次のキーを待たないので、`s` を割り当てると `s` を打った時点で `cl` に展開され、`sa`、`sd`、`sr` は打てなくなる。
ビジュアルモードの `s`（選択を消して入力する）は残る。

ノーマルモードの `>` と `<` は、次のキーを待たずに、Vim の `>>` と `<<` と同じく行をずらす（FR-76）。
`3>` は 3 行をずらし、`>>` と打つと 2 回ずれる。
`>j` のようにモーションを続けるオペレーターは無いので、範囲をずらすときはビジュアルモードで選んでから `>` を打つ。
ビジュアルモードの `>` と `<` は、ずらした後もビジュアルモードと選択の範囲を保つ（FR-77。Vim で `>gv` と打ったときと同じ）。
続けて `>` を打てばさらにずれ、`<Esc>` で抜ける。

## 選んだ行にまとめて入力する

ビジュアルモード（`v` と `V` のどちらでも）の `I` と `A` は、選んだ行のすべてに同時に入力する（要件定義 FR-141、基本設計 3.1）。
`I` は各行の最初の空白でない文字の前（空の行と空白だけの行では行末）から、`A` は各行の行末から入力する。
どちらも、その行でノーマルモードの `I` と `A` を打ったときと同じ位置である。

| キー（`\|` はカーソル） | 結果 |
|---|---|
| `\|foo` と `  bar` と `baz` の 3 行で `VjjI// <Esc>` | `// foo`、`  // bar`、`// baz` |
| `\|a` と `b` と `c` の 3 行で `VjjA,<Esc>` | `a,`、`b,`、`c,` |

- 打っている間から、すべての行にテキストが出る。カーソルは選んだ範囲の最初の行にだけ出る
- `<BS>`、`insert.delete-prev-word`、`insert.delete-to-line-start` のように、入力の位置から見た相対の位置を消す操作は、すべての行で同じように消す
- `insert.delete-to-line-start`（同梱の例では `<C-u>`）は、各行の先頭まで消す。入力したテキストだけでなく、もとの行の中身と字下げも消す（`  ab`、`  cd` で `VjA<C-u>X<Esc>` は `X`、`X`）。入力したテキストだけを打ち直すときは `<BS>` か `insert.delete-prev-word` を使う
- カーソルキー（`<Left>`、`<Home>` など）でカーソルを動かすと、まとめての入力を終える。それまでに入れたテキストはすべての行に残り、その後はカーソルの位置だけに入る
- カーソルが動かないとき（`A` の直後の `<End>` や、同梱の例の `<C-e>`）は、まとめての入力を続ける
- 改行（`<CR>`）、行の先頭の `<BS>`（前の行とつなぐ）、行末の `<Del>`（次の行をつなぐ）も、行をまたぐのでまとめての入力を終え、カーソルの位置だけで行う
- 端末から貼ったテキストは、打った文字と同じく 1 文字ずつ入る。改行の前まではすべての行に入り、改行の後はカーソルの位置だけに入る
- `2I` のように回数を付けると、各行で入力を繰り返す
- `<Esc>` の後の `u` は、すべての行の入力を 1 回で戻す
- `.` は、カーソルの行から同じ数の行に、同じ規則で入力し直す。行が足りなければバッファの最後の行で止めるが、次の `.` はまた同じ数の行に入れる
- 入力の途中に拡張機能がバッファを書き換えたとき（MCP の `edit_buffer`、ACP の書き込みなど）は、各行の入力の位置がそれぞれ編集に合わせてずれる。消された範囲や書き換えられた範囲の中（範囲の始まりより後ろ）にあった位置は無くなり、その行にはそれ以降の入力が入らない（カーソルの行を除く）

Vim では、矩形選択でないビジュアルモードの `I` と `A` は 1 か所にしか入力しない（[vim-differences.md](vim-differences.md)）。

## 編集の禁止

バッファごとに編集を禁止できる（要件定義 FR-85、FR-86、FR-119、FR-120、基本設計 4.6）。
禁止中も、移動、検索、ヤンク、ビジュアルモードの選択、`:w` での保存はできる。
テキストを変える操作（`x`、`d`、`p`、`u`、`:s`、`:d` など）とインサートモードへ移る操作（`i`、`o` など）は、何も変えずに `E21: 編集を禁止したバッファは変更できません` を出す。
マクロの再生は、テキストを変える操作が断られた時点で止まる。

| 禁止にする方法 | 対象 |
|---|---|
| `:readonly`（`:reado` まで省略できる） | カーソルのあるバッファ。打つたびに禁止と解除が切り替わる |
| 起動のオプション `-R`（`vi-imitation -R a.go`） | 起動の引数のファイル。`:e` で後から開くファイルは禁止しない |
| 設定の `readonlyPaths` | 並べたディレクトリの下のファイル。開いた時点で禁止にする。`/` か `~/` で始まるパスはそのディレクトリの下、`/` を含まない名前（`node_modules`）はどの階層のディレクトリの名前と一致しても当たる |

```jsonc
{
  // 開いた時点で編集を禁止にするディレクトリ。絶対パス、~/ で始まるパス、/ を含まないディレクトリの名前で書く
  "readonlyPaths": ["~/go/pkg/mod", "/usr/include", "node_modules"]
}
```

禁止中のバッファでは、ステータス行の名前の後に `[RO]` が出る（FR-87）。
印の文字と出す位置は、[見た目の設定](#見た目の設定)の `symbols` の `readonly` と `statusline` で変えられる。
`-R` と `readonlyPaths` で禁止したバッファも、`:readonly` で解けば編集できる。
禁止の前のアンドゥの履歴は残るので、解いた後に `u` で戻れる。
拡張機能の編集（`buffer/edit`）も断り、拡張機能へは誤りの応答を返す。
Vim との違い（Vim の `-R` は編集を許して保存を断る、など）は [vim-differences.md](vim-differences.md) にある。

## 外部のコマンドとエディタの中のエディタ

`:!{コマンド}` は、Vim と同じく端末をコマンドに渡して動かす（要件定義 FR-80、基本設計 4.9）。
画面は代替画面に入る前の端末に戻り、コマンドの出力が出る。
コマンドが終わると `続けるには ENTER を押してください` が出る。
`<CR>`、`<Space>`、`<Esc>` で画面に戻り、それ以外のキーは画面に戻った後にコマンドとして使う（`:` ならコマンドラインに入る）。
終了コードが 0 でなければ、その前に `シェルが N を返しました` が出る。シグナルで終わったときは `コマンドが中断されました` が出る（Vim の `Command terminated`）。
`:!sh` のような対話のコマンドも使え、`<C-c>` はコマンドに届き、エディタは終わらない。

`:!` などでエディタが起動したコマンドには、環境変数 `VI_IMITATION_SOCKET`（server のソケット）、`EDITOR` と `GIT_EDITOR`（`vi-imitation` の実行ファイル）が入る（FR-82）。
このもとで `vi-imitation` を起動すると、新しい画面を作らずに、渡したファイルを `:!` を打った client のタブで開き、タブが閉じられるまで待つ（FR-83、FR-117）。
たとえば `:!git commit` では、コミットのメッセージがタブで開く。
書いて `:wq` でタブを閉じると、git がコミットを続け、終わると `続けるには ENTER を押してください` が出る。
`:!sh` の中で `git commit` を打っても同じに動く。

入れ子のエディタとして別に使いたいときは、`vi-imitation --new <ファイル>` と打つ（FR-84）。
親の server に繋がずに、渡された端末で新しいエディタが動く。

### 権限の足りないファイルの保存

権限の足りないファイル（`/etc/hosts` など）への `:w` は、`権限を上げて書き込みますか (y/n)?` と問う（FR-79）。
シンボリックリンクなら、問いにはたどった先のパスが出て、先のファイルへ書く。
ファイルには書けて、ディレクトリにだけ書き込みの権限が無いときは、問わずにそのまま書く（Vim と同じ）。
`y` で答えると、画面を一時的に通常の端末へ戻し、`sudo tee <パス>` にバッファの中身を渡して書き込む。
パスワードは `sudo` が端末から直接聞き、エディタはパスワードを読まない。
書き込めたら、バッファは保存済みになる（`:wq` なら続けてタブを閉じる）。
書き込む先のディレクトリが無く、権限が足りずに作れないときも同じく問い、`sudo mkdir -p <ディレクトリ>` で作ってから書き込む（FR-116）。

`sudo` の代わりに使うコマンドは、設定の `elevateCommand` で変えられる。
値はコマンドと引数の配列で、この後に `tee <パス>` か `mkdir -p <ディレクトリ>` を付けて、シェルを通さずに起動する。

```jsonc
{
  "elevateCommand": ["doas"]
}
```

`doas` も、標準入力がパイプのときにパスワードを端末から読むことを、OpenDoas のソースで確かめた（[基本設計 4.7](basic-design.md#47-保存の先のディレクトリと権限の不足)）。
実物の `doas` では動かしていない。

## レジスターと履歴

レジスターの名前は、`a`〜`z`、`_`、名前の省略の 3 種類だけである（要件定義 FR-50）。
Vim の数字のレジスター（`"0`〜`"9`）、`"-`、`""`、`".`、`":`、`"+`、`"*`、`"/` などは無い。
無い名前を指定した操作（`"0p`、`"+yy`、`q1`、`@-`）は、何もせずに `E354` をメッセージの行に出す。

ヤンクと削除で書いたテキストは、レジスターの履歴に新しい順に積む（FR-51）。
名前を省略した `p` と `P` は、履歴の先頭（最後に積んだもの）を貼る。
貼っても履歴の順は変わらない。

| 操作 | 名前付きのレジスター | 履歴 |
|---|---|---|
| `yw`、`yy` など、名前を省略したヤンク | 変えない | 先頭に積む |
| `dw`、`x`、`cw`、`:d` など、名前を省略した削除と変更 | 変えない | `deleteToHistory` が真（既定）なら先頭に積む。偽なら何もしない |
| `"ayw`、`"add` など、名前を指定した書き込み | `a` に書く | 書いた内容を先頭に積む |
| `"Ayw` など、大文字での追記 | `a` に追記する | 追記した後の `a` の内容全体を先頭に積む |
| `"_dd` など、`_` を指定した操作 | 変えない | 変えない |
| `qa`〜`q` のマクロの記録 | `a` に書く（`qA` は追記） | 変えない |
| ビジュアルモードでの端末からの貼り付けで置き換えたテキスト | 変えない | 変えない |

履歴の件数と、1 件あたりの最大の大きさは、設定ファイルの `registers` で変えられる（FR-52、FR-53）。

```jsonc
{
  "registers": {
    // 履歴の件数。超えたら最も古いものから捨てる。1 以上 10000 以下（既定 100）
    "historySize": 100,
    // 履歴に積む 1 件の最大のバイト数。1 以上（既定 1048576 = 1 MiB）
    "maxEntryBytes": 1048576,
    // 名前を省略した削除と変更を履歴に積むか（既定 true）
    "deleteToHistory": true,
    // 履歴の先頭をホストのクリップボードへ送る手段。"osc52" か "off"（既定 "osc52"）
    "clipboard": "osc52",
  },
}
```

`maxEntryBytes` を超えたテキストは履歴に積まず、積まなかったことをメッセージの行に出す。
そのテキストは、次に履歴に積むまでの間だけ、名前を省略した `p` で貼れる。
名前を指定した書き込み（`"ayG`）は、大きさによらずそのレジスターに書く。

### ホストのクリップボードへの送り出し

`clipboard` が `"osc52"`（既定）なら、履歴の先頭が変わるたびに、その内容を OSC 52 の制御シーケンスで端末へ送る（FR-56）。
端末が OSC 52 に対応していれば、ホストのクリップボードに入る。
SSH の先で動かしても、手元の端末のクリップボードに入る。
行単位のテキスト（`yy`、`dd`）は、最後に改行を付けて送る。
`maxEntryBytes` を超えて履歴に積まなかったテキストは送らない。
base64 にして 1,000,000 バイト（元のテキストでおよそ 750,000 バイト）を超えるテキストも送らず、送らなかったことをメッセージの行に出す（仮の上限。tmux 3.4 は base64 で 1 MiB を超える OSC 52 を黙って捨てる）。
貼り付けと `"_` への書き込みは履歴の先頭を変えないので、送らない。

ホストのクリップボードは読まない。
ホストでコピーしたテキストは、端末の貼り付け（bracketed paste）で入れる。

tmux の中で動かすときは、tmux の `set-clipboard` を `on` にする。
tmux は、`on` のときに OSC 52 を受け取って tmux のバッファに入れ、外側の端末のクリップボードにも入れようとする（外側の端末の terminfo に `Ms` があるとき）。
既定の `external` では、tmux の中のアプリケーションが送った OSC 52 を受け付けない（tmux 3.4 の man の `set-clipboard`）。

```sh
# ~/.tmux.conf
set -g set-clipboard on
```

送ったテキストが tmux に届いたかは、`tmux show-buffer` で確かめられる。

### 履歴の一覧から貼る

`:registers`（`:reg`）で、履歴と名前付きのレジスターの一覧を、画面の上の端に浮かべる窓に出す（FR-54）。
窓の行は、上から履歴の項目を新しい順に、続けて中身のある名前付きのレジスターを `a` から順に並べる。
各行は、番号（履歴）か `"a` の形の名前、種類、テキストの 1 行目の順に並ぶ。
種類は、行単位なら `l`、文字単位なら `c` で、2 行以上にわたるテキストなら `l+2` のように 2 行目以降の行数を続ける。
画面が小さく窓を描けないとき（バッファの表示領域が 2 行に満たないとき）は、窓を開かずにメッセージの行に知らせる。

| キー | 動作 |
|---|---|
| `j`、`<Down>` | 次の項目を選ぶ |
| `k`、`<Up>` | 前の項目を選ぶ |
| `<CR>` | 選んだ項目を、`p` と同じくカーソルの後に貼って窓を閉じる |
| `<Esc>` | 貼らずに窓を閉じる |

他のキーは、窓を出している間は捨てる。
選んで貼った履歴の項目は、履歴の先頭へ移る。
その後の `p` と `.` は、同じテキストを貼る。
名前付きのレジスターを選んだときは、`"ap` と同じく貼り、履歴の順は変えない。

マクロの中に `:reg<CR>` を記録しても、窓での選択（`j`、`<CR>` など）は記録されない。
再生すると、`:reg<CR>` の後に記録したキーを先に実行し、窓は再生が終わってから開く。

キーで開きたいときは、設定ファイルの `keymap` で割り当てる。

```jsonc
{
  "keymap": {
    "normal": {
      "<Space>r": ":registers<CR>",
    },
  },
}
```

### 貼った直後に履歴の別の項目へ貼り直す

`p`、`P`、一覧からの貼り付けの直後に `<C-p>` を打つと、貼ったテキストを、履歴の 1 つ古い項目に置き換える（FR-55）。
続けて打つと、さらに古い項目に置き換える。
`<C-n>` は新しいほうへ戻る。
`3<C-p>` のように回数を付けると、その数だけ進む。

- 置き換えは、元の貼り付けと同じ位置に、同じ向き（`p` か `P`）と同じ回数で入れる
- 置き換えは、元の貼り付けとアンドゥの 1 単位にまとまる。`u` の 1 回で、貼り付けの前に戻る
- 置き換えても、履歴の順は変わらない
- いま貼っているテキストと同じ内容の項目は飛ばす。`"ayiw` は `a` と履歴の先頭に同じテキストを書くので、その後の `"ap` の直後の `<C-p>` は、履歴の 2 番目に置き換える
- `"ap` の直後の `<C-p>` は、履歴の先頭（`a` と同じ内容なら、その次）に置き換える。そこから `<C-n>` を打つと、`a` の内容に戻る
- 置き換える項目が貼り付けの上限（64 MiB）を超えるときは、元の貼り付けを残したまま、メッセージの行に知らせる
- 貼り付けの後に別の操作（カーソルの移動、`u`、ヤンクなど）を挟むと、`<C-p>` と `<C-n>` は何もしない
- 履歴の端で打っても何もしない。反対の向きのキーは続けて使える
- `.` は、置き換えた後も元の貼り付け（`p` なら履歴の先頭を貼る）を繰り返す

## ポップアップ

ホバーの説明、「ポップアップで見る」の写し、ヘルプ、キーの一覧、拡張機能が出す一覧は、画面の上に浮かべたポップアップに出る（FR-68）。
ポップアップの中身はバッファなので、フォーカスを入れると `j`、`k`、`<C-d>`、`/`、`y` などのノーマルモードのキーが効く。
`q` は、ポップアップの中でもマクロの記録である。
ホバー、写し、ヘルプ、キーの一覧のバッファは編集できない（[編集の禁止](#編集の禁止)）。

置き場所は 2 つある。

| 置き場所 | 出るもの | フォーカス |
|---|---|---|
| カーソルの近く | ホバー、「ポップアップで見る」の写し、診断の一覧 | 入っていない。次のキーかカーソルの移動で閉じる。`<C-w>w` で入れる |
| 画面の中央 | ヘルプ、キーの一覧、`:popup` で縮めたタブ、`:ext sync` の内訳 | 開いたときから入っている。閉じるまで外れない |

フォーカスの入ったポップアップでは、次のキーとコマンドが使える。

| キー、コマンド | 動作 |
|---|---|
| `<Esc>`（ノーマルモード） | ポップアップを閉じる |
| `<C-w>w` | カーソルの近くのポップアップから元のタブへ戻る。ポップアップは残り、もう一度 `<C-w>w` で入る |
| `<CR>`（一覧） | カーソルの行の項目を選ぶ |
| `:tabopen`（`:tabop`） | ポップアップのバッファをタブへ広げる。写しなら、本物のファイルを開いて移動先へ移る |
| `:q` | ポップアップを閉じる |

`:popup`（`:popu`）は、カーソルのあるタブを中央のポップアップへ縮める（FR-70）。
縮めたタブは、ポップアップの中で通常どおり編集でき、未保存の変更もそのまま残る。
`:tabopen` でタブへ戻し、`<Esc>` で閉じるとタブの並びへ戻る。
タブが 1 つだけのときは縮められない。

tmux と herdr の中では、中央のポップアップを端末多重化ソフトのポップアップ（tmux の `display-popup`、herdr のプラグインのポップアップ）に出す（FR-67）。
ポップアップの中では本体が部品だけを描き、打ったキーは、ポップアップを開いたペインの client の操作になる。
端末多重化ソフトの外と、端末多重化ソフトのポップアップを開けなかったとき（herdr のプラグインを登録していないなど）は、エディタの画面の中央に描き、開けなかった理由をメッセージの行に出す。

拡張機能は、一覧や木を端末多重化ソフトのペインに開ける（`pane/open`。FR-115）。
ペインは、拡張機能へ最後に問い合わせたペインを分割して開き、中のキーと選んだ項目は、そのペインの client の操作になる。
ペインの中の `<Esc>` はペインを閉じない。閉じるには、ペインの中で `:q` を打つか、端末多重化ソフトでペインを閉じる。
端末多重化ソフトの外では、ペインに代えて画面の中央のポップアップに出し、`<Esc>` で閉じる（FR-123）。
拡張機能は、端末多重化ソフトの中でもペインに代えて中央のポップアップに出すことと、中央のポップアップで項目を選んだら閉じることを指定できる（FR-147。[ファイラ](#ファイラ)の `<Space>e`）。

### herdr のプラグインを登録する

herdr の中で中央のポップアップとペインを開くには、配布物の `herdr-plugin/` にある同梱のプラグインを、herdr に 1 回登録する。

```sh
herdr plugin link dist/vi-imitation-<コミット>-<OS>-<アーキテクチャ>/herdr-plugin
```

登録していないと、herdr の中でもエディタの画面の中央に描き、メッセージの行に「herdr のプラグインを登録してください」と出す。
プラグインは、本体が起動を頼んだコマンドを動かすだけで、本体の実行ファイルの場所を持たないので、配布物を入れ替えても登録し直さなくてよい。
外すときは `herdr plugin unlink vi-imitation` を打つ。

## ヘルプとキーの一覧

`:help {語}`（`:h {語}`）は、この文書を本体の中で開き、語の説明のある行へカーソルを置く（FR-89）。
文書は本体に埋め込んであるので、配布物の外でも読める。
語には、キーの列（`:help w`、`:help <C-o>`）、Ex コマンドの名前（`:help :w`、`:help :tabmove`）、設定の項目の名前（`:help registers.historySize`）を書く。
Vim と同じく、`:` の無い語はまずキーや設定の名前として引き、無ければ `:` を足したコマンドとして引く（`:help w` は `w` のキー、`:help :w` は `:w` のコマンド）。
`` `語` `` と書いた箇所のうち、見出し、表の最初の列、箇条書きの先頭を先に探し、次に文書のどこかの `` `語` ``、その次に語で始まる名前（`:help reg` は `:registers`）を探す。
語を省くと先頭を開く。
説明が見つからなければ、`E149: {語} のヘルプがありません` と出る。
文書の末尾には、本体のキーの一覧（キーの一覧と同じ説明文）と、読み込んだ拡張機能の操作の一覧（宣言ファイルの `contributes` と説明文）が付く。

キーの一覧は、接頭辞の後に `?` を打って出す（FR-90）。
`<leader>l?` は `<leader>l` で始まるキーと LSP の操作のキー（`K`、`gq`、`<C-]>` を含む）、`<leader>d?` は `<leader>d` で始まるキーと DAP の操作のキーの一覧である。
`<leader>?`、`g?`、`<C-w>?`、`s?` のように、それ自体は操作でない接頭辞の後の `?` でも、その接頭辞で始まるキーの一覧が出る。
`d` のように、それ自体が操作のキーの後の `?` は、これまでどおり `?` の検索になる（`d?foo<CR>`）。
`:keys {接頭辞}` は、接頭辞で始まるキーの一覧を出す（`:keys <Leader>d`）。
`<leader>l?` と違い、`:keys <Leader>l` は LSP の操作のキー（`K`、`gq`、`<C-]>`）を出さない。
接頭辞を省くと、すべてのキーと、回数、レジスター、マクロ、ドットリピートのキーを出す。

一覧の各行は、キー、モード（`n` はノーマル、`v` はビジュアル、`o` はオペレーターの後、`i` はインサート、`c` はコマンドライン）、出どころ、説明である。
出どころは、本体の既定のキーが `本体`、設定の `keymap` が `設定`、拡張機能が `拡張機能 <ID>` である。
設定の `keymap` の項目の説明は、`→ ` の後の展開先のキーの列である。
右辺に操作の ID を書いた項目の説明は、その操作の説明（[操作の ID の一覧](#操作の-id-の一覧)の説明の列）である。
設定の `keymap` が本体か拡張機能のキーと同じキーを割り当てていると、両方の行を出し、効かないほうの行の説明の後に `（設定の keymap が優先）` と付ける。
`keymap` で `s` を割り当てたときの `sa` のように、手前の部分を割り当てて打てなくなったキーにも同じ印を付ける。
一部のモードだけを割り当てたときは、`（v では設定の keymap が優先）` のように効かないモードを書く。
並びは、キーを大文字と小文字を区別せずに比べた順（`<Space>ld`、`<Space>lD`、`<Space>le` の順）で、`?` の行は末尾に置く。
拡張機能の操作の説明は、宣言ファイルの `contributes` の各項目に書いた `description` で、書いていなければ操作の ID を出す（[スクリプトの拡張機能](#スクリプトの拡張機能)の例）。

ヘルプとキーの一覧は、画面の中央のポップアップに出す（[ポップアップ](#ポップアップ)）。
どちらも編集を禁止したバッファ（[編集の禁止](#編集の禁止)）で、移動、検索、ヤンクはできる。
ヘルプの長い行は、窓の幅で折り返す。`:tabopen` でタブへ広げられる。
キーの一覧で `<CR>` を打つと、ポップアップを閉じてから、カーソルの行のキーを打ったのと同じく実行する。
設定の `keymap` を通すので、上書きされた本体の行を選ぶと、上書きした方が動く。
回数、レジスター、マクロ、ドットリピートの行と、ノーマルモードでないキーの行は、選んでも実行しない。
閉じるときは `<Esc>` を打つ。

## 標準の拡張機能

配布物に入っている標準の拡張機能は次のとおりである。
「起動の契機」は、本体が拡張機能のプロセスを起動するきっかけで、宣言ファイル（`extensions/<ID>/manifest.json`）の `activation` に書いてある。

| ID | 機能 | 起動の契機 | コマンド、キー | 別に要るもの |
|---|---|---|---|---|
| `treesitter` | 構文ハイライト（同梱は Go、JSON、JSONC。設定でほかの言語のパーサを足せる）、構文に沿ったテキストオブジェクト（Go） | Go か JSON か JSONC（`.jsonc`）のファイルか、設定でパーサを足したファイルの種類のファイルを開いたとき | `af`、`if`、`ac`、`ic`、`aa`、`ia` | 同梱の言語はなし。パーサを足すなら PATH の `git` と C コンパイラ（`cc`） |
| `line` | 行のテキストオブジェクト | `il`、`al` を打ったとき | `il`、`al` | なし |
| `lsp` | 診断と、LSP の操作（定義へ移動、ホバー、参照、シンボル、整形、名前の変更） | Go のファイルを開いたとき | [LSP と DAP の既定のキー](#lsp-と-dap-の既定のキー) | PATH の `gopls`。ほかの言語の言語サーバーは、設定で足して入れる（[LSP](#lsp)） |
| `dap` | デバッグ（DAP の操作） | 操作を呼んだとき | [LSP と DAP の既定のキー](#lsp-と-dap-の既定のキー) | PATH の `dlv` |
| `acp` | コーディングエージェントとの対話（ACP のクライアント） | コマンドを呼んだとき | `:acp.prompt`、`:acp.cancel` | ACP で話すエージェントと、その設定 |
| `mcp` | エディタの状態と編集を MCP のツールとして提供する | MCP のクライアントが最初に接続したとき | `:mcp.start` | MCP のクライアント |
| `git` | バッファのファイルを git の HEAD の版と見比べる | コマンドを呼んだとき | `<Leader>gd`、`:git.diffHead` | PATH の `git`、端末多重化ソフト（tmux、herdr） |
| `finder` | ファジーファインダー（ファイル、検索、タブ、行、キー、コマンド、レジスター、ジャンプリスト） | コマンドを呼んだとき | [ファジーファインダー](#ファジーファインダー) | 検索には PATH の `rg`。ファイルの一覧には `fd`（`fdfind`）か `rg` があれば使う |
| `filer` | ツリー型のファイラ | コマンドを呼んだとき | `<Leader>e`、`<Leader>E`、[ファイラ](#ファイラ) | gitignore されたものを隠すには PATH の `git`。分割したペインに出すには端末多重化ソフト（tmux、herdr） |

### tree-sitter による構文ハイライト

Go と JSON と JSONC（ファイルの種類 `jsonc`。拡張子 `.jsonc` のファイルで、設定ファイル `config.jsonc` を含む）のファイルを開くと、予約語、型、文字列、数値などに色が付く。
JSONC のファイルは JSON と同じ規則で色を付け、コメントにも色が付く。
コメントと末尾のカンマを含んでいても、その後ろの文字列や数値に色が付く。
編集すると、その場で色が付け直される。
同梱のこれらの言語には、設定は要らない。

Go のファイルでは、`af`/`if`（関数の全体と本体の内側）、`ac`/`ic`（型の宣言の全体と内側）、`aa`/`ia`（引数と区切りの `,`、引数 1 つ）を、`v`、`d`、`c` などと組み合わせて使える。
ドットリピートとマクロでも繰り返せる。

#### パーサを足す

同梱していない言語は、その言語の tree-sitter のパーサ（文法）の git のリポジトリと版を、設定ファイルの `extensions.settings.treesitter.parsers` に書いて足す（FR-142）。
例は tree-sitter-python の v0.25.0 を足すものである。

```jsonc
{
  "extensions": {
    "settings": {
      "treesitter": {
        "parsers": {
          // 名前は、取得したソースとビルドしたものを置くディレクトリと、ロックファイルの記録の名前になる
          "python": {
            "repo": "https://github.com/tree-sitter/tree-sitter-python",
            // タグ、ブランチ、コミットのハッシュ
            "version": "v0.25.0",
            // このパーサを使うファイルの種類（ステータス行の filetype の部品が出す名前）
            "fileTypes": ["python"],
          },
        },
      },
    },
  },
}
```

| 項目（仮） | 内容 | 省いたとき |
|---|---|---|
| `parsers.<名前>.repo` | パーサの git のリポジトリ。URL か絶対パス | 省けない |
| `parsers.<名前>.version` | 取り出す版。タグ、ブランチ、コミットのハッシュの順に探す | 省けない |
| `parsers.<名前>.fileTypes` | このパーサを使うファイルの種類の列 | 省けない |
| `parsers.<名前>.path` | `src/parser.c` のある `src/` の親のディレクトリ（リポジトリの中の相対パス）。1 つのリポジトリに複数の文法があるとき（tree-sitter-typescript の `typescript` と `tsx` など）に書く | リポジトリの最上位 |
| `parsers.<名前>.queries` | ハイライトのクエリのファイル（リポジトリの中の相対パス）の列。複数あれば順につなぐ | `path` の下の `queries/highlights.scm`、無ければ最上位の `queries/highlights.scm` |
| `compiler` | パーサをビルドする C コンパイラ | PATH の `cc` |

パーサを足したファイルの種類のファイルを初めて開くと、次の順に用意する。
用意の間も編集を続けられ、進み具合と失敗の理由はメッセージの行に出る。

1. `git` でリポジトリを `$XDG_DATA_HOME/vi-imitation/treesitter/src/<名前>/` に取得し、`version` のコミットを取り出す（メッセージ「パーサ python を取得している」）
2. C コンパイラで `src/parser.c` と、あれば `src/scanner.c` を共有ライブラリにして、ハイライトのクエリと一緒に `$XDG_DATA_HOME/vi-imitation/treesitter/parsers/<名前>/` の下に置く（「パーサ python をビルドしている」）。tree-sitter の CLI は要らない
3. 取り出したコミットを、設定ファイルと同じディレクトリのロックファイル `treesitter.lock.json` に記録し、読み込んで色を付ける（「パーサ python を用意した」）

2026-11-20 に、`make dist` の配布物の本体を tmux の中で起動し、上の例の設定で Python のファイルを開いて、取得とビルドの後に色が付くことと、起動し直して開いたときはビルドせずに色が付くことを確かめた（tmux 3.6、git 2.53.0、gcc 15.2.0）。

2 回目からは、ロックファイルの記録が設定の `repo` と `version` に合い、そのコミットのビルドしたものがあれば、取得もビルドもせずに読み込む。
ロックファイルを設定ファイルと一緒に dotfiles のリポジトリに置けば、別のマシンでも同じコミットを取得する。
`version` を変えると、次に開いたときに取得し直してビルドし、ロックファイルの記録を替える。
設定に並べなくなったパーサの記録は、次にどれかのパーサを取得したときにロックファイルから消す。
設定を変えたら、本体の server を起動し直す（tree-sitter の拡張機能は起動したときに設定を読む）。

色の付け方のクエリには、パーサのリポジトリの公式の `queries/highlights.scm` を使う。
nvim-treesitter のクエリは、Neovim だけが解釈する述語（`#lua-match?`）を使うので、そのままでは色を誤る。

次のときは、そのパーサだけを使わない。
ほかのパーサと同梱の言語は、そのまま使える。

- 設定の誤り（`repo` が相対パス、知らない項目など）。最初にファイルを開いたときにメッセージの行で知らせる
- 取得できない、ビルドできない、パーサの ABI の版が合わない（tree-sitter 0.25 が読めるのは 13 から 15）、共有ライブラリを読めない。理由をメッセージの行に出し、詳しい出力（C コンパイラの誤りなど）は拡張機能のログに書く
- 2 つ以上のパーサに同じファイルの種類を書いたとき。その種類にはどれも使わない

用意に失敗したパーサは、server を起動し直すまで試し直さない。
ネットワークに出られなくて取得できなかったときも、出られるようになってから同じ server でファイルを開き直すだけでは使えないので、server を起動し直す。

同梱の言語（`go`、`json`、`jsonc`）と同じファイルの種類をパーサに書くと、パーサのほうを使う。
そのパーサを用意できなければ、同梱の言語を使う。
同梱の言語に無いファイルの種類には、テキストオブジェクト（`af` など）は無い。
`go` をパーサに書いたときは、色はパーサで付け、`af` などは同梱の Go のクエリで動く。
足したファイルの種類のために、設定の `extensions.activation` を書く必要はない（本体が `fileTypes` から起動の契機を足す）。
パーサの項目に `fileTypes` が無い（`filetypes` と綴りを誤ったなど）と、そのパーサでは拡張機能が起動しないので、本体が起動したときに設定の誤りとして知らせる。
拡張子からファイルの種類への既定の対応に無い言語（`.lua` など）は、設定の `fileTypes` の対応表（`{ ".lua": "lua" }`）にも書く。パーサの `fileTypes` には、その対応表の右辺の名前を書く。
C++ で書いた外部スキャナー（`src/scanner.cc`）を持つパーサはビルドしない。

### 行のテキストオブジェクト

`il` はカーソルの行から行頭と行末の空白を除いた範囲、`al` は行全体（行末の改行を含めない）を指すテキストオブジェクトである。
`dil` で行の中身を消し、行そのものは残す。

### LSP

Go のファイルを開くと、gopls を子プロセスとして起動する。
gopls は PATH から探す。
設定は要らない。

| 操作 | 結果 |
|---|---|
| 編集して誤りを作る | 行の左に `E`、誤りの箇所に下線が出る。カーソルをその行に置くと、最下行に `E compiler: declared and not used: y` のように内容が出る |
| `K` か `<leader>lh` | カーソルの位置の識別子の説明をポップアップで出す。次のキーで閉じる。`<C-w>w` でフォーカスを入れるとスクロールでき、`<Esc>` で閉じる |
| `<C-]>` か `<leader>lD` | 定義へ移動する。別のファイル（標準ライブラリの `fmt/print.go` など）の定義なら、そのファイルを開く。定義のファイルは新しいタブで開く。`<C-o>` で元の位置へ戻り、`<C-i>` で定義へ進み直す（[タブとジャンプ](#タブとジャンプ)） |
| `<leader>ld` | 定義の周りの 20 行の写しを、移らずにポップアップで出す。`<C-w>w` の後の `:tabopen` で定義へ移る |
| `gqq`、`gqip` など | 整形する。gopls（v0.24.0）は範囲の整形に対応しないので、全体を整形する。`u` で整形の前に戻せる |
| `:rename 新しい名前` | カーソルの位置の識別子の名前を変える。タブで開いていないファイルは、タブを増やさずに変えて書き出し、変えたファイルの一覧を最下行に出す。編集を禁止したファイルなど、変えられないファイルが 1 つでもあれば、どのファイルも変えずにそのファイルを知らせる。`u` で戻るのはカーソルのあるタブのファイルだけで、書き出したファイルは戻らない（戻すには、もう一度 `:rename 元の名前` を打つ） |
| `:w` | 書き出す前に整形する |

ほかの操作は [LSP と DAP の既定のキー](#lsp-と-dap-の既定のキー) にある。

言語サーバーを変える、保存の前の整形をやめるといった設定は、`extensions.settings.lsp` に書く。
項目は `go doc -u ./cmd/vi-imitation-ext-lsp settings` と `serverConfig` にある。

#### 言語を足し、言語サーバーを mise で入れる

既定の言語サーバーは Go の gopls だけである。
ほかの言語は、`extensions.settings.lsp.servers` にファイルの種類（`python`、`sh` など）ごとの言語サーバーを書き、`extensions.activation.lsp` にそのファイルの種類の起動の契機を足す。
言語サーバーの実行ファイルは、vi-imitation が取ってこない。
[mise](https://mise.jdx.dev/) で入れる前提で、言語サーバーごとに mise の道具の ID（`mise`）を書いておくと、見つからないときに入れ方を案内する。

```jsonc
{
  "extensions": {
    "settings": {
      "lsp": {
        "servers": {
          "python": {
            "command": "pyright-langserver",
            "args": ["--stdio"],
            "rootMarkers": ["pyproject.toml", "setup.py", ".git"],
            // 版は、自分の mise の設定（~/.config/mise/config.toml）に書いた版に合わせる
            "mise": "npm:pyright@1.1.410",
          },
        },
        // true にすると、見つからないときに mise に入れさせてから起動する（既定は false で、案内だけ）
        // "autoInstall": true,
      },
    },
    "activation": { "lsp": [{ "on": "filetype", "pattern": "python" }] },
  },
}
```

`mise` には固定した版を書く。`npm:pyright` のように版を省くか、`npm:pyright@latest` と書くと、設定の誤りになる（どちらも、自動で入れたときにその時点の最新版が入るため）。

`command` が PATH で見つかれば、`mise` と `autoInstall` に関わらず、そのまま起動する。
ただし、見つかったのが mise の shim で、`mise which <command>` が失敗する（版が決まっていない）ときは、見つからなかったものとして扱う。
見つからないときの動きは、`autoInstall` で選ぶ。

| `autoInstall` | 見つからないときの動き |
|---|---|
| `false`（既定） | メッセージの行に `言語サーバー pyright-langserver が見つからない。mise use -g npm:pyright@1.1.410 で入れられる` と出し、何も入れない。同じ言語サーバーの案内は、LSP の拡張機能が動いている間に 2 回出さない（拡張機能が異常終了から起動し直すと、もう一度出しうる）。案内のとおりに入れた後、そのファイルの種類の別のファイルを開くと、言語サーバーを起動する（本体を起動し直してもよい） |
| `true` | mise で道具を入れてから、`mise exec npm:pyright@1.1.410 -- pyright-langserver --stdio` で起動する。入れている間は、メッセージの行に `mise で npm:pyright@1.1.410 を入れている` と mise の出力の行を出す。mise が PATH に無ければ、案内に戻る |

`autoInstall` は、`servers` の言語サーバーごとにも書け、そちらが優先する。
`autoInstall` で入れた道具は mise の設定ファイルに書かれない。
mise の shim のディレクトリを PATH に置いていると、その道具の shim は PATH で見つかるが、版が決まっていないので起動できない（shim は `No version is set for shim` で終わる）。
vi-imitation は上のとおり、そのような shim を使わずに `mise exec` で起動する。
入れるのに失敗したとき（ネットワークに出られない、プロジェクトの `mise.toml` を信頼していない、など）は、mise と、mise が起動した npm などの誤りの行をメッセージの行に出す。
信頼していない `mise.toml` で止まったときは、内容を確かめてから `mise trust` を打つ。vi-imitation は、利用者の代わりに信頼しない。

### DAP によるデバッグ

デバッグアダプターは、既定で Delve（`dlv dap`）を使う。
`dlv` は PATH から探す。
設定は要らない。

1. 止めたい行で `<leader>db` を打つ。行の左に `B` が出る。もう一度打つと消える。条件を付けるときは `:breakpoint.conditional i == 3` を打つ
2. `<leader>dc` を打つ。デバッグを始めたバッファのファイルのあるディレクトリのパッケージをビルドして実行し、ブレークポイントで止まる。止まった行の左に `=>` が出て、最下行に「停止した（breakpoint）」が出る
3. `<leader>dn`、`<leader>di`、`<leader>do` で 1 行ずつ進む。`=>` とカーソルが止まった位置へ移る
4. `<leader>dc` で続ける。デバッグ対象が終わると「デバッグを終了した」が出る
5. 途中でやめるときは `<leader>dt` を打つ。`<leader>dr` は終えてから始め直し、`<leader>dl` は前回と同じ設定で始める

デバッグ対象の標準出力（`fmt.Println` の出力など）と Delve の出力は、`dap` の欄に出る（`:output dap`）。
デバッグが失敗したときの理由も、この欄で読む。

デバッグアダプターや launch の引数を変える設定は、`extensions.settings.dap` に書く。
項目と例は `go doc -u ./cmd/vi-imitation-ext-dap settings` にある。

### ACP のエージェント

ACP（Agent Client Protocol）で話すコーディングエージェントを子プロセスとして起動し、プロンプトを送る。
エージェントには既定が無いので、`extensions.settings.acp.agent` に起動のコマンドを書く（[設定ファイル](#設定ファイル)の例）。
`agent` には `command`、`args`、`env` を書ける（`go doc -u ./cmd/vi-imitation-ext-acp settings`）。
書かずに `:acp.prompt` を打つと、「エージェントが設定されていない（設定の agent.command に書く）」と出る。
この `agent.command` は、設定ファイルの `extensions.settings.acp.agent.command` のことである。

| 操作 | 結果 |
|---|---|
| `:acp.prompt <プロンプト>` | プロンプトを送る。最初の 1 回でエージェントを起動する |
| `:acp.cancel` | 処理中のプロンプトを取り消す。`acp` の欄に `[cancelled]` が出る |

返答は `acp` の欄に書き足され、欄を開いていなければ画面に出ない。
欄を開いてから送るときは、`:output|acp.prompt notes.txt の2行目の内容をそのまま答えて` のように続けて打つ。
欄には、送ったプロンプト（`> ` で始まる行）、エージェントが使ったツールの要約（`[tool]` で始まる行）、返答、終わりの印（`[end_turn]`）が順に出る。

エージェントがツールの実行の許可を求めると、画面の下に問いと `[1] Allow once` のような選択肢が出る。
番号のキーで選び、`<Esc>` で取り消す。
選んだ答えは、最下行に「問いに「Allow once」と答えました」のように出る。
エージェントがツールを続けて呼ぶと、問いも続けて届く。
2 件目からは最下行の先頭に `問い 2:` のように何件目かが出て、前の問いへの答えが問いの上に出る。
問いを出している間は、番号と `<Esc>` 以外のキー（`:output` など）は捨てられ、捨てた数が問いの上に出る。
答え終わってから打ち直す。

### git の HEAD の版と見比べる

`<Leader>gd`（`:git.diffHead`）を打つと、カーソルのあるバッファのファイルの、git の HEAD の版を、いまのペインの右に開いた新しいペインの client のタブに出し、ファイルのタブと見比べる（[2 つのファイルを見比べる](#2-つのファイルを見比べる)）。
HEAD の版のタブの名前は `HEAD:<リポジトリの根からのパス>` で、編集を禁止したファイルでないバッファである。
`do` で HEAD の内容をファイルへ取り込める。HEAD の版への `dp` は E21 になる。
見比べるのは保存していない変更を含むバッファの中身で、ファイルを書き出さなくてよい。
HEAD の版は、作業ツリーへ取り出すときと同じ改行の変換（`.gitattributes` の `eol`、`core.autocrlf`）を通して読む。シンボリックリンクは、リンクの先のファイルの HEAD の版と見比べる。

- 見比べる相手は HEAD（最後のコミット）で、index（`git add` した内容）ではない。gitsigns の `:Gitsigns diffthis`（nvim でよく `<leader>gd` に割り当てる）は、既定では index と見比べるので、`git add` した後の結果が違う
- 右のペインの client が繋ぐのを待っている間（最長 30 秒）にもう一度打つと、待っていることを知らせて断る

- 端末多重化ソフトの外では見比べられない（メッセージの行にそのことが出る）
- git のリポジトリの外のファイル、HEAD に無いファイル（まだコミットしていないファイル）、ファイルでないバッファでは使えない
- 本体を `git commit` の `GIT_EDITOR` にして起動したときも、環境変数の `GIT_DIR` などは使わず、ファイルを含むリポジトリの HEAD を読む
- 行の印、blame などは持たない（要件定義 7 章の未決事項）

### MCP のサーバー

MCP（Model Context Protocol）のクライアント（Claude Code などのコーディングエージェント）から、起動中のエディタのバッファを読み書きできる。
ツールの一覧は `go doc ./cmd/vi-imitation-ext-mcp` と基本設計 6.2 にある。

本体は、起動時から Unix ドメインソケットで MCP のクライアントの接続を待つ。
ソケットのパスは起動ごとに変わる。
`:mcp.start` を打つと、最下行にパスが出る。

```text
MCP のサーバーは /tmp/vi-imitation-2518129-2071416057/mcp.sock で接続を待っています
```

パスは `$XDG_RUNTIME_DIR/vi-imitation/` の下に作り、そこに作れないときは一時ディレクトリ（`TMPDIR` か `/tmp`）の下に作る（`internal/exthost` の `NewMCPSocket`）。
上の例は、`XDG_RUNTIME_DIR` が長くてソケットのパスの上限を超えたので、`/tmp` の下に作ったものである。

MCP のクライアントには、配布物の中継コマンド `vi-imitation-mcp-bridge` を、標準入出力で話す MCP のサーバーとして登録する。
中継コマンドの `--socket` に、`:mcp.start` が出したパスを渡す。
Claude Code では、次の JSON を `--mcp-config` に渡して確かめた。

```json
{
  "mcpServers": {
    "vi-imitation": {
      "type": "stdio",
      "command": "/path/to/vi-imitation-<コミット>-<OS>-<アーキテクチャ>/vi-imitation-mcp-bridge",
      "args": ["--socket", "/tmp/vi-imitation-2518129-2071416057/mcp.sock"]
    }
  }
}
```

```sh
echo 'vi-imitation の MCP ツールで notes.txt の 1 行目の先頭に "MCP: " を挿入して' |
  claude -p --mcp-config mcp.json \
    --allowedTools mcp__vi-imitation__list_buffers mcp__vi-imitation__edit_buffer
```

`--allowedTools` で、使わせる MCP のツールを許可しておく。
`--allowedTools` は後に続く引数をすべて値として取るので、プロンプトは引数でなく標準入力で渡す（引数で渡すと「Input must be provided either through stdin or as a prompt argument」で失敗した）。
Claude Code が `edit_buffer` のツールを呼ぶと、エディタの画面のバッファが変わる。
ディスクのファイルは変わらないので、エディタで `:w` を打って保存する。
ソケットのパスはエディタを起動し直すと変わるので、そのたびに `--socket` を書き換える。

### ファジーファインダー

画面の中央に、入力の行と一覧の窓を開き、打つたびに一覧を絞り込む（fzf と同じ使い方）。
キーは `<Leader>` が既定の `<Space>` のときのものである。

| キー | コマンド | 一覧 | 選んだとき |
|---|---|---|---|
| `<Space>ff` | `:finder.files` | 作業ディレクトリの下のファイル | 開く |
| `<Space>fg` | `:finder.grep` | 作業ディレクトリの下の `rg` の検索の結果。入力は `rg` の正規表現で、打つたびに検索し直す | その行と列へ移る |
| `<Space>f.` | `:finder.grepDir` | いまのファイルのディレクトリの下の検索の結果 | 同上 |
| `<Space>fw` | `:finder.grepWord` | カーソルの下の語を入れた検索の結果 | 同上 |
| `<Space>fb` | `:finder.buffers` | すべての client のタブ | そのタブへ移る（別の client のタブならそのペインへ） |
| `<Space>fl` | `:finder.lines` | いまのバッファの行 | その行へ移る |
| `<Space>fj` | `:finder.jumps` | ジャンプリスト（新しい順） | その位置へ移る |
| `<Space>fr` | `:finder.registers` | レジスターと履歴（`:registers` と同じ） | カーソルの後に貼る（`p` と同じ位置） |
| `<Space>fk` | `:finder.keys` | キーの一覧 | そのキーを打ったのと同じく実行する（[キーの一覧](#ヘルプとキーの一覧)の `<CR>` と同じ） |
| `<Space>fc` | `:finder.commands` | コマンド | `:名前<CR>` を打ったのと同じく実行する（引数の要るコマンドは誤りになる） |
| `<Space>fp` | `:finder.resume` | 最後に開いた一覧を、最後の入力で開き直す | |

窓の中のキーは次のとおりである。
開いた時点でインサートモードになっている。

| キー | 動作 |
|---|---|
| 文字のキー、`<BS>` など | 入力の行を編集する |
| `<C-n>`、`<C-j>`、`<Down>` / `<C-p>`、`<C-k>`、`<Up>` | 一覧の次の項目 / 前の項目へ |
| `<CR>` | 一覧のカーソルの項目を選ぶ。打った入力への一覧がまだ届いていなければ、届いてから選ぶ |
| `<Esc>` | 閉じる（インサートモードでも 1 回で閉じる） |

ファインダーで打ったキーはマクロに記録され、再生するとファインダーを開いて同じく打つ（`qa<Space>ffbeta<CR>q` を `@a` で再生すると、また `beta` で絞って開く）。

入力は空白で区切った語の並びで、すべての語を含む項目が残る。
語の文字は、項目の中に順に現れればよい（間に他の文字があってよい）。
語に大文字が無ければ、大文字と小文字を区別しない。
一致した文字には `finder.match` の色（既定は太字と下線）が付く。
見出しの「12/345」は、一致した数と候補の数である。
一覧に出すのは上位 300 件までである。

ファイルの一覧と検索は、隠しファイル（`.` で始まるもの）を含め、`.git` の下を除く。
`fd` と `rg` は `.gitignore` に書いたファイルを除くが、どちらも無いときは除かない。
設定は `extensions.settings` の `finder` に書く（どれも省いてよい）。

```jsonc
"finder": { "hidden": false, "limit": 1000 }
```

### ファイラ

作業ディレクトリの下のディレクトリとファイルを木で出し、選んだファイルを開く（nvim の neo-tree と同じ使い方）。
キーは `<Leader>` が既定の `<Space>` のときのものである。

| キー | コマンド | 出す場所 | ファイルを選んだとき |
|---|---|---|---|
| `<Space>e` | `:filer.popup` | 画面の中央のポップアップ | ポップアップを閉じて、元のタブでそのファイルを開く |
| `<Space>E` | `:filer.pane` | 端末多重化ソフトで右に分割したペイン（元のペインの幅の 30%） | ペインを残して、ペインを開いた client のタブでそのファイルを開き、フォーカスをその client のペインへ戻す |

どちらも、開いたときに、カーソルのあるファイルまで木を開いて、その行にカーソルを置く。
開いているときにもう一度打つと閉じる。
`<Space>E` のペインは、`:q` か端末多重化ソフトで閉じるまで残り、元の client で別のファイルへ移るたびに、そのファイルまで木を開いてカーソルを置く。
端末多重化ソフトの外では、`<Space>E` も画面の中央のポップアップに出し、ファイルを選んだら閉じる。

木の中のキーは次のとおりである。
ほかのノーマルモードのキー（`j`、`k`、`gg`、`/`、`n` など）もそのまま効くので、絞り込むときは `/` で探す。

| キー | 動作 |
|---|---|
| `l`、`<CR>` | ディレクトリなら開く（開いていれば閉じる）。ファイルなら選ぶ |
| `h` | 開いたディレクトリなら閉じる。それ以外なら、親のディレクトリを閉じてそこへ移る |
| `H` | gitignore されたものを見せるかを切り替える（見せている間は見出しに「gitignore されたものも表示」と出る） |
| `<Esc>` | 中央のポップアップを閉じる（ペインは閉じない） |

隠しファイル（`.` で始まるもの。`.git` を含む）は見せ、gitignore されたものは隠す。
gitignore の判定には PATH の `git` を使い、git のリポジトリの外と、`git` が無いときは、何も隠さない。
並びは、ディレクトリが先で、その中は名前の順（大文字が小文字より先）である。
ディレクトリの中身は開いたときに読むので、大きなディレクトリ（`node_modules` など）も開ける。
木を開いた後にファイラの外で作ったり消したりしたファイルは、そのディレクトリを閉じて開き直しても木に入らない（ファイラを開き直すと入る）。
ただし、`<Space>E` のペインで、元の client が木に無いファイルへ移ったときは、そのファイルのディレクトリを読み直す。
ファイルの作成、名前の変更、削除は、まだできない。

## 拡張機能の取得（`:ext sync`）

git のリポジトリにある拡張機能を、設定ファイルに並べて取得できる（基本設計 5.6）。
取得したソースを `go build` でビルドするので、PATH に `go` と `git` が要る。

```jsonc
{
  "extensions": {
    "install": [
      // name は $XDG_DATA_HOME/vi-imitation/extensions/<name>/ のディレクトリの名前になる
      { "name": "surround", "repo": "https://github.com/example/vi-imitation-surround", "version": "v0.1.0" }
    ]
  }
}
```

`version` には、タグ、ブランチ、コミットのハッシュを書ける。
`repo` は URL か絶対パスにする。
取得元のリポジトリは、最上位に宣言ファイル `manifest.json` と `main` パッケージを置き、宣言ファイルの `command` を `./<実行ファイルの名前>` にする。

| コマンド | 働き |
|---|---|
| `:ext sync` | 設定ファイルを読み直し、並べた拡張機能を取得してビルドし、入れる。版を変えたものは入れ替え、並べなくなったものは削除する |
| `:ext update` | `:ext sync` と同じだが、ロックファイルの記録を使わずに版を解決し直す。ブランチの版を最新にするときに使う |

取得したコミットは、設定ファイルの隣の `extensions.lock.json` に記録する。
設定ファイルと一緒に dotfiles のリポジトリで管理すると、別のマシンで `:ext sync` を打ったときに同じコミットを取り出す。

取得の間も編集を続けられる。
進み具合はメッセージの行に出て、終わると件数が出る。
失敗の理由と警告は、画面の中央のポップアップに出る（`<Esc>` で閉じる）。

入れたものがいつから使えるかは、次のとおりである。

- 新しく入れた拡張機能は、本体を起動し直すと使える
- 版を変えて入れ替えた拡張機能は、動いていれば止まり、新しい版が起動し直す。開いているファイルの種類で起動する拡張機能（LSP、構文の色）はすぐに、それ以外は次にその拡張機能の操作を呼んだときに起動する
- 宣言ファイル（キー、コマンド、起動の契機）が変わった版に入れ替えたときは、本体を起動し直すまで前の版が動き続ける
- 削除した拡張機能は、動いていれば止まる
- 止めて入れ替えるのは、`:ext sync` を打った server の拡張機能だけである。他の tmux の window の server は、起動し直すまで前の版を使う

設定ファイルの `extensions.install` に誤りがあるとき（JSONC の構文の誤りを含む）は、その回の `:ext sync` は何も削除しない。
`extensions/<name>/` に手で置いたディレクトリは、上書きも削除もしない。

## スクリプトの拡張機能

Go のソースファイル 1 つを、コンパイルせずに拡張機能として動かせる。
配布物の `vi-imitation-ext-script` が、Go のインタープリター yaegi でスクリプトを実行する。

`$XDG_DATA_HOME/vi-imitation/extensions/<ID>/`（`XDG_DATA_HOME` が空なら `~/.local/share/vi-imitation/extensions/<ID>/`）に、宣言ファイルとスクリプトを置く。
次の例は、カーソルの行の前に `// hello` の行を入れるコマンド `hello.insert` である。

`extensions/hello/manifest.json`:

```json
{
  "id": "hello",
  "command": "/path/to/vi-imitation-<コミット>-<OS>-<アーキテクチャ>/vi-imitation-ext-script",
  "args": ["hello.go"],
  "activation": [{ "on": "command", "id": "hello.insert" }],
  "contributes": { "commands": [{ "id": "hello.insert", "description": "カーソルの行の前に // hello の行を入れる" }] }
}
```

`contributes` の各項目の `description` は、キーの一覧と `:help` に出る説明文である。省いてもよい。

`extensions/hello/hello.go`:

```go
package main

import (
	"context"
	"log"

	"github.com/piplupflipper/vi-imitation/ext"
	"github.com/piplupflipper/vi-imitation/ext/server"
)

func main() {
	s := server.New()
	s.Command("hello.insert", insert)
	if err := s.Run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

// insert は、カーソルの行の前に "// hello" の行を入れる。
func insert(_ context.Context, _ *server.Buffer, p *ext.CommandExecuteParams) (ext.CommandExecuteResult, error) {
	pos := ext.Position{Line: p.Cursor.Line, Byte: 0}
	edit := ext.Edit{Range: ext.Range{Start: pos, End: pos}, Text: "// hello\n"}
	return ext.CommandExecuteResult{OK: true, Transaction: &ext.Transaction{Edits: []ext.Edit{edit}}}, nil
}
```

本体を起動し直して `:hello.insert` を打つと、カーソルの行の前に `// hello` が入る。

`command` には、配布物の `vi-imitation-ext-script` の絶対パスを書く。
配布物の場所を書かずに指す方法は未決である（基本設計 10 章）。
`args` の相対パスは、宣言ファイルのディレクトリから解決される。
モーション、テキストオブジェクト、オペレーターの足し方と、yaegi の制限は `go doc ./cmd/vi-imitation-ext-script` にある。
