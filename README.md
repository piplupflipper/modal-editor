# modal-editor

Vim のキーバインドを持つ、ターミナル用のモーダルエディタ（仮称）。
実装は未着手で、現在は開発環境と設計の文書だけがある。

- 要件定義: [docs/requirements.md](docs/requirements.md)
- 基本設計: [docs/basic-design.md](docs/basic-design.md)
- 要件ごとの進捗: [docs/progress.md](docs/progress.md)（`make progress` が生成する）

詳細設計の文書は作らず、コードと doc コメントを詳細設計として扱う。

## 必要なもの

- Go（バージョンは [go.mod](go.mod) を参照）
- make
- C コンパイラ（gcc など）。tree-sitter の拡張機能（`cmd/modal-editor-ext-treesitter/`）が cgo で文法をコンパイルするので、`make check`、`make test`、`make build` に要る。本体は cgo を使わない

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

Go はコンパイル言語なので、型チェックはコンパイルの一部として行われる。
`make typecheck` は、実行ファイルを残さずにコンパイルだけを行う。

## ディレクトリ構成

| パス | 内容 |
|---|---|
| `cmd/modal-editor/` | エディタ本体の実行ファイルの入口 |
| `cmd/modal-editor-mcp-bridge/` | MCP の中継コマンド。標準入出力と起動中のエディタの Unix ドメインソケットの間でメッセージを転送する |
| `cmd/modal-editor-ext-line/` | 標準の拡張機能（行のテキストオブジェクト）の実行ファイルと宣言ファイル |
| `cmd/modal-editor-ext-script/` | スクリプトの拡張機能（利用者が置く Go のソースファイル）を yaegi で実行する実行ファイル。宣言ファイルの `args` でスクリプトのパスを受け取る |
| `cmd/modal-editor-ext-treesitter/` | 標準の拡張機能（tree-sitter による構文ハイライト）の実行ファイルと宣言ファイル。cgo を使い、ビルドに C コンパイラが要る |
| `cmd/reqtrace/` | 要件ごとの進捗を生成するツール |
| `internal/` | このリポジトリの中だけで使うパッケージ |
| `docs/` | 要件定義などの文書 |
| `docs/measurements/` | 計測の手順と結果 |
