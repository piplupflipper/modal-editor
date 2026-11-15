package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/ext/server"
)

// writeAttempts は、fs/write_text_file で、バッファの編集と保存がバージョン番号の不一致で通らなかったときに試す回数の上限である（仮）。
// 利用者がエージェントの書き込みと同時に同じバッファを編集すると、本体は古いバージョン番号の編集を適用しない（FR-15）。
const writeAttempts = 3

// errNotAbsolute は、fs/* のパスが絶対パスでないことを表す。ACP の File System はパスを絶対パスと定めている。
var errNotAbsolute = errors.New("path is not absolute")

// readTextFile は、fs/read_text_file に答える（基本設計 6.5）。
// path のバッファの複製を持っていれば、未保存の変更を含めた複製から、無ければファイルから読む。
// line（1 から数える）と limit（行数の上限）があれば、その範囲の行だけを返す。
//
// 複製から読む内容は、本体がファイルへ書き出すときと同じく、FinalNewline が true なら末尾に改行を足したものである。
//
// req: FR-34
func (c *client) readTextFile(ctx context.Context, p *readTextFileParams) (readTextFileResult, error) {
	if !filepath.IsAbs(p.Path) {
		return readTextFileResult{}, &rpcError{Code: codeInvalidParams, Message: fmt.Sprintf("%s: %v: %q", methodReadTextFile, errNotAbsolute, p.Path)}
	}
	if (p.Line != nil && *p.Line < 1) || (p.Limit != nil && *p.Limit < 0) {
		return readTextFileResult{}, &rpcError{Code: codeInvalidParams, Message: methodReadTextFile + ": line must be >= 1 and limit must be >= 0"}
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeouts.request)
	defer cancel()
	content, err := c.readText(ctx, p.Path)
	if err != nil {
		return readTextFileResult{}, err
	}
	return readTextFileResult{Content: sliceLines(content, p.Line, p.Limit)}, nil
}

