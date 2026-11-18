package server_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/ext/server"
	"github.com/piplupflipper/modal-editor/internal/rpc"
)

// IsReadOnly は、code が ext.ErrorCodeReadOnly の誤りの応答（包んだものを含む）だけを見分ける。
func TestIsReadOnly(t *testing.T) {
	ro := &rpc.Error{Code: ext.ErrorCodeReadOnly, Message: "E21"}
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{ro, true},
		{fmt.Errorf("wrapped: %w", ro), true},
		{&rpc.Error{Code: rpc.CodeInvalidParams, Message: "x"}, false},
		{errors.New("E21"), false},
		{nil, false},
	} {
		if got := server.IsReadOnly(tc.err); got != tc.want {
			t.Errorf("IsReadOnly(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}
