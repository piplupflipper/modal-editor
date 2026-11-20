# 配布の形とリンクの仕方の検証（NFR-01、NFR-02、NFR-06）

要件定義の NFR-01（最近の Linux にファイルを置くだけで動作する。本体と拡張機能は別のファイル）、NFR-02（本体は cgo を使わず、C の標準ライブラリに依存しない）、NFR-06（利用者のマシンにビルド用の道具を要求しない）を、どう検証するかと、2026-11-16 の結果と、2026-11-20 の変更（#277）を書く（#66）。
検証の方法の要約は基本設計の 9.1 にある。

## 結論

`make build` が作る実行ファイルのうち、tree-sitter の拡張機能を除くものは、全て静的にリンクされていて、動的リンカーと共有ライブラリを要求しない。
本体、MCP の中継コマンド、tree-sitter 以外の標準の拡張機能は `CGO_ENABLED=0` でビルドされている。
cgo を使う tree-sitter の拡張機能は、2026-11-20 から glibc に動的にリンクする（#277）。
利用者の手元でビルドしたパーサの共有ライブラリを `dlopen` で読む（FR-142）ためである。
このため、古い glibc と musl の環境では、tree-sitter の拡張機能は起動せず、構文の色付けを使えない。
2026-11-20 のオーナーの裁定で、このエディタは最近の Linux で動くことを目的とし、あらゆる環境で動くことは目的としないとした。

