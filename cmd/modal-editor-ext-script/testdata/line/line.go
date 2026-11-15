// line.go は、スクリプトの拡張機能の例である。
// modal-editor-ext-line の line.inner（il）と同じテキストオブジェクトを、script.inner の ID で提供する。
package main

import (
	"context"
	"log"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/ext/server"
)

func main() {
	s := server.New()
	s.TextObject("script.inner", resolveInner)
	if err := s.Run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func isBlank(c byte) bool { return c == ' ' || c == '\t' }

// resolveInner は、カーソル行から回数の分の行の、先頭と末尾の空白を除いた範囲を返す。
func resolveInner(_ context.Context, b *server.Buffer, p *ext.TextObjectResolveParams) (ext.TextObjectResolveResult, error) {
	first := p.Cursor.Line
	count := p.Count
	if count < 1 {
		count = 1
	}
	if first < 0 || first+count > b.LineCount() {
		return ext.TextObjectResolveResult{}, nil
	}
	last := first + count - 1
	var res ext.TextObjectResolveResult
	found := false
	for l := first; l <= last && !found; l++ {
		line := b.Line(l)
		for i := 0; i < len(line); i++ {
			if !isBlank(line[i]) {
				res.Range.Start = ext.Position{Line: l, Byte: i}
				found = true
				break
			}
		}
	}
	if !found {
		return ext.TextObjectResolveResult{}, nil
	}
	for l := last; l >= first; l-- {
		line := b.Line(l)
		for i := len(line); i > 0; i-- {
			if !isBlank(line[i-1]) {
				res.Range.End = ext.Position{Line: l, Byte: i}
				res.OK = true
				return res, nil
			}
		}
	}
	return res, nil
}
