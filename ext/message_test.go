package ext

import (
	"encoding/json"
	"reflect"
	"testing"
)

// roundTrip は、v を JSON にして want と比べ、読み戻した値が v と等しいことを確かめる。
func roundTrip[T any](t *testing.T, v T, want string) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if got := string(data); got != want {
		t.Errorf("Marshal = %s, want %s", got, want)
	}
	var back T
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(back, v) {
		t.Errorf("Unmarshal = %#v, want %#v", back, v)
	}
}

var (
	testRange = Range{Start: Position{Line: 1, Byte: 2}, End: Position{Line: 3, Byte: 4}}
	testRJSON = `{"start":{"line":1,"byte":2},"end":{"line":3,"byte":4}}`
)

// req: FR-15
func TestTransactionJSON(t *testing.T) {
	roundTrip(t, Transaction{
		BaseVersion: 7,
		Edits: []Edit{
			{Range: testRange, Text: "あ"},
			{Range: Range{}, Text: ""},
		},
	}, `{"baseVersion":7,"edits":[{"range":`+testRJSON+`,"text":"あ"},{"range":{"start":{"line":0,"byte":0},"end":{"line":0,"byte":0}},"text":""}]}`)
}

// req: FR-11, FR-12, FR-15, FR-16, FR-17, FR-35
func TestMessageJSON(t *testing.T) {
	tx := Transaction{BaseVersion: 3, Edits: []Edit{{Range: testRange, Text: "x"}}}
	txJSON := `{"baseVersion":3,"edits":[{"range":` + testRJSON + `,"text":"x"}]}`

	t.Run("initialize", func(t *testing.T) {
		roundTrip(t, InitializeParams{APIVersion: APIVersion, Settings: json.RawMessage(`{"a":1}`)},
			`{"apiVersion":"0.1.0","settings":{"a":1}}`)
		roundTrip(t, InitializeParams{APIVersion: APIVersion}, `{"apiVersion":"0.1.0"}`)
		roundTrip(t, InitializeResult{APIVersion: APIVersion}, `{"apiVersion":"0.1.0"}`)
	})
	t.Run("buffer/open", func(t *testing.T) {
		roundTrip(t, BufferOpenParams{Buffer: 1, Path: "/a.md", FileType: "markdown", Version: 0, Text: "a\nb"},
			`{"buffer":1,"path":"/a.md","fileType":"markdown","version":0,"text":"a\nb"}`)
		roundTrip(t, BufferOpenParams{Buffer: 2}, `{"buffer":2,"version":0,"text":""}`)
	})
	t.Run("buffer/change", func(t *testing.T) {
		roundTrip(t, BufferChangeParams{Buffer: 1, FromVersion: 4, ToVersion: 5, Edits: []Edit{{Range: testRange, Text: "x"}}},
			`{"buffer":1,"fromVersion":4,"toVersion":5,"edits":[{"range":`+testRJSON+`,"text":"x"}]}`)
	})
	t.Run("buffer/close", func(t *testing.T) {
		roundTrip(t, BufferCloseParams{Buffer: 9}, `{"buffer":9}`)
	})
	t.Run("motion/resolve", func(t *testing.T) {
		roundTrip(t, MotionResolveParams{Buffer: 1, Version: 2, ID: "m", Cursor: Position{Line: 5, Byte: 6}, Count: 3},
			`{"buffer":1,"version":2,"id":"m","cursor":{"line":5,"byte":6},"count":3}`)
		roundTrip(t, MotionResolveResult{Version: 2, Position: Position{Line: 7, Byte: 8}},
			`{"version":2,"position":{"line":7,"byte":8}}`)
	})
	t.Run("textObject/resolve", func(t *testing.T) {
		roundTrip(t, TextObjectResolveParams{Buffer: 1, Version: 2, ID: "o", Cursor: Position{Line: 5, Byte: 6}, Count: 1},
			`{"buffer":1,"version":2,"id":"o","cursor":{"line":5,"byte":6},"count":1}`)
		roundTrip(t, TextObjectResolveResult{Version: 2, Range: testRange},
			`{"version":2,"range":`+testRJSON+`}`)
	})
	t.Run("operator/apply", func(t *testing.T) {
		roundTrip(t, OperatorApplyParams{Buffer: 1, Version: 3, ID: "op", Range: testRange, Count: 1},
			`{"buffer":1,"version":3,"id":"op","range":`+testRJSON+`,"count":1}`)
		roundTrip(t, OperatorApplyResult{Transaction: tx}, `{"transaction":`+txJSON+`}`)
	})
	t.Run("ping", func(t *testing.T) {
		roundTrip(t, PingParams{}, `{}`)
		roundTrip(t, PingResult{}, `{}`)
	})
	t.Run("$/cancel", func(t *testing.T) {
		roundTrip(t, CancelParams{ID: json.RawMessage(`12`)}, `{"id":12}`)
		roundTrip(t, CancelParams{ID: json.RawMessage(`"req-1"`)}, `{"id":"req-1"}`)
	})
	t.Run("buffer/edit", func(t *testing.T) {
		roundTrip(t, BufferEditParams{Buffer: 1, Transaction: tx}, `{"buffer":1,"transaction":`+txJSON+`}`)
		roundTrip(t, BufferEditResult{Applied: false, Version: 4}, `{"applied":false,"version":4}`)
	})
	t.Run("decoration/set", func(t *testing.T) {
		roundTrip(t, DecorationSetParams{Buffer: 1, Version: 3, Decorations: []Decoration{{Range: testRange, Style: "keyword"}}},
			`{"buffer":1,"version":3,"decorations":[{"range":`+testRJSON+`,"style":"keyword"}]}`)
	})
	t.Run("message/show", func(t *testing.T) {
		roundTrip(t, MessageShowParams{Level: MessageWarning, Text: "注意"}, `{"level":"warning","text":"注意"}`)
	})
}

// TestMethodNames は、メソッドの名前が基本設計 5.3 の表と一致することを確かめる。
func TestMethodNames(t *testing.T) {
	got := []string{
		MethodInitialize, MethodBufferOpen, MethodBufferChange, MethodBufferClose,
		MethodMotionResolve, MethodTextObjectResolve, MethodOperatorApply,
		MethodPing, MethodCancel, MethodBufferEdit, MethodDecorationSet, MethodMessageShow,
	}
	want := []string{
		"initialize", "buffer/open", "buffer/change", "buffer/close",
		"motion/resolve", "textObject/resolve", "operator/apply",
		"ping", "$/cancel", "buffer/edit", "decoration/set", "message/show",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("method names = %q, want %q", got, want)
	}
}
