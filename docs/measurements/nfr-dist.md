# 配布の形と静的リンクの検証（NFR-01、NFR-02、NFR-06）

要件定義の NFR-01（ファイルを置くだけで動作する。本体と拡張機能は別のファイル）、NFR-02（本体は cgo を使わず、C の標準ライブラリに依存しない）、NFR-06（利用者のマシンにビルド用の道具を要求しない）を、どう検証するかと、2026-11-16 の結果を書く（#66）。
検証の方法の要約は基本設計の 9.1 にある。

## 結論

`make build` が作る 7 個の実行ファイルは、全て静的にリンクされていて、動的リンカーと共有ライブラリを要求しない。
本体、MCP の中継コマンド、tree-sitter 以外の標準の拡張機能は `CGO_ENABLED=0` でビルドされている。
cgo を使う tree-sitter の拡張機能は、musl を C の標準ライブラリとして静的にリンクする。
`make dist` の配布物を、ビルドしたマシンと、ビルド用の道具の無い 4 種類のコンテナ（glibc の版の違うもの、musl のもの、busybox）に置き、全ての実行ファイルが起動し、tree-sitter の拡張機能が Go のソースを解析することを確かめた。

## tree-sitter の拡張機能を静的にリンクする理由

tree-sitter の拡張機能（`cmd/modal-editor-ext-treesitter`）は、文法を C でコンパイルするので cgo を使う。
`CGO_ENABLED=1` で普通にビルドすると、ビルドしたマシンの glibc に動的にリンクされる。
2026-11-16 に Ubuntu 24.04（glibc 2.39）で作った実行ファイルは、`objdump -T` で見ると `GLIBC_2.32` と `GLIBC_2.34` のシンボルを要求していた。
この実行ファイルと、静的にリンクした実行ファイルをコンテナで起動した結果は次のとおりである。

| イメージ | C の標準ライブラリ | 動的にリンクしたもの | glibc を静的にリンクしたもの | musl を静的にリンクしたもの |
|---|---|---|---|---|
| `almalinux:8-minimal`（8.10） | glibc 2.28 | `GLIBC_2.32' not found` で終了 | 起動する | 起動する |
| `alpine:3.24.1` | musl | `exec /x: no such file or directory`（動的リンカーが無い） | 起動する | 起動する |
| `busybox:1.36.1` | glibc | 起動する | 起動する | 起動する |

