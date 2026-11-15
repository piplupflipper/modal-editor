# 起動時間、メモリの使用量、実行ファイルのサイズ（NFR-05）

要件定義の NFR-05 は「起動時間、メモリの使用量、実行ファイルのサイズの目標値は未定で、計測してから定める」としている。
この文書は、3 つの量の測り方と 2026-11-16 の値を書く（#66）。
目標値はオーナーが決めるので、この文書は値だけを書く。

## 測る時点と、測り直す時点

本体は 2026-11-16 の時点で `--version` だけが動き、エディタのイベントループ（`internal/editor`）と画面の描画は、端末の制御に使うライブラリの裁定（#9）を待っている。
このため起動時間とメモリの使用量は、`--version` を表示して終了するまでの値である。
エディタの起動（端末を初期化して最初の画面を描くまで）とは別の量なので、`internal/editor` が入った後に、同じ手順の対象を変えて測り直す。

## 環境

| 項目 | 値 |
|---|---|
| CPU | AMD Ryzen 9 8945HX（32 論理コア） |
| メモリ | 29 GiB |
| OS | Ubuntu 24.04、Linux 7.0.0-31-generic |
| Go | 1.27.4 linux/amd64 |
| コミット | `b4797f5`（ブランチ `feat/66-nfr-verification`） |
| ビルド | `make build`（`-trimpath`。シンボルとデバッグ情報は削らない） |

## 実行ファイルのサイズ

`make build` の後に `ls -l bin/` で読んだ値である。

| 実行ファイル | バイト数 | MiB |
|---|---|---|
| `modal-editor`（本体） | 2,468,266 | 2.35 |
| `modal-editor-mcp-bridge` | 3,091,548 | 2.95 |
| `modal-editor-ext-line` | 4,342,193 | 4.14 |
| `modal-editor-ext-lsp` | 5,051,030 | 4.82 |
| `modal-editor-ext-dap` | 5,196,045 | 4.96 |
| `modal-editor-ext-treesitter` | 6,668,576 | 6.36 |
| `modal-editor-ext-script` | 29,590,157 | 28.22 |
| 配布物の `tar.gz`（`make dist`） | 30,969,670 | 29.54 |

`modal-editor-ext-script` が大きいのは、スクリプトから呼べる Go の標準ライブラリのシンボルの表（yaegi の `stdlib`）を含むためと見ている。
内訳は測っていない。
`-ldflags '-s -w'` でシンボルとデバッグ情報を削ると小さくなる見込みだが、この計測では削っていない。

## 起動時間とメモリの使用量

`--version` を 200 回続けて起動し、1 回ごとの経過時間を `date +%s%N` の差で取った。
最大常駐セットサイズ（maximum RSS）は、別に 20 回起動して GNU time の `%M` を読み、最大を取った。
手順は次のスクリプトである（リポジトリには置かない）。

```sh
#!/bin/sh
# 使い方: measure.sh <実行ファイル> [回数]
set -eu
bin=$1
n=${2:-200}
tmp=$(mktemp)
i=0
while [ "$i" -lt "$n" ]; do
	s=$(date +%s%N)
	"$bin" --version >/dev/null
	e=$(date +%s%N)
	echo $(((e - s) / 1000)) >>"$tmp"
	i=$((i + 1))
done
sort -n "$tmp" | awk -v n="$n" '{a[NR]=$1} END {printf "start_us p50=%d p90=%d p99=%d max=%d\n", a[int(n*0.5)], a[int(n*0.9)], a[int(n*0.99)], a[n]}'
rm -f "$tmp"
max=0
i=0
while [ "$i" -lt 20 ]; do
	r=$(/usr/bin/time -f %M "$bin" --version 2>&1 >/dev/null)
	[ "$r" -gt "$max" ] && max=$r
	i=$((i + 1))
done
echo "maxrss_kib=$max"
```

経過時間には、計測の側の `date` の起動が 1 回分含まれる。
同じスクリプトで `/bin/true` を測った値を、計測の手順そのものにかかる時間の目安として並べる。

| 対象 | p50 | p90 | p99 | 最大 | 最大 RSS |
|---|---|---|---|---|---|
| `modal-editor --version`（1 回目） | 1.84 ms | 1.97 ms | 2.15 ms | 2.19 ms | 2,428 KiB |
| `modal-editor --version`（2 回目） | 1.84 ms | 1.97 ms | 2.09 ms | 2.32 ms | 2,428 KiB |
| `modal-editor --version`（3 回目） | 1.69 ms | 1.95 ms | 2.69 ms | 3.10 ms | 2,428 KiB |
| `modal-editor-mcp-bridge --version` | 1.86 ms | 2.00 ms | 2.16 ms | 2.21 ms | 2,920 KiB |
| `/bin/true --version`（目安） | 1.12 ms | 1.20 ms | 1.30 ms | 1.34 ms | 2,220 KiB |

本体の `--version` の p50 は 1.7〜1.8 ms で、`/bin/true` との差は 0.6〜0.7 ms だった。
差の大部分は Go のランタイムの初期化と考えているが、内訳は測っていない。

拡張機能の起動時間は、本体が拡張機能を非同期に読み込むので（NFR-04）、キー入力を受け付けるまでの時間には入らない。
拡張機能への最初の問い合わせの往復は [stage1-rpc-latency.md](stage1-rpc-latency.md) が測っている。
