package rpc

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
)

// DefaultMaxMessageSize は、受信する 1 件のメッセージの本文の上限のバイト数の既定値である。
// 壊れた相手が巨大な長さを送ってきたときに、本体のメモリを使い切らないための上限である。
// 接続ごとの値は [WithMaxMessageSize] で変えられる。64 MiB は仮の値で、根拠は無い。
const DefaultMaxMessageSize = 64 << 20

// ReadMessage は、r から Content-Length ヘッダーで区切られたメッセージを 1 件読み、本文を返す。
// Content-Length が maxSize を超えるメッセージは、本文を読まずにプロトコルの誤りとする。
//
// ヘッダーは LSP と同じ形式で、各行を "\r\n" で終え、空行で本文と区切る。
// Content-Length 以外のヘッダー（Content-Type など）は読み飛ばす。ヘッダー名の大文字と小文字は区別しない。
//
// メッセージの境目で入力が終わっていれば io.EOF を返す。
// メッセージの途中で終わっていれば io.ErrUnexpectedEOF を返す。
// ヘッダーの形が誤っていれば [ErrProtocol] を包んだ誤りを返す。
func ReadMessage(r *bufio.Reader, maxSize int) ([]byte, error) {
	length := -1
	first := true
	for {
		line, err := r.ReadSlice('\n')
		if err != nil {
			switch {
			case errors.Is(err, io.EOF) && first && len(line) == 0:
				return nil, io.EOF
			case errors.Is(err, io.EOF):
				return nil, io.ErrUnexpectedEOF
			case errors.Is(err, bufio.ErrBufferFull):
				return nil, fmt.Errorf("%w: header line too long", ErrProtocol)
			default:
				return nil, err
			}
		}
		first = false
		if !bytes.HasSuffix(line, []byte("\r\n")) {
			return nil, fmt.Errorf("%w: header line not terminated by CRLF", ErrProtocol)
		}
		line = line[:len(line)-2]
		if len(line) == 0 {
			break
		}
		name, value, ok := bytes.Cut(line, []byte(":"))
		if !ok {
			return nil, fmt.Errorf("%w: malformed header line %q", ErrProtocol, line)
		}
		if !bytes.EqualFold(bytes.TrimSpace(name), []byte("Content-Length")) {
			continue
		}
		if length >= 0 {
			return nil, fmt.Errorf("%w: duplicate Content-Length", ErrProtocol)
		}
		n, err := strconv.Atoi(string(bytes.TrimSpace(value)))
		if err != nil || n < 0 {
			return nil, fmt.Errorf("%w: invalid Content-Length %q", ErrProtocol, value)
		}
		if n > maxSize {
			return nil, fmt.Errorf("%w: Content-Length %d exceeds limit %d", ErrProtocol, n, maxSize)
		}
		length = n
	}
	if length < 0 {
		return nil, fmt.Errorf("%w: missing Content-Length", ErrProtocol)
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return body, nil
}

// WriteMessage は、body に Content-Length ヘッダーを付けて w へ書く。
// 並行に呼ばれる前提を置かないので、複数の goroutine から書くときは呼び出し側で直列化する。
func WriteMessage(w io.Writer, body []byte) error {
	buf := make([]byte, 0, len(body)+32)
	buf = fmt.Appendf(buf, "Content-Length: %d\r\n\r\n", len(body))
	buf = append(buf, body...)
	_, err := w.Write(buf)
	return err
}