2026-11-16 までは、tree-sitter の拡張機能も musl を C の標準ライブラリとして静的にリンクしていた。
そのときの検証の結果（[tree-sitter の拡張機能を静的にリンクしていた理由](#tree-sitter-の拡張機能を静的にリンクしていた理由)、[2026-11-16 の結果](#2026-11-16-の結果)）は、記録として残す。

## tree-sitter の拡張機能を動的にリンクする理由（2026-11-20）

2026-11-20 の調査で、次のことを手元で確かめた（#277 の issue の表）。

| 項目 | 結果 |
|---|---|
| musl で静的にリンクした実行ファイルから `dlopen` | 使えない。`dlopen failed: Dynamic loading not supported` を返した |
| glibc に動的にリンクした実行ファイルから `dlopen` | 手元でビルドした tree-sitter-python v0.25.0 の `.so` を読めた |

オーナーは、案 B1（tree-sitter の拡張機能を glibc に動的にリンクし、利用者の手元で作ったパーサの `.so` を読み込む）を採った。
古い glibc（AlmaLinux 8 の 2.28）と Alpine では、構文の色付けをあきらめる。

`make build` は、tree-sitter の拡張機能を `CGO_ENABLED=1` で、ビルドしたマシンの C コンパイラと glibc でビルドする（Makefile の `CGO_EXTENSIONS`）。
2026-11-20 に Ubuntu（glibc 2.43、gcc 15.2.0）で作った実行ファイルは、次のとおりだった。

| 項目 | 結果 | 確かめ方 |
|---|---|---|
| 要求する共有ライブラリ | `libc.so.6` だけ | `readelf -d` の `NEEDED` |
| 要求する glibc のシンボルの版 | 最も新しいもので `GLIBC_2.34` | `objdump -T` |
| 大きさ | 6,677,096 バイト | `ls -l` |

要求するシンボルの版は、ビルドしたマシンの glibc と C コンパイラで変わりうる。
CI（GitHub Actions の `ubuntu-latest`）で作ったものの版は、この文書では確かめていない。

## C の標準ライブラリの選び方（2026-11-16 の記録）

2026-11-16 に、tree-sitter の拡張機能を静的にリンクする方法として、次の 3 つを比べた。
手元で試したのは glibc 静的と musl（`musl-gcc`）で、zig cc は試していない。

| 案 | ライセンスの義務 | ビルドの道具 | 確かめたこと |
|---|---|---|---|
| glibc 静的（`-extldflags "-static"` だけ） | glibc は LGPL-2.1-or-later。静的にリンクして配る場合、利用者がライブラリを差し替えて再リンクできるように、アプリケーション側のオブジェクトかソースを提供する義務が付く（LGPL 2.1 の 6 節） | `libc6-dev` の `libc.a` | 下の表の 3 環境で起動した |
| musl 静的（`CC=musl-gcc`、2026-11-16 に採用し、2026-11-20 にやめた） | musl は MIT。著作権の表示とライセンスの文面を添える | Debian と Ubuntu の `musl-tools` | 下の表の 3 環境とビルドしたマシンで起動し、静的にリンクした状態の `go test ./cmd/vi-imitation-ext-treesitter` が成功した |
| zig cc（`CC="zig cc -target x86_64-linux-musl"`） | musl 静的と同じ | zig の版を固定して取得する | 試していない |

glibc を静的にリンクした実行ファイルも、2026-11-20 の調査では `dlopen` で `.so` を読めた。
ただし、リンカーは「静的にリンクしたアプリケーションの `dlopen` は、実行時にリンクしたときと同じ版の glibc の共有ライブラリを要する」と警告した。
古い glibc の環境では動かないので、glibc 静的にしても動的にリンクしたときと同じ環境の制約が残り、LGPL の再リンクの義務だけが増える。

## tree-sitter の拡張機能を静的にリンクしていた理由

この節は 2026-11-16 の記録である。

tree-sitter の拡張機能（`cmd/vi-imitation-ext-treesitter`）は、文法を C でコンパイルするので cgo を使う。
`CGO_ENABLED=1` で普通にビルドすると、ビルドしたマシンの glibc に動的にリンクされる。
2026-11-16 に Ubuntu 24.04（glibc 2.39）で作った実行ファイルは、`objdump -T` で見ると `GLIBC_2.32` と `GLIBC_2.34` のシンボルを要求していた。
この実行ファイルと、静的にリンクした実行ファイルをコンテナで起動した結果は次のとおりである。

| イメージ | C の標準ライブラリ | 動的にリンクしたもの | glibc を静的にリンクしたもの | musl を静的にリンクしたもの |
|---|---|---|---|---|
| `almalinux:8-minimal`（8.10） | glibc 2.28 | `GLIBC_2.32' not found` で終了 | 起動する | 起動する |
| `alpine:3.24.1` | musl | `exec /x: no such file or directory`（動的リンカーが無い） | 起動する | 起動する |
| `busybox:1.36.1` | glibc | 起動する | 起動する | 起動する |

AlmaLinux 8 は、セキュリティの保守が 2029-05-31 まで続く（[AlmaLinux のリリースノートの一覧](https://wiki.almalinux.org/release-notes/)、2026-11-16 に参照）。
2026-11-16 の時点の NFR-01（メンテナンスされている一般的な Linux ディストリビューションで動く）では、動的にリンクした実行ファイルは AlmaLinux 8 で起動しないので、NFR-01 を満たさないとした。
2026-11-20 に NFR-01 を最近の Linux に絞ったので、tree-sitter の拡張機能については、この判断を改めた。

## 同梱するライセンスの文面

`make dist` は、実行ファイルに静的にリンクするサードパーティのライセンスの文面を `licenses/` に置く。

| パス | 内容 | 集め方 |
|---|---|---|
| `licenses/go/` | Go の標準ライブラリとランタイム（`LICENSE`、`PATENTS`） | `$(go env GOROOT)` から写す |
| `licenses/<モジュールのパス>/` | 依存するモジュール（tree-sitter の Go の束縛と C の本体、Go と JSON の文法、yaegi、go-pointer、diff の go-udiff の `LICENSE-BSD` と `LICENSE-MIT`） | 配布する実行ファイルのパッケージと、本体につなぐことが決まっているパッケージ（Makefile の `DIST_LICENSE_PACKAGES`。2026-11-18 の時点で `internal/diff`）の `go list -deps` から、モジュールのディレクトリの `LICENSE*` を写す |

`internal/distcheck` の `TestDistLicenses` が、これらの文面が配布物にあることを確かめる。
2026-11-20 までは musl の文面（`licenses/musl/copyright`）も置いていた。
tree-sitter の拡張機能は glibc に動的にリンクし、配布物は C の標準ライブラリを含まないので、2026-11-20 に外した。
利用者の手元でビルドしたパーサは配布物に含まないので、パーサの文面も置かない。

このリポジトリ自身のライセンスは、2026-11-16 の時点で決まっていない（`LICENSE` が無い）。
オーナーの裁定を待つ。

## 検査の一覧

| ターゲット | 検査の内容 | 対象の要件 | CI |
|---|---|---|---|
| `make build-check` | `make build` の本体の build settings の `CGO_ENABLED` が `0` で、ELF に `PT_INTERP` と `DT_NEEDED` が無い。tree-sitter 以外の全ての実行ファイルが同じく静的で、`CGO_ENABLED=0`。tree-sitter の拡張機能は、`PT_INTERP` が glibc の `ld-linux` で、`DT_NEEDED` が glibc の共有ライブラリ（`libc.so.6` など）だけ（`TestBuildCgoGlibcDynamic`） | NFR-01、NFR-02 | 実行する |
| `make dist-check` | 配布物の宣言ファイルの `command` が同じディレクトリの実行ファイルを指し、tree-sitter 以外は静的、tree-sitter は glibc の共有ライブラリだけを要求する。ライセンスの文面がある | NFR-01、NFR-06 | 実行する |
| `make dist-smoke` | ビルド用の道具の無いコンテナに配布物を置き、起動する | NFR-01、NFR-06 | 実行する |

`make build-check` と `make dist-check` は `internal/distcheck` のテストを、生成物のディレクトリを環境変数（`VI_IMITATION_BUILD_DIR`、`VI_IMITATION_DIST_DIR`）で渡して実行する。
`make check` の `go test ./...` では環境変数が無いので、このテストは飛ばされる。

`make dist-smoke` は Docker を使う。
コンテナはネットワークを切り（`--network none`）、配布物を読み取り専用で `/opt/vi-imitation` に置いて、`internal/distcheck/testdata/smoke.sh` で次を実行する。

- 本体と MCP の中継コマンドの `--version`
- 宣言ファイルのある拡張機能を、標準入力を閉じて起動する（接続が閉じたので終了コード 0 で終わる）
- スクリプトの実行環境 `vi-imitation-ext-script` を引数なしで起動し、使い方の表示を確かめる
- tree-sitter の拡張機能に `initialize` と、Go のソースの `buffer/open` を送り、`decoration/set` が `keyword` と `function` の色を返すことを確かめる。文法の C のコードと、動的にリンクした glibc を実行時に通す

イメージは `debian:13.7-slim`、`almalinux:8.10-minimal`、`alpine:3.24.1`、`busybox:1.36.1` で、版のタグで固定する。
どれも C コンパイラと Go を含まないので、起動できれば NFR-06 の「ビルド用の道具を要求しない」を満たす。
tree-sitter の拡張機能の起動と解析まで確かめるのは、Makefile の `DIST_SMOKE_TREESITTER_IMAGES`（`debian:13.7-slim` と `busybox:1.36.1`）だけである。
残りの `almalinux:8.10-minimal`（glibc 2.28）と `alpine:3.24.1`（musl）では、tree-sitter の拡張機能を起動してみて、起動しなくても失敗にせず、結果を出力するだけにする（`smoke.sh` の環境変数 `SMOKE_TREESITTER`）。
CI は、イメージの取得（`make dist-smoke-images`。失敗したら 10 秒後に 1 回だけ再試行する）を別のステップにして、Docker Hub の取得の失敗と配布物の起動の失敗を見分ける。

## 配布物の形

`make dist` は `dist/` を消してから `dist/vi-imitation-<版>-<OS>-<アーキテクチャ>/` を作り、同じ名前の `tar.gz` にまとめる。

```text
vi-imitation-<版>-linux-amd64/
├── vi-imitation                 本体
├── vi-imitation-mcp-bridge      MCP の中継コマンド
├── vi-imitation-ext-script      スクリプトの実行環境（宣言ファイルは利用者のスクリプトごとに置く）
├── extensions/
│   ├── dap/         manifest.json と vi-imitation-ext-dap
│   ├── line/        manifest.json と vi-imitation-ext-line
│   ├── lsp/         manifest.json と vi-imitation-ext-lsp
│   └── treesitter/  manifest.json と vi-imitation-ext-treesitter（glibc に動的にリンクする）
└── licenses/        サードパーティのライセンスの文面
```

本体が拡張機能を探す規則は、要件定義 7 章の未決事項のままである。
`extensions/<名前>/` の配置は仮で、規則が決まったら `make dist` を合わせる。

## 2026-11-16 の結果

ブランチ `feat/66-nfr-verification` を Ubuntu 24.04、Go 1.27.4、musl 1.2.4（Ubuntu の `musl-tools` 1.2.4-2）でビルドし、`make build-check`、`make dist-check`、`make dist-smoke` が成功した。
`make dist-smoke` では、4 種類のイメージの全てで、本体と中継コマンドが版を表示し、4 個の拡張機能とスクリプトの実行環境が起動し、tree-sitter の拡張機能が Go のソースを解析した。
ビルドしたマシンで同じ確認のスクリプトを実行した結果も同じだった。

## 2026-11-20 の結果

ブランチ `feat/277-parser-fetch` を Ubuntu（glibc 2.43、gcc 15.2.0）でビルドし、`make build-check` と `make dist-check` が成功した。
`make dist-smoke` は手元では実行せず（手元の Docker にイメージを足さないため）、GitHub Actions の CI（`ubuntu-latest`）で実行して成功した。
tree-sitter の拡張機能の結果は次のとおりだった。本体、中継コマンド、ほかの拡張機能、スクリプトの実行環境は、4 種類のイメージの全てで起動した。

| イメージ | tree-sitter の拡張機能 |
|---|---|
| `debian:13.7-slim` | 起動し、Go のソースを解析した |
| `almalinux:8.10-minimal` | 起動しない（想定どおり） |
| `alpine:3.24.1` | 起動しない（想定どおり） |
| `busybox:1.36.1` | 起動し、Go のソースを解析した |
