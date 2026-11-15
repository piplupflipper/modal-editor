package server_test

import (
	"context"
	"log"
	"strings"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/ext/server"
)

// innerLine は、テキストオブジェクト「行の内側」（行頭の空白を除いた行末まで）のハンドラーである。
// 宣言ファイルには contributes.textObjects に {"id": "example.innerLine", "keys": "il"} と書く。
func innerLine(_ context.Context, b *server.Buffer, p *ext.TextObjectResolveParams) (ext.TextObjectResolveResult, error) {
	line := b.Line(p.Cursor.Line)
	start := len(line) - len(strings.TrimLeft(line, " \t"))
	if start == len(line) {
		// 空白だけの行には、行の内側が無い。
		return ext.TextObjectResolveResult{OK: false}, nil
	}
	return ext.TextObjectResolveResult{
		OK: true,
		Range: ext.Range{
			Start: ext.Position{Line: p.Cursor.Line, Byte: start},
			End:   ext.Position{Line: p.Cursor.Line, Byte: len(line)},
		},
	}, nil
}

// テキストオブジェクトを 1 つ提供する、最小の拡張機能の main である。
// 本体はこの実行ファイルを子プロセスとして起動し、標準入出力でメッセージをやり取りする。
func Example() {
	s := server.New()
	s.TextObject("example.innerLine", innerLine)
	if err := s.Run(context.Background()); err != nil {
		log.Fatal(err)
	}
}
