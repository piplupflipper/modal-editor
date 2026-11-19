package main

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/piplupflipper/modal-editor/internal/editor"
	"github.com/piplupflipper/modal-editor/internal/protocol"
)

// 待つだけの client は、ファイルを絶対パスにし、MODAL_EDITOR_CLIENT の client を Parent にした Hello を送り、
// 理由の無い終わりの知らせで nil を、理由付きで誤りを、知らせの無い切断で errWaitServerLost を返す（FR-83）。
//
// req: FR-83
func TestWaitClient(t *testing.T) {
	getenv := func(name string) string {
		if name == editor.EnvClient {
			return "7"
		}
		return ""
	}
	for _, tt := range []struct {
		name  string
		reply []protocol.ServerMessage
		want  error
	}{
		{"タブが閉じられた", []protocol.ServerMessage{protocol.Exit{}}, nil},
		{"開けない", []protocol.ServerMessage{protocol.Exit{Reason: "x を開けない"}}, errors.New("x を開けない")},
		{"切断", nil, errWaitServerLost},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c, s := protocol.Pipe()
			done := make(chan error, 1)
			go func() { done <- waitClient(c, getenv, []string{"rel.txt"}, true) }()
			m, err := s.Recv()
			if err != nil {
				t.Fatal(err)
			}
			h, ok := m.(protocol.Hello)
			abs, _ := filepath.Abs("rel.txt")
			if !ok || h.Kind != protocol.KindWait || h.Parent != 7 || !h.ReadOnly || len(h.Files) != 1 || h.Files[0] != abs {
				t.Fatalf("Hello = %#v", m)
			}
			for _, r := range tt.reply {
				if serr := s.Send(r); serr != nil {
					t.Fatal(serr)
				}
			}
			_ = s.Close()
			err = <-done
			switch {
			case tt.want == nil && err != nil, tt.want != nil && (err == nil || err.Error() != tt.want.Error()):
				t.Errorf("waitClient = %v, want %v", err, tt.want)
			}
		})
	}
}
