// faulty.go は、異常を起こすハンドラーを集めたスクリプトである。テストだけで使う。
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/ext/server"
)

func main() {
	s := server.New()
	// 標準出力へ書いてから答える。本体とのメッセージを壊さないことを確かめる。
	s.TextObject("faulty.print", func(_ context.Context, _ *server.Buffer, p *ext.TextObjectResolveParams) (ext.TextObjectResolveResult, error) {
		fmt.Println("printed to stdout")
		return ext.TextObjectResolveResult{OK: true, Range: ext.Range{Start: p.Cursor, End: p.Cursor}}, nil
	})
	s.TextObject("faulty.panic", func(_ context.Context, _ *server.Buffer, _ *ext.TextObjectResolveParams) (ext.TextObjectResolveResult, error) {
		panic("panic in handler")
	})
	s.TextObject("faulty.gopanic", func(_ context.Context, _ *server.Buffer, _ *ext.TextObjectResolveParams) (ext.TextObjectResolveResult, error) {
		go func() { panic("panic in goroutine") }()
		select {}
	})
	s.TextObject("faulty.hang", func(_ context.Context, _ *server.Buffer, _ *ext.TextObjectResolveParams) (ext.TextObjectResolveResult, error) {
		n := 0
		for {
			n++
		}
	})
	if err := s.Run(context.Background()); err != nil {
		log.Fatal(err)
	}
}