// readText は、path の内容を、複製を持っていれば複製から、無ければファイルから読む。
func (c *client) readText(ctx context.Context, path string) (string, error) {
	if id, ok := c.bufferFor(path); ok {
		// WaitBuffer は、ループの上で複製の Clone を取る。版 0 を指定するので、複製があれば待たない。
		b, err := c.srv.WaitBuffer(ctx, id, 0)
		switch {
		case err == nil:
			return fileContent(b), nil
		case !errors.Is(err, server.ErrBufferClosed):
			return "", err
		}
		// 調べた後に閉じられたら、ファイルから読む。
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(data) {
		// JSON の文字列にすると不正なバイトが置き換わり、エージェントが内容を誤って読むので、読ませない。
		return "", fmt.Errorf("%s is not valid UTF-8", path)
	}
	return string(data), nil
}

// fileContent は、複製をファイルへ書き出したときの内容を返す。
func fileContent(b *server.Buffer) string {
	text := b.Text()
	if b.FinalNewline() {
		text += "\n"
	}
	return text
}

// sliceLines は、content の line 行目（1 から数える）から limit 行を、改行を含めて返す。
// line が nil なら 1 行目から、limit が nil なら最後の行までである。
func sliceLines(content string, line, limit *int) string {
	if line == nil && limit == nil {
		return content
	}
	lines := strings.SplitAfter(content, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	start := 0
	if line != nil {
		start = *line - 1
	}
	if start >= len(lines) {
		return ""
	}
	end := len(lines)
	if limit != nil && start+*limit < end {
		end = start + *limit
	}
	return strings.Join(lines[start:end], "")
}

// writeTextFile は、fs/write_text_file に答える（基本設計 6.5）。
// path のバッファの複製を持っていれば（本体がそのファイルを開いていれば）、そのバッファの全文を置き換えて書き出す。
// 開いていなければ、unopened の書き方で書く。どちらの場合も、エージェントは書き込みの後にファイルを読むコマンドを動かしうるので、
// バッファへの編集だけで止めずにファイルへ書き出す。
//
// req: FR-34
func (c *client) writeTextFile(ctx context.Context, p *writeTextFileParams) (writeTextFileResult, error) {
	if !filepath.IsAbs(p.Path) {
		return writeTextFileResult{}, &rpcError{Code: codeInvalidParams, Message: fmt.Sprintf("%s: %v: %q", methodWriteTextFile, errNotAbsolute, p.Path)}
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeouts.request)
	defer cancel()
	if id, ok := c.bufferFor(p.Path); ok {
		err := c.writeBuffer(ctx, id, 0, p.Content)
		if !errors.Is(err, server.ErrBufferClosed) {
			return writeTextFileResult{}, err
		}
		// 調べた後に閉じられたら、開いていないファイルとして書く。
	}
	return writeTextFileResult{}, c.unopened.writeUnopened(ctx, p.Path, p.Content)
}

// unopenedFileWriter は、本体が開いていないファイルへの fs/write_text_file の書き方である。
//
// どう書くかは #43 の 3 の裁定待ちである（基本設計 6.5、10 章。FR-15 が拡張機能による開いていないファイルへの直接の書き込みを禁じるか）。
// 案は 2 つで、A は本体にバッファとして開かせてバッファを通して書く（openAsBuffer。推奨で、当面はこれを使う）、
// B は拡張機能がファイルへ直接書く。B に決まったら、このインターフェースを満たす型を足し、newClient で差し替える。
// 開いているファイルへの書き込みは、どちらの案でもバッファを通す（client.writeBuffer）。
type unopenedFileWriter interface {
	// writeUnopened は、path の内容を content にする。ファイルが無ければ作る（ACP の File System）。
	writeUnopened(ctx context.Context, path, content string) error
}

// openAsBuffer は、#43 の 3 の案 A の書き方である。
// editor/open でカーソルを移さずに開き、応答のバージョン番号の複製が届くのを待ってから、全文を置き換える buffer/edit と、
// skipHooks の buffer/save を送る（基本設計 6.5）。本体は無いファイルを空のバッファとして開くので、保存でファイルができる。
type openAsBuffer struct{ c *client }

// writeUnopened は、path をバッファとして開き、writeBuffer で書き出す。
//
// 不正な UTF-8 を含むファイルは、開く前に調べて誤りで答える。本体はそうしたバッファを拡張機能へ送らない（基本設計 4.1、5.3）ので、
// 開いてから複製を待つと、待つ期限（timeouts.request）までエージェントのターンが止まる。
// 本体で開いているバッファが不正な UTF-8 を含む場合は、editor/open の応答より先に buffer/close が届き、
// WaitBuffer が server.ErrBufferClosed ですぐに返る（基本設計 6.7）。
func (w openAsBuffer) writeUnopened(ctx context.Context, path, content string) error {
	switch data, err := os.ReadFile(path); {
	case err == nil && !utf8.Valid(data):
		return fmt.Errorf("%s is not valid UTF-8; the editor does not pass such a buffer to extensions", path)
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return err
	}
	res, err := w.c.srv.OpenEditor(ctx, ext.EditorOpenParams{Path: path, Focus: false})
	if err != nil {
		return fmt.Errorf("editor/open: %w", err)
	}
	err = w.c.writeBuffer(ctx, res.Buffer, res.Version, content)
	if errors.Is(err, server.ErrBufferClosed) {
		return fmt.Errorf("%s: the editor did not pass the buffer to the extension (it may contain invalid UTF-8): %w", path, err)
	}
	return err
}

// writeBuffer は、バッファ id の全文を content に置き換え、保存の前の整形を通さずに書き出す（基本設計 6.5）。
// 複製がバージョン番号 version 以降になるのを待ってから編集を作る。
//
// バッファのテキストは、書き出したファイルが content と一致するように、複製の FinalNewline に合わせて作る（bufferText）。
//
// 本体のバッファが編集を作った後に変わっていて、編集か保存が通らなければ、新しい複製を待って writeAttempts 回まで試す。
// 待つ間か待つ前にバッファが閉じられたら、server.ErrBufferClosed を返す。
//
// req: FR-34
func (c *client) writeBuffer(ctx context.Context, id ext.BufferID, version ext.Version, content string) error {
	for range writeAttempts {
		b, err := c.srv.WaitBuffer(ctx, id, version)
		if err != nil {
			return err
		}
		text := bufferText(content, b.Text(), b.FinalNewline())
		base := b.Version()
		if b.Text() != text {
			last := b.LineCount() - 1
			res, eerr := c.srv.Edit(ctx, ext.BufferEditParams{Buffer: id, Transaction: ext.Transaction{
				BaseVersion: base,
				Edits: []ext.Edit{{
					Range: ext.Range{End: ext.Position{Line: last, Byte: len(b.Line(last))}},
					Text:  text,
				}},
			}})
			if eerr != nil {
				return fmt.Errorf("buffer/edit: %w", eerr)
			}
			if !res.Applied {
				version = res.Version
				continue
			}
			base = res.Version
		}
		sres, err := c.srv.SaveBuffer(ctx, ext.BufferSaveParams{Buffer: id, Version: &base, SkipHooks: true})
		if err != nil {
			return fmt.Errorf("buffer/save: %w", err)
		}
		if sres.Saved {
			return nil
		}
		if sres.Version == base {
			// バージョン番号が一致しても書き出せなかった（書き込みの権限が無いなど）。理由は本体が利用者へ表示している。
			return fmt.Errorf("buffer/save: buffer %d was not saved", id)
		}
		version = sres.Version
	}
	return fmt.Errorf("buffer %d kept changing while writing; gave up after %d attempts", id, writeAttempts)
}

// bufferText は、fs/write_text_file の content を書き出すために、バッファのテキストをどうするかを返す。
// cur は複製の現在のテキスト、finalNewline は複製の FinalNewline（本体が書き出すときに最後の行の後に改行を足すか）である。
//
// 拡張機能 API には FinalNewline を変える手段が無い（buffer/edit はテキストだけを変える）ので、テキストの側で合わせる。
//
//   - finalNewline が true: 本体が改行を足すので、content の末尾の改行 1 つを除く
//   - finalNewline が false: 本体は改行を足さないので、content をそのまま使う。末尾の改行は空の最後の行として残り、
//     書き出したファイルは content と一致する
//
// 複製が空で finalNewline が false のバッファは、行の無いバッファ（本体が無いファイルを開いたときの空のバッファなど）として、
// true と同じに扱う。行の無いバッファは空のファイルとして書き出すために false で届くが、編集で行ができると、
// 本体の末尾の改行を補う設定（Vim の 'fixendofline'。既定で有効）に従って改行を足すようになるためである。
//
// finalNewline が true のバッファへ、改行で終わらない content（空を含む）を書くと、書き出したファイルの末尾に改行が付く。
// これは末尾の改行を補う設定のとおりの結果で、Vim で 'fixendofline' のバッファを書き出したときと同じである。
func bufferText(content, cur string, finalNewline bool) string {
	if finalNewline || cur == "" {
		return strings.TrimSuffix(content, "\n")
	}
	return content
}