AlmaLinux 8 は、セキュリティの保守が 2029-05-31 まで続く（[AlmaLinux のリリースノートの一覧](https://wiki.almalinux.org/release-notes/)、2026-11-16 に参照）。
動的にリンクした実行ファイルは、保守されているディストリビューションで起動しないので、NFR-01 を満たさない。

## C の標準ライブラリの選び方

静的にリンクする方法として、次の 3 つを比べた。
手元で試したのは glibc 静的と musl（`musl-gcc`）で、zig cc は試していない。

| 案 | ライセンスの義務 | ビルドの道具 | 確かめたこと |
|---|---|---|---|
| glibc 静的（`-extldflags "-static"` だけ） | glibc は LGPL-2.1-or-later。静的にリンクして配る場合、利用者がライブラリを差し替えて再リンクできるように、アプリケーション側のオブジェクトかソースを提供する義務が付く（LGPL 2.1 の 6 節） | `libc6-dev` の `libc.a` | 上の表の 3 環境で起動した |
| musl 静的（`CC=musl-gcc`、採用） | musl は MIT。著作権の表示とライセンスの文面を添える | Debian と Ubuntu の `musl-tools` | 上の表の 3 環境とビルドしたマシンで起動し、静的にリンクした状態の `go test ./cmd/modal-editor-ext-treesitter` が成功した |
| zig cc（`CC="zig cc -target x86_64-linux-musl"`） | musl 静的と同じ | zig の版を固定して取得する | 試していない。arm64 などへのクロスビルドが要るときの候補 |

ライセンスの義務が軽いので、musl 静的を採った。
Makefile は `MUSL_CC`（既定は `musl-gcc`）を `CC` に指定して cgo の拡張機能をビルドする。
`internal/distcheck` の `TestBuildCgoNotGlibc` は、ELF に glibc の `crt1.o` が入れる `.note.ABI-tag` が無いことを確かめ、指定が外れて glibc に戻ったことを見つける。

musl は glibc と実装の違う関数（malloc など）を持つので、性能が変わる可能性がある。
2026-11-16 の時点では測っていない。

前の版で付けていた `-tags netgo,osusergo` は外した。
このタグは、静的にリンクした glibc で動かない名前の解決（`getaddrinfo`、`getpwnam`）を Go の実装に置き換えるためのものだった。
tree-sitter の拡張機能は `net` と `os/user` を import しない（`go list -deps` で確認）ので、タグは効果を持っていなかった。
musl は NSS を持たないので、将来これらを import しても同じ問題は起きない。

## 同梱するライセンスの文面

`make dist` は、実行ファイルに静的にリンクするサードパーティのライセンスの文面を `licenses/` に置く。

| パス | 内容 | 集め方 |
|---|---|---|
| `licenses/go/` | Go の標準ライブラリとランタイム（`LICENSE`、`PATENTS`） | `$(go env GOROOT)` から写す |
| `licenses/musl/copyright` | musl | `MUSL_COPYRIGHT`（既定は Debian の musl のパッケージが置く `/usr/share/doc/musl/copyright`）から写す |
| `licenses/<モジュールのパス>/` | 依存するモジュール（tree-sitter の Go の束縛と C の本体、Go と JSON の文法、yaegi、go-pointer、diff の go-udiff の `LICENSE-BSD` と `LICENSE-MIT`） | 配布する実行ファイルのパッケージと、本体につなぐことが決まっているパッケージ（Makefile の `DIST_LICENSE_PACKAGES`。2026-11-18 の時点で `internal/diff`）の `go list -deps` から、モジュールのディレクトリの `LICENSE*` を写す |

`internal/distcheck` の `TestDistLicenses` が、これらの文面が配布物にあることを確かめる。
musl-gcc の静的リンクでは GCC の `libgcc.a` もリンクされる。
`libgcc` は [GCC Runtime Library Exception](https://www.gnu.org/licenses/gcc-exception-3.1.html) の下にあり、この例外は、GCC でコンパイルしたプログラムへ `libgcc` を組み込んで配ることを許している。
このため `libgcc` の文面は同梱していない。

このリポジトリ自身のライセンスは、2026-11-16 の時点で決まっていない（`LICENSE` が無い）。
オーナーの裁定を待つ。

## 検査の一覧

| ターゲット | 検査の内容 | 対象の要件 | CI |
|---|---|---|---|
| `make build-check` | `make build` の本体の build settings の `CGO_ENABLED` が `0` で、ELF に `PT_INTERP` と `DT_NEEDED` が無い。全ての実行ファイルが同じく静的で、tree-sitter 以外は `CGO_ENABLED=0`。tree-sitter の拡張機能は glibc をリンクしていない | NFR-01、NFR-02 | 実行する |
| `make dist-check` | 配布物の宣言ファイルの `command` が同じディレクトリの実行ファイルを指し、全ての実行ファイルが静的。ライセンスの文面がある | NFR-01、NFR-06 | 実行する |
| `make dist-smoke` | ビルド用の道具の無いコンテナに配布物を置き、起動する | NFR-01、NFR-06 | 実行する |

`make build-check` と `make dist-check` は `internal/distcheck` のテストを、生成物のディレクトリを環境変数（`MODAL_EDITOR_BUILD_DIR`、`MODAL_EDITOR_DIST_DIR`）で渡して実行する。
`make check` の `go test ./...` では環境変数が無いので、このテストは飛ばされる。

`make dist-smoke` は Docker を使う。
コンテナはネットワークを切り（`--network none`）、配布物を読み取り専用で `/opt/modal-editor` に置いて、`internal/distcheck/testdata/smoke.sh` で次を実行する。

- 本体と MCP の中継コマンドの `--version`
- 宣言ファイルのある拡張機能を、標準入力を閉じて起動する（接続が閉じたので終了コード 0 で終わる）
- スクリプトの実行環境 `modal-editor-ext-script` を引数なしで起動し、使い方の表示を確かめる
- tree-sitter の拡張機能に `initialize` と、Go のソースの `buffer/open` を送り、`decoration/set` が `keyword` と `function` の色を返すことを確かめる。文法の C のコードと、静的にリンクした musl を実行時に通す

イメージは `debian:13.7-slim`、`almalinux:8.10-minimal`、`alpine:3.24.1`、`busybox:1.36.1` で、版のタグで固定する。
どれも C コンパイラと Go を含まないので、起動できれば NFR-06 の「ビルド用の道具を要求しない」を満たす。
CI は、イメージの取得（`make dist-smoke-images`。失敗したら 10 秒後に 1 回だけ再試行する）を別のステップにして、Docker Hub の取得の失敗と配布物の起動の失敗を見分ける。

## 配布物の形

`make dist` は `dist/` を消してから `dist/modal-editor-<版>-<OS>-<アーキテクチャ>/` を作り、同じ名前の `tar.gz` にまとめる。

```text
modal-editor-<版>-linux-amd64/
├── modal-editor                 本体
├── modal-editor-mcp-bridge      MCP の中継コマンド
├── modal-editor-ext-script      スクリプトの実行環境（宣言ファイルは利用者のスクリプトごとに置く）
├── extensions/
│   ├── dap/         manifest.json と modal-editor-ext-dap
│   ├── line/        manifest.json と modal-editor-ext-line
│   ├── lsp/         manifest.json と modal-editor-ext-lsp
│   └── treesitter/  manifest.json と modal-editor-ext-treesitter
└── licenses/        サードパーティのライセンスの文面
```

本体が拡張機能を探す規則は、要件定義 7 章の未決事項のままである。
`extensions/<名前>/` の配置は仮で、規則が決まったら `make dist` を合わせる。

## 2026-11-16 の結果

ブランチ `feat/66-nfr-verification` を Ubuntu 24.04、Go 1.27.4、musl 1.2.4（Ubuntu の `musl-tools` 1.2.4-2）でビルドし、`make build-check`、`make dist-check`、`make dist-smoke` が成功した。
`make dist-smoke` では、4 種類のイメージの全てで、本体と中継コマンドが版を表示し、4 個の拡張機能とスクリプトの実行環境が起動し、tree-sitter の拡張機能が Go のソースを解析した。
ビルドしたマシンで同じ確認のスクリプトを実行した結果も同じだった。
