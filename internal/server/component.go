package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/piplupflipper/modal-editor/internal/editor"
	"github.com/piplupflipper/modal-editor/internal/mux"
	"github.com/piplupflipper/modal-editor/internal/protocol"
)

// 部品の client（基本設計 1.2、1.3。FR-66、FR-67、FR-115）。
//
// server は、端末多重化ソフトの層（internal/mux。server を起動した環境で判定する）で中央のポップアップかペインを開き、
// その中で、この server のソケットに繋ぐ部品の client（本体の実行ファイルに -popup <部品の ID> か -pane <部品の ID> を渡したもの）を起動する。
// server のソケットは -connect <パス> で渡す（:diffsplit の splitPane と同じ）。
// 部品の client が Hello（protocol.KindPopup、KindPane）を送ったら、editor.Editor.AttachComponent で、その部品を開いた元の client に繋ぐ。

// ComponentFlagPopup と ComponentFlagPane は、部品の client を起動するときに本体の実行ファイルに渡すオプションの名前である（cmd/modal-editor）。
const (
	ComponentFlagPopup = "-popup"
	ComponentFlagPane  = "-pane"
)

// popupWidth と popupHeight は、中央のポップアップの大きさ（端末全体に対する百分率）である（仮。herdr の同梱のプラグインの既定と同じ）。
const popupWidth, popupHeight = 80, 80

// popupBusyTimeout と popupBusyRetry は、ポップアップが既に開いていて開けない（mux.ErrPopupBusy）ときに、開き直すのを待つ上限と間隔である（仮）。
// 前の部品を閉じて部品の client に終わりを知らせた直後は、端末多重化ソフトのポップアップがまだ閉じていないことがある
// （ヘルプからキーの一覧へ続けて開いたときなど）。
const (
	popupBusyTimeout = 1500 * time.Millisecond
	popupBusyRetry   = 50 * time.Millisecond
)

// componentCommand は、部品 view を描く部品の client を起動するコマンドと、端末多重化ソフトの層を返す。
// flag は ComponentFlagPopup か ComponentFlagPane である。
func (s *Server) componentCommand(flag string, view uint64, dir string) (mux.Mux, mux.Command, error) {
	sock, _ := s.sock.Load().(string)
	if sock == "" {
		return nil, mux.Command{}, errors.New("server のソケットで待ち受けていない")
	}
	m := mux.Detect(os.Getenv)
	if m.Kind() == mux.KindNone {
		return nil, mux.Command{}, mux.ErrUnsupported
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, mux.Command{}, fmt.Errorf("本体の実行ファイルのパスを得られない: %w", err)
	}
	// server のソケットは -connect で渡す（diffsplit.go の splitPane と同じ。環境変数 MODAL_EDITOR_SOCKET で渡すと、
	// 部品の client の中で起動したものが待つだけの client になりうるため）。
	return m, mux.Command{
		Args: []string{exe, flag, strconv.FormatUint(view, 10), "-connect", sock},
		Dir:  dir,
	}, nil
}

// openPopup は、editor.Config.OpenPopup である（FR-67）。from のペインの上に、端末多重化ソフトの中央のポップアップを開き、
// その中で部品 view を描くポップアップの部品の client を起動する。
// ポップアップが既に開いていれば（mux.ErrPopupBusy）、popupBusyTimeout まで開き直す。
//
// req: FR-66, FR-67
func (s *Server) openPopup(from string, view uint64, dir string) error {
	m, cmd, err := s.componentCommand(ComponentFlagPopup, view, dir)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(popupBusyTimeout)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), splitTimeout)
		err = m.OpenPopup(ctx, mux.PopupOptions{Target: from, Width: popupWidth, Height: popupHeight, Command: cmd})
		cancel()
		if !errors.Is(err, mux.ErrPopupBusy) || time.Now().After(deadline) {
			return herdrHint(err)
		}
		time.Sleep(popupBusyRetry)
	}
}

// openPane は、editor.Config.OpenPane である（FR-115）。from のペインを分割し、新しいペインで部品 view を描くペインの部品の client を起動する。
//
// req: FR-66, FR-115
func (s *Server) openPane(from string, view uint64, dir string, opts editor.PaneOptions) error {
	m, cmd, err := s.componentCommand(ComponentFlagPane, view, dir)
	if err != nil {
		return err
	}
	dirn := mux.Right
	if opts.Down {
		dirn = mux.Down
	}
	ctx, cancel := context.WithTimeout(context.Background(), splitTimeout)
	defer cancel()
	_, err = m.SplitPane(ctx, mux.SplitOptions{Target: from, Direction: dirn, Size: opts.Size, Command: cmd, Focus: opts.Focus})
	return herdrHint(err)
}

// herdrHint は、herdr が同梱のプラグインを知らない（登録していない）ことを表す誤りに、登録の手順を書き足す（基本設計 1.3）。
// 他の誤りはそのまま返す。
func herdrHint(err error) error {
	if he, ok := errors.AsType[*mux.HerdrError](err); ok && he.Code == mux.HerdrPluginNotFound {
		return fmt.Errorf("%w（herdr のプラグインを登録してください: herdr plugin link <配布物>/herdr-plugin）", err)
	}
	return err
}

// serveComponent は、部品の client の接続 conn を、Hello の View の部品を開いた元の client に繋ぐ（editor.Editor.AttachComponent）。
// 部品が無ければ（部品の client が繋ぐ前に閉じた）、理由の無い終わりの知らせで断る（部品の client は黙って終わり、
// 端末多重化ソフトのポップアップかペインが閉じる）。
//
// req: FR-67, FR-115
func (s *Server) serveComponent(a *acceptor, conn protocol.ServerConn, h protocol.Hello) {
	select {
	case <-a.done:
		s.reject(conn, "server が終わった")
		return
	default:
	}
	t := s.newTerm()
	t.attach(editor.ClientID(s.nextID.Add(1)), conn, h)
	s.track(t)
	if err := s.ed.AttachComponent(t, h.View, h.Kind == protocol.KindPane); err != nil {
		s.log.Info("部品の client を断った", "kind", h.Kind, "view", h.View, "err", err)
		t.end("")
		return
	}
	s.log.Info("部品の client を繋いだ", "kind", h.Kind, "view", h.View)
}
