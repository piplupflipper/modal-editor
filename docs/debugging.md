# デバッグの案内

vi-imitation が思うように動かないとき、画面が崩れるときに、利用者が自分の環境で確かめる手順と、不具合を報告するときに添えるものを書く。
本体に埋め込んであり、`vi-imitation --docs debugging` でも読める。

この文書の手順は、2026-11-21 に `make dist` で作った配布物の本体を tmux 3.6 の中で起動して確かめた。
コマンドの例の `~/vi.log` と `a.go` は、手元のパスに読み替える。

## 記録（ログ）

本体は、`--log` に書いたファイルにだけ記録を追記する。
`--log` を書かなければ、記録をどこにも書かない（画面を崩さないため、標準エラー出力にも書かない）。

```sh
vi-imitation --log ~/vi.log a.go
```

記録は 1 行 1 件で、`time=… level=INFO msg="…"` の形である。
次のものが残る。

| 中身 | 行の例 |
|---|---|
| server のソケットのパスと起動 | `msg="server のソケット" unit=/tmp/tmux-1000/default:@0 path=/run/user/1000/vi-imitation/u-7728c370cf16ff14/server.sock` |
| 拡張機能の状態の変化 | `msg="extension state changed" extension=lsp state=running` |
| 拡張機能の標準エラー出力（1 行ずつ） | `msg="extension stderr" extension=lsp line="vi-imitation-ext-lsp: … gopls: exec: \"gopls\": executable file not found in $PATH"` |
| 編集の client が繋いだこと | `msg="編集の client を足した" client=2 pane=%1` |

記録の詳しさを変える手段は無い。

### tmux と herdr の中での記録

tmux の中では window ごとに、herdr の中ではタブごとに、1 つの server（編集の状態と拡張機能を持つプロセス）が動き、同じ window で起動した 2 つ目からの `vi-imitation` はその server に繋ぐだけである。
server の記録は、server を起動した最初の `vi-imitation` の `--log` のファイルに書く。
2 つ目からの `vi-imitation` の `--log` のファイルには、その client の記録（ソケットのパスなど）だけが残り、拡張機能の標準エラー出力は残らない。

拡張機能の記録を取るには、次のどちらかにする。

- その window の `vi-imitation` をすべて `:qa` で終える（最後の編集の client が終わると server も終わる）。その後に `--log` を付けて起動し直す
- `--new` を付けて起動する。同じ window の server に繋がず、server と client を 1 つのプロセスで動かすので、記録はすべてそのファイルに書く

```sh
vi-imitation --new --log ~/vi.log a.go
```

`:!` で動かしたシェルの中で起動した `vi-imitation` も、環境変数 `VI_IMITATION_SOCKET` の server に繋ぐ（`--new` を付けると繋がない）。

## エディタの中で見られるもの

| コマンド | 見られるもの |
|---|---|
| `:messages`（`:mes`） | 設定の誤り、拡張機能の誤りと知らせ、拡張機能の異常終了を、古い順に 1 件 1 行で出す。起動の後にメッセージの行に出た誤りは、ここで全部を読める |
| `:output [名前]`（`:out`） | 拡張機能が書き足す出力の欄（DAP の `dap`、ACP の `acp`）を開く |
| `:option.get 名前` | 設定の項目の今の値を出す（`:option.get tab.width`）。範囲がバッファごとの項目は、カーソルのあるバッファの値である |
| `:help 読み込んだ拡張機能の操作` | 読み込んだ拡張機能のコマンド、オペレーター、モーション、テキストオブジェクトの一覧。拡張機能を読み込めたかを確かめられる |
| `:keys` | 本体、設定、拡張機能のキーの一覧と、設定の `keymap` が上書きしたキー |

## 設定が効かないとき

設定ファイルの誤りは、起動の後にメッセージの行へ `config.jsonc:2:19: fixendofline は true か false にする` の形で出る。
誤りのある項目は使わず、ほかの項目は効く。
知らない項目も誤りになるので、綴りの誤りはここで分かる。
設定ファイルは `$XDG_CONFIG_HOME/vi-imitation/config.jsonc`（`XDG_CONFIG_HOME` が空なら `~/.config/vi-imitation/config.jsonc`）で、変えたら起動し直す。
書き方は `vi-imitation --docs config` にある。

`.editorconfig` の正しくない値（`indent_size = x` など）は、メッセージに出さずに記録に残し、その項目を使わない。
字下げの幅が思った値にならないときは、`:option.get indent.width` で今の値を見て、`vi-imitation --docs usage 値の決まり方` の順に当てはめる。

## 拡張機能が動かないとき

次の順に確かめる。

1. 起動した本体の隣に `extensions/` があるか。本体は、自分の実行ファイルの隣の `extensions/<名前>/manifest.json` と、`$XDG_DATA_HOME/vi-imitation/extensions/<名前>/manifest.json`（`XDG_DATA_HOME` が空なら `~/.local/share/vi-imitation/extensions/`）だけを読む。`make build` で作った `bin/vi-imitation` の隣には `extensions/` が無いので、標準の拡張機能を読まない
2. 宣言ファイルの誤りが出ていないか。誤りのある宣言ファイルは使わず、起動の後に `bad/manifest.json: 宣言ファイルの誤り: command が空である` の形で出る（`:messages` でも読める）
3. 起動の契機が成り立っているか。拡張機能は、宣言ファイルの `activation`（ファイルの種類、コマンド、操作）が成り立つまで起動しない。ファイルの種類は、ステータス行か `:option.get filetype` で見る。名前の無いバッファは `:set filetype=go` で決められる
4. 記録（`--log`）に、その拡張機能の `extension state changed` と `extension stderr` の行があるか。異常で終わった拡張機能は、メッセージの行に `拡張機能 <ID> が異常終了しました` と出て、起動し直す。5 回続けて失敗すると `拡張機能 <ID> は失敗が続いたので無効にしました` と出て、それからは起動し直さない

