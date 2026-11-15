#!/bin/sh
# make dist-smoke がコンテナの中で実行する、配布物の起動の確認（NFR-01、NFR-06）。
# 使い方: smoke.sh <配布物のディレクトリ>
# busybox の sh でも動くように、POSIX の sh の範囲で書く。
set -eu
d=$1

"$d/modal-editor" --version
"$d/modal-editor-mcp-bridge" --version

# 宣言ファイルのある拡張機能は、標準入力を閉じると接続が終わったとして終了コード 0 で終わる。
for m in "$d"/extensions/*; do
	name=${m##*/}
	"$m/modal-editor-ext-$name" </dev/null
	echo "$name: ok"
done

# スクリプトの実行環境は、引数が無いと使い方を表示する。
"$d/modal-editor-ext-script" 2>&1 | grep -q '^usage:'
echo "script: ok"

# tree-sitter の拡張機能に Go のソースを 1 回解析させ、decoration/set が返ることを確かめる。
# 文法の C のコードと、静的にリンクした C の標準ライブラリ（malloc など）を実行時に通す。
msg() {
	printf 'Content-Length: %d\r\n\r\n%s' "${#1}" "$1"
}
out=$(
	{
		msg '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"apiVersion":"0.2.0"}}'
		msg '{"jsonrpc":"2.0","method":"buffer/open","params":{"buffer":1,"version":1,"fileType":"go","text":"package main\nfunc main() {}"}}'
		# 解析は別の goroutine で行うので、応答を待ってから標準入力を閉じる。
		sleep 2
	} | "$d/extensions/treesitter/modal-editor-ext-treesitter"
)
case $out in
*'"method":"decoration/set"'*'"styles":["keyword","function"]'*) echo "treesitter parse: ok" ;;
*)
	echo "treesitter parse: unexpected output: $out" >&2
	exit 1
	;;
esac
