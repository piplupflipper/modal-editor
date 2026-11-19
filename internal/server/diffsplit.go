package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/piplupflipper/modal-editor/internal/mux"
)

// envSocket は、server のソケットのパスを渡す環境変数である（基本設計 1.2 のソケットのパスの規則 1。cmd/modal-editor の envSocket と同じ名前）。
const envSocket = "MODAL_EDITOR_SOCKET"

// splitTimeout は、:diffsplit でペインを分割するときに、端末多重化ソフトの操作を待つ上限である（仮。FR-63 のフォーカスの移動と同じ値）。
const splitTimeout = 2 * time.Second

// splitPane は、editor.Config.SplitPane である（:diffsplit。基本設計 6.8。FR-99）。
// 端末多重化ソフトの層（server を起動した環境で判定する。基本設計 1.3）で from のペインを右に分割し、
// そこでこの server のソケットに繋ぐ編集の client（本体の実行ファイルに file を渡したもの）を、dir で起動する。
// server のソケットは環境変数 MODAL_EDITOR_SOCKET で渡す（ソケットのパスの規則 1）。
// 新しい client がそのファイルのタブを開いて繋いだら、イベントループが見比べを始める。
func (s *Server) splitPane(from, file, dir string) error {
	sock, _ := s.sock.Load().(string)
	if sock == "" {
		return errors.New("server のソケットで待ち受けていない")
	}
	m := mux.Detect(os.Getenv)
	if m.Kind() == mux.KindNone {
		return mux.ErrUnsupported
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("本体の実行ファイルのパスを得られない: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), splitTimeout)
	defer cancel()
	_, err = m.SplitPane(ctx, mux.SplitOptions{
		Target:    from,
		Direction: mux.Right,
		Command:   mux.Command{Args: []string{exe, file}, Env: map[string]string{envSocket: sock}, Dir: dir},
		Focus:     true,
	})
	return err
}
