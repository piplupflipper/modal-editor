package rpc

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestIDJSON(t *testing.T) {
	tests := []struct {
		id   ID
		json string
	}{
		{NumberID(0), `0`},
		{NumberID(42), `42`},
		{NumberID(-7), `-7`},
		{StringID("abc"), `"abc"`},
		{StringID(""), `""`},
		{StringID("42"), `"42"`},
	}
	for _, tt := range tests {
		got, err := json.Marshal(tt.id)
		if err != nil {
			t.Fatalf("Marshal(%v): %v", tt.id, err)
		}
		if string(got) != tt.json {
			t.Errorf("Marshal(%v) = %s, want %s", tt.id, got, tt.json)
		}
		var back ID
		if err := json.Unmarshal([]byte(tt.json), &back); err != nil {
			t.Fatalf("Unmarshal(%s): %v", tt.json, err)
		}
		if back != tt.id {
			t.Errorf("Unmarshal(%s) = %v, want %v", tt.json, back, tt.id)
		}
	}
	// 整数の 42 と文字列の "42" は別の ID である。
	if NumberID(42) == StringID("42") {
		t.Error(`NumberID(42) == StringID("42")`)
	}
}

func TestIDUnmarshalInvalid(t *testing.T) {
	for _, in := range []string{`1.5`, `true`, `{}`, `[]`, `99999999999999999999`} {
		var id ID
		if err := json.Unmarshal([]byte(in), &id); err == nil {
			t.Errorf("Unmarshal(%s) succeeded with %v, want error", in, id)
		}
	}
}

func TestDecodeMessage(t *testing.T) {
	t.Run("request", func(t *testing.T) {
		msg, err := decodeMessage([]byte(`{"jsonrpc":"2.0","id":3,"method":"ping","params":{"a":1}}`))
		if err != nil {
			t.Fatal(err)
		}
		req, ok := msg.(*Request)
		if !ok {
			t.Fatalf("got %T, want *Request", msg)
		}
		if req.ID != NumberID(3) || req.Method != "ping" || string(req.Params) != `{"a":1}` {
			t.Errorf("got %+v", req)
		}
	})
	t.Run("notification", func(t *testing.T) {
		msg, err := decodeMessage([]byte(`{"jsonrpc":"2.0","method":"buffer/close"}`))
		if err != nil {
			t.Fatal(err)
		}
		n, ok := msg.(*Notification)
		if !ok {
			t.Fatalf("got %T, want *Notification", msg)
		}
		if n.Method != "buffer/close" || n.Params != nil {
			t.Errorf("got %+v", n)
		}
	})
	t.Run("null result is success", func(t *testing.T) {
		msg, err := decodeMessage([]byte(`{"jsonrpc":"2.0","id":"x","result":null}`))
		if err != nil {
			t.Fatal(err)
		}
		resp, ok := msg.(*Response)
		if !ok {
			t.Fatalf("got %T, want *Response", msg)
		}
		if resp.ID != StringID("x") || resp.Error != nil || string(resp.Result) != "null" {
			t.Errorf("got %+v", resp)
		}
	})
	t.Run("error response", func(t *testing.T) {
		msg, err := decodeMessage([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"nope"}}`))
		if err != nil {
			t.Fatal(err)
		}
		resp, ok := msg.(*Response)
		if !ok {
			t.Fatalf("got %T, want *Response", msg)
		}
		if resp.Error == nil || resp.Error.Code != CodeMethodNotFound || resp.Error.Message != "nope" {
			t.Errorf("got %+v", resp)
		}
	})
}

func TestDecodeMessageInvalid(t *testing.T) {
	tests := map[string]string{
		"not json":            `{"jsonrpc":`,
		"not object":          `[1,2]`,
		"missing version":     `{"id":1,"method":"ping"}`,
		"wrong version":       `{"jsonrpc":"1.0","id":1,"method":"ping"}`,
		"no method no id":     `{"jsonrpc":"2.0"}`,
		"response no payload": `{"jsonrpc":"2.0","id":1}`,
		"result and error":    `{"jsonrpc":"2.0","id":1,"result":1,"error":{"code":1,"message":"x"}}`,
		"bad id":              `{"jsonrpc":"2.0","id":1.5,"method":"ping"}`,
	}
	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			msg, err := decodeMessage([]byte(in))
			if !errors.Is(err, ErrProtocol) {
				t.Errorf("decodeMessage(%s) = %v, %v; want ErrProtocol", in, msg, err)
			}
		})
	}
}