### 言語サーバーが見つからないとき

LSP の拡張機能は、言語サーバー（既定は Go の gopls）を PATH から探す。
見つからなければ、メッセージの行に `言語サーバー gopls を起動できない: exec: "gopls": executable file not found in $PATH` と出る。
`vi-imitation` を起動したシェルで `command -v gopls` を打ち、PATH にあるかを確かめる。

言語サーバーを mise で入れるときは、設定に mise の道具の ID を書くと、見つからないときに入れ方を案内する（`vi-imitation --docs usage 言語を足し、言語サーバーを mise で入れる`）。
mise の shim が PATH にあり、版が決まっていない（`mise which <コマンド>` が失敗する）ときは、見つからなかったものとして扱う。

## 画面が崩れるとき

まず、どこで崩れているかを分ける。
tmux の中なら、崩れたペインで次を打つ。

```sh
tmux capture-pane -e -p -t <ペイン> > ~/pane.txt
```

`tmux capture-pane` は、tmux が持っている画面の中身（色の制御シーケンスを含む）を出す。
`cat ~/pane.txt` で正しく見えるのに端末の画面が崩れているなら、崩れているのは tmux から外側の端末までの間である。

本体が端末へ送ったバイト列は、`script` で記録できる。

```sh
script -q -c 'vi-imitation a.go' ~/typescript.txt
```

### 全角の文字を消した跡が縞に残る

2026-11-20 に、tmux 3.6 の中でだけ、全角の文字を消した行に縞が残ることがあった（#297）。
tmux は、行末での折り返しを止めたペインで、全角の文字の右の半分の列への空白を外側の端末へ送らずに飛ばす。
外側の端末が全角の文字の右の半分を消さないと、それが残って縞に見える。
PR #302（2026-11-20 にマージ）で、本体は既定の色の空白を `ESC [ K` と `ESC [ n X` で消すようにした。
それより前に作った配布物なら、新しい配布物に替える。
新しい配布物でも残るなら、上の `tmux capture-pane` の結果と、端末の名前と版を添えて報告する。

### 曖昧な幅の文字で列がずれる

本体は、Unicode で幅が曖昧と定められた文字（罫線の `│`、`●`、Nerd Font のアイコン）を 1 列と数える（Vim の `'ambiwidth'` の既定の `single` と同じ）。
端末を「曖昧な幅の文字を 2 列で描く」設定にしていると、それより右の列がずれる。
端末の設定を 1 列にするか、設定の `symbols` で記号を ASCII にし（`vi-imitation --docs usage 記号`）、`icons.show` を `false` にする。

### Ctrl-H が効かない

Ctrl-H は、端末からも tmux の中でも `^H`（0x08）で届き、本体は `<BS>` と読む。
このため `keymap` の `<C-h>` の割り当ては効かず、Ctrl-H は `<BS>` の割り当てのとおりに働く。
Ctrl-H に別の操作をさせたいときは、`<BS>` に割り当てる（`<BS>` も同じ操作になる）。

### 色が違う

本体は、端末が 24 ビットの色を出せるかを確かめずに、設定の `#rrggbb` の色を 24 ビットの色で送る。
24 ビットの色を出せない端末では、色を 0〜255 の 256 色の番号で書く（`vi-imitation --docs usage 配色`）。
tmux の中では、tmux が外側の端末へ 24 ビットの色を送るかを次で確かめる。

```sh
tmux display -p '#{client_termname} #{client_termfeatures}'
```

出力の `client_termfeatures` に `RGB` が無ければ、tmux は 24 ビットの色を外側へ送らない。
そのときは、`~/.tmux.conf` に `set -as terminal-features ',<client_termname の値>:RGB'` を書いて足す（tmux の `terminal-features` の設定。2026-11-21 に tmux 3.6 にこの設定があることを確かめた）。

## 不具合を報告するときに添えるもの

- `vi-imitation --version` の出力
- OS、端末の名前と版、`echo $TERM` の出力
- 端末多重化ソフトの中なら、`tmux -V` の出力か herdr の版。tmux なら `tmux display -p '#{client_termname} #{client_termfeatures}'` の出力
- 起動のしかた（引数、tmux や herdr の中か外か、`:!` の中か）と、起こしたキーの順
- 画面の崩れなら、崩れたときの `tmux capture-pane -e -p -t <ペイン>` の出力と、端末の画面の写真
- `--log` の記録（tmux と herdr の中では、server を起動した `vi-imitation` の記録。上の「tmux と herdr の中での記録」）
- 設定ファイル（`config.jsonc`）と、足した拡張機能の宣言ファイル
- `:messages` の出力

記録と画面の中身には、開いたファイルのパスと中身が入る。
公開の場に貼る前に、見せたくない部分を消す。
