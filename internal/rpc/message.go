// Package rpc は、JSON-RPC 2.0 のメッセージの読み書きと、リクエストとレスポンスの対応付けを行う。
//
// 本体と拡張機能は、子プロセスの標準入力と標準出力の上で、このパッケージの [Conn] を使って通信する（基本設計 5.3）。
// メッセージの区切りは LSP と同じ Content-Length ヘッダーの形式である。
// このパッケージはメッセージの中身の意味（メソッドごとの引数と結果の型）を持たない。
//
// このパッケージは基本設計 2 章の土台の層に属し、このリポジトリの他のパッケージを import しない。
package rpc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// Version は、このパッケージが読み書きする JSON-RPC のバージョンである。
// 受け取ったメッセージの jsonrpc の値がこれと異なれば、プロトコルの誤りとして扱う。
const Version = "2.0"

// CancelMethod は、期限を超えたリクエストの取り消しを相手に知らせる通知のメソッド名の既定値である（基本設計 5.3）。
// 引数は [CancelParams] である。接続ごとの名前は [WithCancelMethod] で変えられる（LSP なら "$/cancelRequest"）。
const CancelMethod = "$/cancel"

// CancelParams は、[CancelMethod] の通知の引数である。
type CancelParams struct {
	// ID は、取り消すリクエストの ID である。
	ID ID `json:"id"`
}

// JSON-RPC 2.0 が定めるエラーコードである。
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

// ID は、リクエストの ID である。
// JSON-RPC 2.0 の ID は整数と文字列のどちらでもよいので、どちらかを保持する。
// 比較可能な値なので、== で比べられ、map のキーにできる。
// ゼロ値は整数の 0 を表す。
type ID struct {
	num   int64
	str   string
	isStr bool
}

// NumberID は、整数の ID を返す。
func NumberID(n int64) ID { return ID{num: n} }

// StringID は、文字列の ID を返す。
func StringID(s string) ID { return ID{str: s, isStr: true} }

// String は、ID を人が読むための文字列にする。文字列の ID は引用符で囲む。
func (id ID) String() string {
	if id.isStr {
		return strconv.Quote(id.str)
	}
	return strconv.FormatInt(id.num, 10)
}

// MarshalJSON は、ID を JSON の数値または文字列にする。
func (id ID) MarshalJSON() ([]byte, error) {
	if id.isStr {
		return json.Marshal(id.str)
	}
	return []byte(strconv.FormatInt(id.num, 10)), nil
}

// UnmarshalJSON は、JSON の整数または文字列を ID にする。
// 小数、範囲外の整数、その他の型は誤りとする。
func (id *ID) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) > 0 && data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return fmt.Errorf("rpc: invalid id %s: %w", data, err)
		}
		*id = StringID(s)
		return nil
	}
	n, err := strconv.ParseInt(string(data), 10, 64)
	if err != nil {
		return fmt.Errorf("rpc: invalid id %s: %w", data, err)
	}
	*id = NumberID(n)
	return nil
}

// Request は、応答を求めるメッセージである。
type Request struct {
	// ID は、応答を対応付けるための ID である。
	ID ID
	// Method は、呼び出すメソッドの名前である。
	Method string
	// Params は、引数の JSON である。引数が無ければ nil である。
	Params json.RawMessage
}

// Notification は、応答を求めないメッセージである。
type Notification struct {
	// Method は、呼び出すメソッドの名前である。
	Method string
	// Params は、引数の JSON である。引数が無ければ nil である。
	Params json.RawMessage
}

// Response は、リクエストへの応答である。
// Error が nil なら成功で、Result に結果の JSON が入る。
type Response struct {
	// ID は、応答の対象のリクエストの ID である。
	ID ID
	// Result は、成功したときの結果の JSON である。結果が null の場合は "null" が入る。
	Result json.RawMessage
	// Error は、失敗したときの誤りである。成功したときは nil である。
	Error *Error
}

// Error は、JSON-RPC 2.0 のエラーオブジェクトである。error を実装する。
type Error struct {
	// Code は、エラーコードである。JSON-RPC 2.0 が定めるコードは Code で始まる定数にある。
	Code int64 `json:"code"`
	// Message は、誤りの短い説明である。
	Message string `json:"message"`
	// Data は、追加の情報の JSON である。無ければ nil である。
	Data json.RawMessage `json:"data,omitempty"`
}

// Error は、エラーコードと説明を 1 行の文字列にする。
func (e *Error) Error() string {
	return fmt.Sprintf("rpc: remote error %d: %s", e.Code, e.Message)
}

// ErrProtocol は、受け取ったバイト列が JSON-RPC 2.0 のメッセージとして解釈できないことを表す。
// ヘッダーの誤り、JSON の誤り、jsonrpc の値の誤り、必須の項目の欠けがこれにあたる。
// 個々の誤りはこの値を包んで返すので、errors.Is で判定する。
var ErrProtocol = errors.New("rpc: protocol error")

// wireMessage は、送受信する JSON の形である。リクエスト、通知、レスポンスのすべてを表す。
type wireMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *ID             `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// encodeRequest は、リクエストの JSON を作る。
func encodeRequest(id ID, method string, params any) ([]byte, error) {
	raw, err := marshalParams(params)
	if err != nil {
		return nil, err
	}
	return json.Marshal(wireMessage{JSONRPC: Version, ID: &id, Method: method, Params: raw})
}

// encodeNotification は、通知の JSON を作る。
func encodeNotification(method string, params any) ([]byte, error) {
	raw, err := marshalParams(params)
	if err != nil {
		return nil, err
	}
	return json.Marshal(wireMessage{JSONRPC: Version, Method: method, Params: raw})
}

// encodeResponse は、レスポンスの JSON を作る。rpcErr が nil でなければ失敗の応答にする。
func encodeResponse(id ID, result any, rpcErr *Error) ([]byte, error) {
	msg := wireMessage{JSONRPC: Version, ID: &id}
	if rpcErr != nil {
		msg.Error = rpcErr
		return json.Marshal(msg)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("rpc: marshal result: %w", err)
	}
	msg.Result = raw
	return json.Marshal(msg)
}

// marshalParams は、引数を JSON にする。nil なら params を省く。
func marshalParams(params any) (json.RawMessage, error) {
	if params == nil {
		return nil, nil
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("rpc: marshal params: %w", err)
	}
	return raw, nil
}

// decodeMessage は、1 件のメッセージの JSON を解釈し、*Request、*Notification、*Response のいずれかを返す。
// 解釈できなければ [ErrProtocol] を包んだ誤りを返す。
func decodeMessage(data []byte) (any, error) {
	var msg wireMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("%w: invalid json: %w", ErrProtocol, err)
	}
	if msg.JSONRPC != Version {
		return nil, fmt.Errorf("%w: unsupported jsonrpc version %q", ErrProtocol, msg.JSONRPC)
	}
	if msg.Method != "" {
		if msg.ID == nil {
			return &Notification{Method: msg.Method, Params: msg.Params}, nil
		}
		return &Request{ID: *msg.ID, Method: msg.Method, Params: msg.Params}, nil
	}
	if msg.ID == nil {
		return nil, fmt.Errorf("%w: message has neither method nor id", ErrProtocol)
	}
	hasResult := msg.Result != nil
	hasError := msg.Error != nil
	if hasResult == hasError {
		return nil, fmt.Errorf("%w: response must have exactly one of result and error", ErrProtocol)
	}
	return &Response{ID: *msg.ID, Result: msg.Result, Error: msg.Error}, nil
}
