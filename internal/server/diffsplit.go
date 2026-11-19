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
// この環境変数のもとで起動した modal-editor は、待つだけの client になる（基本設計 4.9。FR-83）。
const envSocket = "MODAL_EDITOR_SOCKET"

// splitTimeout は、:diffsplit でペインを分割するときに、端末多重化ソフトの操作を待つ上限である（仮。FR-63 のフォーカスの移動と同じ値）。
const splitTimeout = 2 * time.Second

// splitPane は、editor.Config.SplitPane である（:diffsplit。基本設計 6.8。FR-99）。
// 端末多重化ソフトの層（server を起動した環境で判定する。基本設計 1.3）で from のペインを右に分割し、
// そこでこの server に繋ぐ編集の client（本体の実行ファイルに file を渡したもの）を、dir で起動する。
// 新しい client がそのファイルのタブを開いて繋いだら、イベントループが見比べを始める。
//
// 新しいペインは from と同じ tmux の window（herdr のタブ）にあるので、新しい client はソケットのパスの規則 2 と 3 でこの server に繋ぐ。
// 環境変数 MODAL_EDITOR_SOCKET で渡すと、#226 の後は待つだけの client（FR-83）になり、ファイルを from の client のタブに開いて
// 新しいペインには何も出さないので（#245 で見つけた）、渡さない。端末多重化ソフトのサーバーの環境に残っていても効かないよう、空にする。
// from の client が別の window へ動いた後（FR-112）は、新しい client が別の server に繋ぎ、見比べは始まらない（仮。基本設計 10 章）。
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
	args := []string{exe}
	if file != "" {
		// 空なら、ファイルを渡さずに起動する（拡張機能の diff/start。新しい client のタブは server が決める）。
		args = append(args, file)
	}
	ctx, cancel := context.WithTimeout(context.Background(), splitTimeout)
	defer cancel()
	_, err = m.SplitPane(ctx, mux.SplitOptions{
		Target:    from,
		Direction: mux.Right,
		Command:   mux.Command{Args: args, Env: map[string]string{envSocket: ""}, Dir: dir},
		Focus:     true,
	})
	return err
}
