# modal-editor

Vim のキーバインドを持つ、ターミナル用のモーダルエディタ（仮称）。
実装は未着手で、現在は開発環境と設計の文書だけがある。

- 要件定義: [docs/requirements.md](docs/requirements.md)
- 基本設計: [docs/basic-design.md](docs/basic-design.md)
- 要件ごとの進捗: [docs/progress.md](docs/progress.md)（`make progress` が生成する）
- Vim との違い（直さないと決めた食い違いと、見つけた未実装の機能）: [docs/vim-differences.md](docs/vim-differences.md)

詳細設計の文書は作らず、コードと doc コメントを詳細設計として扱う。

## 必要なもの

- Go（バージョンは [go.mod](go.mod) を参照）
- make
- C コンパイラ（gcc など）。tree-sitter の拡張機能（`cmd/modal-editor-ext-treesitter/`）が cgo で文法をコンパイルするので、`make check`、`make test`、`make build` に要る。本体は cgo を使わない
- musl-gcc（Debian と Ubuntu では `musl-tools`）。`make build` が tree-sitter の拡張機能を musl で静的にリンクするので要る。別の場所にあるなら `make build MUSL_CC=<パス>` で指定する

配布物の利用者には、C コンパイラも Go も要らない。

リンターなどの開発ツールは、`make` が `go run` でバージョンを固定して実行するので、インストールは不要である。
初回の実行時だけ、ツールのビルドに数分かかる。

## 開発

```sh
make help    # ターゲットの一覧
make hooks   # コミット前に make check を実行する Git フックを有効にする（clone 後に 1 回）
make check   # 整形の検査、型チェック、リント、テストをまとめて実行（CI と同じ内容）
```

| コマンド | 内容 |
|---|---|
| `make fmt` | コードを整形する（gofumpt と goimports） |
| `make fmt-check` | 整形されていないファイルがあれば失敗する |
| `make typecheck` | 全パッケージをコンパイルし、`go vet` を実行する |
| `make lint` | golangci-lint を実行する。設定は [.golangci.yml](.golangci.yml) |
| `make progress` | 要件ごとの進捗（`docs/progress.md`）を生成する。注釈の書き方は[基本設計](docs/basic-design.md)の「要件との対応」を参照 |
| `make test` | テストを実行する（データ競合の検出つき） |
| `make vuln` | 依存ライブラリの既知の脆弱性を調べる |
| `make build` | 実行ファイルを `bin/` に作る |
| `make build-check` | `make build` の後に、本体の `CGO_ENABLED=0` と全ての実行ファイルの静的リンクを検査する |
| `make dist` | 配布物（実行ファイルと宣言ファイル）を `dist/` にディレクトリと `tar.gz` で作る |
| `make dist-check` | `make dist` の後に、配布物の形を検査する |
| `make dist-smoke` | `make dist` の後に、ビルド用の道具の無いコンテナで配布物を起動する。Docker が要る |

Go はコンパイル言語なので、型チェックはコンパイルの一部として行われる。
`make typecheck` は、実行ファイルを残さずにコンパイルだけを行う。

## ディレクトリ構成

| パス | 内容 |
|---|---|
| `cmd/modal-editor/` | エディタ本体の実行ファイルの入口 |
| `cmd/modal-editor-mcp-bridge/` | MCP の中継コマンド。標準入出力と起動中のエディタの Unix ドメインソケットの間でメッセージを転送する |
| `cmd/modal-editor-ext-line/` | 標準の拡張機能（行のテキストオブジェクト）の実行ファイルと宣言ファイル |
| `cmd/modal-editor-ext-script/` | スクリプトの拡張機能（利用者が置く Go のソースファイル）を yaegi で実行する実行ファイル。宣言ファイルの `args` でスクリプトのパスを受け取る。スクリプトの書き方と yaegi の制限はパッケージの doc（`go doc ./cmd/modal-editor-ext-script`）にある |
| `cmd/modal-editor-ext-treesitter/` | 標準の拡張機能（tree-sitter による構文ハイライト）の実行ファイルと宣言ファイル。cgo を使い、ビルドに C コンパイラが要る |
| `cmd/modal-editor-ext-dap/` | 標準の拡張機能（DAP によるデバッグ）の実行ファイルと宣言ファイル。デバッグアダプター（既定は delve の `dlv dap`）を子プロセスとして起動する |
| `cmd/modal-editor-ext-lsp/` | 標準の拡張機能（LSP のクライアント。診断、定義へ移動、ホバー、整形）の実行ファイルと宣言ファイル。言語サーバー（既定は Go の gopls）を子プロセスとして起動する |
| `cmd/modal-editor-ext-acp/` | 標準の拡張機能（ACP のクライアント。プロンプトの送信、返答の表示、権限の確認、エディタの内容を通したファイルの読み書き）の実行ファイルと宣言ファイル。設定に書いたコーディングエージェントを子プロセスとして起動する |
| `cmd/modal-editor-ext-mcp/` | 標準の拡張機能（MCP のサーバー。バッファの一覧と内容、カーソル位置、選択範囲、診断の取得、編集、ファイルを開く、保存のツール）の実行ファイルと宣言ファイル。中継コマンドからの接続を Unix ドメインソケットで受ける。当面の使い方（環境変数 `MODAL_EDITOR_MCP_SOCKET` とコマンド `mcp.start`）はパッケージの doc（`go doc ./cmd/modal-editor-ext-mcp`）にある |
| `cmd/reqtrace/` | 要件ごとの進捗を生成するツール |
| `internal/distcheck/` | `make build` と `make dist` の生成物を検査するテスト |
| `internal/` | このリポジトリの中だけで使うパッケージ |
| `docs/` | 要件定義などの文書 |
| `docs/measurements/` | 計測の手順と結果 |
