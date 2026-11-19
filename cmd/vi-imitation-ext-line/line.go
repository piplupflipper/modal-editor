package main

import (
	"context"
	"fmt"

	"github.com/piplupflipper/vi-imitation/ext"
	"github.com/piplupflipper/vi-imitation/ext/server"
)

// テキストオブジェクトの ID。宣言ファイル（manifest.json）の contributes.textObjects と一致させる。
const (
	idInner  = "line.inner"
	idAround = "line.around"
)

// newServer は、この拡張機能のハンドラーを登録した Server を返す。
//
// req: FR-11
func newServer() *server.Server {
	s := server.New()
	s.TextObject(idInner, resolveInner)
	s.TextObject(idAround, resolveAround)
	return s
}

// lineSpan は、カーソル行から回数の分だけの行の、最初の行と最後の行の番号（0 始まり）を返す。
// 回数が 0（省略）なら 1 行とする。
// カーソルの行がバッファの外にあれば誤りを返す。
// 回数の分の行がバッファに無ければ ok は false である（範囲を最終行で切り詰めない）。
func lineSpan(b *server.Buffer, p *ext.TextObjectResolveParams) (first, last int, ok bool, err error) {
	first = p.Cursor.Line
	if first < 0 || first >= b.LineCount() {
		return 0, 0, false, fmt.Errorf("cursor line %d out of range [0, %d)", first, b.LineCount())
	}
	count := max(p.Count, 1)
	if count > b.LineCount()-first {
		return 0, 0, false, nil
	}
	return first, first + count - 1, true, nil
}

// resolveInner は、line.inner（il）の範囲を返す。
//
// 範囲は、カーソル行から回数の分の行をつないだテキストから、先頭と末尾の空白（空白文字とタブ）を除いたものである。
// 行の間の改行は範囲に含む。文字単位で扱う（Linewise は false）。
// 除いた結果が空になる（どの行も空白だけか空）なら、ok が false の結果を返す。
// 回数の分の行がバッファに無ければ、ok が false の結果を返す。
func resolveInner(_ context.Context, b *server.Buffer, p *ext.TextObjectResolveParams) (ext.TextObjectResolveResult, error) {
	first, last, ok, err := lineSpan(b, p)
	if err != nil || !ok {
		return ext.TextObjectResolveResult{}, err
	}
	start, found := firstNonBlank(b, first, last)
	if !found {
		return ext.TextObjectResolveResult{}, nil
	}
	end, _ := lastNonBlankEnd(b, first, last)
	return ext.TextObjectResolveResult{OK: true, Range: ext.Range{Start: start, End: end}}, nil
}

// resolveAround は、line.around（al）の範囲を返す。
//
// 範囲は、カーソル行の先頭から、回数の分だけ下の行の末尾までである。最後の行の末尾の改行は含めない。
// 文字単位で扱う（Linewise は false）。空の行では空の範囲を返し、ok は true である。
// 回数の分の行がバッファに無ければ、ok が false の結果を返す。
func resolveAround(_ context.Context, b *server.Buffer, p *ext.TextObjectResolveParams) (ext.TextObjectResolveResult, error) {
	first, last, ok, err := lineSpan(b, p)
	if err != nil || !ok {
		return ext.TextObjectResolveResult{}, err
	}
	return ext.TextObjectResolveResult{
		OK: true,
		Range: ext.Range{
			Start: ext.Position{Line: first, Byte: 0},
			End:   ext.Position{Line: last, Byte: len(b.Line(last))},
		},
	}, nil
}

// isBlank は、c が空白文字かタブかどうかを返す（Vim の ^ が飛ばす文字と同じ）。
func isBlank(c byte) bool { return c == ' ' || c == '\t' }

// firstNonBlank は、first 行から last 行までで最初の、空白でない文字の位置を返す。無ければ found は false である。
func firstNonBlank(b *server.Buffer, first, last int) (pos ext.Position, found bool) {
	for l := first; l <= last; l++ {
		line := b.Line(l)
		for i := 0; i < len(line); i++ {
			if !isBlank(line[i]) {
				return ext.Position{Line: l, Byte: i}, true
			}
		}
	}
	return ext.Position{}, false
}

// lastNonBlankEnd は、first 行から last 行までで最後の、空白でない文字の直後の位置を返す。無ければ found は false である。
// 空白は 1 バイトの文字なので、直後の位置は UTF-8 の文字の境界になる。
func lastNonBlankEnd(b *server.Buffer, first, last int) (pos ext.Position, found bool) {
	for l := last; l >= first; l-- {
		line := b.Line(l)
		for i := len(line); i > 0; i-- {
			if !isBlank(line[i-1]) {
				return ext.Position{Line: l, Byte: i}, true
			}
		}
	}
	return ext.Position{}, false
}
