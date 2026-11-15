# 配布の形と静的リンクの検証（NFR-01、NFR-02、NFR-06）

要件定義の NFR-01（ファイルを置くだけで動作する。本体と拡張機能は別のファイル）、NFR-02（本体は cgo を使わず、C の標準ライブラリに依存しない）、NFR-06（利用者のマシンにビルド用の道具を要求しない）を、どう検証するかと、2026-11-16 の結果を書く（#66）。
検証の方法の要約は基本設計の 9 章にある。

## 結論

`make build` が作る 7 個の実行ファイルは、全て静的にリンクされていて、動的リンカーと共有ライブラリを要求しない。
本体、MCP の中継コマンド、tree-sitter 以外の標準の拡張機能は `CGO_ENABLED=0` でビルドされている。
`make dist` の配布物を、ビルド用の道具の無い 4 種類のコンテナ（glibc の版の違うもの、musl のもの、busybox）に置き、全ての実行ファイルが起動することを確かめた。

## tree-sitter の拡張機能を静的にリンクする理由

tree-sitter の拡張機能（`cmd/modal-editor-ext-treesitter`）は、文法を C でコンパイルするので cgo を使う。
`CGO_ENABLED=1` で普通にビルドすると、ビルドしたマシンの glibc に動的にリンクされる。
2026-11-16 に Ubuntu 24.04（glibc 2.39）で作った実行ファイルは、`objdump -T` で見ると `GLIBC_2.32` と `GLIBC_2.34` のシンボルを要求していた。
この実行ファイルをコンテナで起動した結果は次のとおりである。

| イメージ | C の標準ライブラリ | 動的にリンクしたもの | 静的にリンクしたもの |
|---|---|---|---|
| `almalinux:8-minimal` | glibc 2.28 | `GLIBC_2.32' not found` で終了 | 起動する |
| `alpine:3.24.1` | musl | `exec /x: no such file or directory`（動的リンカーが無い） | 起動する |
| `busybox:1.36.1` | glibc | 起動する | 起動する |

AlmaLinux 8 は 2026-11-16 の時点で保守されているディストリビューションなので、動的にリンクした実行ファイルは NFR-01 を満たさない。
このため Makefile は、cgo の拡張機能を `-linkmode external -extldflags "-static"` で C の標準ライブラリごと静的にリンクする。
静的にリンクした glibc では、名前の解決（`getaddrinfo`、`getpwnam`）が NSS の共有ライブラリを読もうとして動かない。
`-tags netgo,osusergo` で、Go の標準ライブラリのこれらの処理を Go の実装に置き換える。
静的にリンクした状態で `go test` を実行し、tree-sitter の拡張機能のテストが成功することも確かめた。

```bash
CGO_ENABLED=1 go test -count=1 -tags netgo,osusergo \
  -ldflags '-linkmode external -extldflags "-static"' ./cmd/modal-editor-ext-treesitter
```

ビルドするマシンには、静的リンク用の C の標準ライブラリ（Debian と Ubuntu では `libc6-dev` の `libc.a`）が要る。
GitHub Actions の `ubuntu-latest` には入っている。

## 検査の一覧

| ターゲット | 検査の内容 | 対象の要件 | CI |
|---|---|---|---|
| `make build-check` | `make build` の本体の build settings の `CGO_ENABLED` が `0` で、ELF に `PT_INTERP` と `DT_NEEDED` が無い。全ての実行ファイルが同じく静的で、tree-sitter 以外は `CGO_ENABLED=0` | NFR-01、NFR-02 | 実行する |
| `make dist-check` | 配布物の宣言ファイルの `command` が同じディレクトリの実行ファイルを指し、全ての実行ファイルが静的 | NFR-01、NFR-06 | 実行する |
| `make dist-smoke` | ビルド用の道具の無いコンテナに配布物を置き、起動する | NFR-01、NFR-06 | 実行する |

`make build-check` と `make dist-check` は `internal/distcheck` のテストを、生成物のディレクトリを環境変数（`MODAL_EDITOR_BUILD_DIR`、`MODAL_EDITOR_DIST_DIR`）で渡して実行する。
`make check` の `go test ./...` では環境変数が無いので、このテストは飛ばされる。

`make dist-smoke` は Docker を使う。
コンテナはネットワークを切り（`--network none`）、配布物を読み取り専用で `/opt/modal-editor` に置いて、次を実行する。

- 本体と MCP の中継コマンドの `--version`
- 宣言ファイルのある拡張機能を、標準入力を閉じて起動する（接続が閉じたので終了コード 0 で終わる）
- スクリプトの実行環境 `modal-editor-ext-script` を引数なしで起動し、使い方の表示を確かめる

イメージは `debian:stable-slim`、`almalinux:8-minimal`、`alpine:3`、`busybox:stable` である。
どれも C コンパイラと Go を含まないので、起動できれば NFR-06 の「ビルド用の道具を要求しない」を満たす。

## 配布物の形

`make dist` は `dist/modal-editor-<版>-<OS>-<アーキテクチャ>/` を作り、同じ名前の `tar.gz` にまとめる。

```text
modal-editor-<版>-linux-amd64/
├── modal-editor                 本体
├── modal-editor-mcp-bridge      MCP の中継コマンド
├── modal-editor-ext-script      スクリプトの実行環境（宣言ファイルは利用者のスクリプトごとに置く）
└── extensions/
    ├── dap/         manifest.json と modal-editor-ext-dap
    ├── line/        manifest.json と modal-editor-ext-line
    ├── lsp/         manifest.json と modal-editor-ext-lsp
    └── treesitter/  manifest.json と modal-editor-ext-treesitter
```

本体が拡張機能を探す規則は、要件定義 7 章の未決事項のままである。
`extensions/<名前>/` の配置は仮で、規則が決まったら `make dist` を合わせる。

## 2026-11-16 の結果

コミット `b4797f5`（ブランチ `feat/66-nfr-verification`）を Ubuntu 24.04、Go 1.27.4 でビルドし、`make build-check`、`make dist-check`、`make dist-smoke` が成功した。
`make dist-smoke` では、4 種類のイメージの全てで、本体と中継コマンドが版を表示し、4 個の拡張機能とスクリプトの実行環境が起動した。
