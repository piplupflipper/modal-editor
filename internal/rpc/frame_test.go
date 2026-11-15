package rpc

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestWriteReadMessage(t *testing.T) {
	var buf bytes.Buffer
	bodies := []string{`{"a":1}`, ``, `{"text":"日本語"}`}
	for _, b := range bodies {
		if err := WriteMessage(&buf, []byte(b)); err != nil {
			t.Fatal(err)
		}
	}
	if !strings.HasPrefix(buf.String(), "Content-Length: 7\r\n\r\n{\"a\":1}") {
		t.Errorf("unexpected encoding: %q", buf.String())
	}
	r := bufio.NewReader(&buf)
	for _, want := range bodies {
		got, err := ReadMessage(r, DefaultMaxMessageSize)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("ReadMessage = %q, want %q", got, want)
		}
	}
	if _, err := ReadMessage(r, DefaultMaxMessageSize); !errors.Is(err, io.EOF) {
		t.Errorf("ReadMessage at end = %v, want io.EOF", err)
	}
}

func TestReadMessageHeaders(t *testing.T) {
	// Content-Length 以外のヘッダーは読み飛ばし、ヘッダー名の大文字と小文字は区別しない。
	in := "Content-Type: application/vscode-jsonrpc; charset=utf-8\r\ncontent-length:  2 \r\n\r\n{}"
	got, err := ReadMessage(bufio.NewReader(strings.NewReader(in)), DefaultMaxMessageSize)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "{}" {
		t.Errorf("got %q", got)
	}
}

func TestReadMessageBroken(t *testing.T) {
	tests := map[string]struct {
		in   string
		want error
	}{
		"missing length":   {"Content-Type: x\r\n\r\n{}", ErrProtocol},
		"negative length":  {"Content-Length: -1\r\n\r\n", ErrProtocol},
		"non-number":       {"Content-Length: ab\r\n\r\n", ErrProtocol},
		"duplicate length": {"Content-Length: 2\r\nContent-Length: 2\r\n\r\n{}", ErrProtocol},
		"too large":        {"Content-Length: 999999999999\r\n\r\n", ErrProtocol},
		"over limit":       {"Content-Length: 67108865\r\n\r\n", ErrProtocol},
		"no colon":         {"Content-Length 2\r\n\r\n{}", ErrProtocol},
		"LF only":          {"Content-Length: 2\n\n{}", ErrProtocol},
		"long header line": {"X: " + strings.Repeat("a", 10000) + "\r\n", ErrProtocol},
		"eof in header":    {"Content-Length: 2\r\n", io.ErrUnexpectedEOF},
		"eof mid line":     {"Content-Len", io.ErrUnexpectedEOF},
		"eof in body":      {"Content-Length: 10\r\n\r\n{}", io.ErrUnexpectedEOF},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := ReadMessage(bufio.NewReader(strings.NewReader(tt.in)), DefaultMaxMessageSize)
			if !errors.Is(err, tt.want) {
				t.Errorf("ReadMessage(%q) = %q, %v; want %v", tt.in, got, err, tt.want)
			}
		})
	}
}
