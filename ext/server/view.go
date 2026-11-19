package server

import (
	"context"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/internal/rpc"
)

// この節は、ポップアップと表示の部品（ext の view.go。基本設計 1.4、5.3 の段階 3 の表）を扱う。

// OpenPopup は、本体へ popup/open を送り、表示の部品を中身にしたポップアップを開かせる。応答はポップアップの ID である。
// p が ext.PopupOpenParams.Validate を通らなければ、送らずにその誤りを返す。
// Edit と同じく、ループの上から呼ぶと ErrEditInHandler を返す。
func (s *Server) OpenPopup(ctx context.Context, p ext.PopupOpenParams) (ext.PopupOpenResult, error) {
	if err := p.Validate(); err != nil {
		return ext.PopupOpenResult{}, err
	}
	return call[ext.PopupOpenResult](ctx, s, ext.MethodPopupOpen, p)
}

// UpdateView は、本体へ view/update を送り、開いたポップアップの部品の中身を置き換えさせる。応答は待たない。どこから呼んでもよい。
// p.Component が ext.ViewComponent.Validate を通らなければ、送らずにその誤りを返す。
func (s *Server) UpdateView(p ext.ViewUpdateParams) error {
	if err := p.Component.Validate(); err != nil {
		return err
	}
	return s.notify(ext.MethodViewUpdate, p)
}

// CloseView は、本体へ view/close を送り、開いたポップアップを閉じさせる。応答は待たない。どこから呼んでもよい。
// 本体は、この知らせで閉じたポップアップについて view/closed を送らない。
func (s *Server) CloseView(id ext.ViewID) error {
	return s.notify(ext.MethodViewClose, ext.ViewCloseParams{View: id})
}

// ViewEventKind は、ViewEvent の種類である。
type ViewEventKind int

// ViewEvent の種類。
const (
	// ViewClosed は、view/closed である。利用者か本体がポップアップを閉じた。
	ViewClosed ViewEventKind = iota
	// ViewSelected は、view/select である。Item に項目の ID（入力の行では中身）が入る。
	ViewSelected
	// ViewInputChanged は、input/change である。Item に入力の行の中身が入る。
	ViewInputChanged
	// ViewKey は、view/key である。Key に contributes.popupKeys の項目の ID が、Item にカーソルの行の項目の ID が入る。
	ViewKey
	// ViewExpanded は、view/expand である。Item に開閉した木の項目の ID が、Expanded に開いたかどうかが入る。
	ViewExpanded
)

// ViewEvent は、本体からポップアップの利用者の操作の知らせ（view/closed、view/select、input/change、view/key）を受け取ったことを表す。
type ViewEvent struct {
	Kind ViewEventKind
	// View は、ポップアップの ID である。
	View ext.ViewID
	// Item は、選んだ項目の ID か、入力の行の中身である。ViewClosed では空である。
	Item string
	// Key は、ViewKey のときの contributes.popupKeys の項目の ID である。
	Key string
	// Expanded は、ViewExpanded のときの、項目を開いたかどうかである。
	Expanded bool
}

// OnView は、ポップアップの利用者の操作の知らせを受け取ったときに呼ぶ関数を登録する。Serve の後に呼ぶと panic する。
// 他のフックと同じくループの上で呼ばれるので、f の中で本体の応答を待つ関数（OpenPopup など）をこの ctx で呼ぶと ErrEditInHandler になる。
// 応答を待たない UpdateView と CloseView は呼んでよい。
func (s *Server) OnView(f func(ctx context.Context, e *ViewEvent)) {
	s.mustNotStarted()
	s.onView = f
}

// viewNotification は、ポップアップの知らせ n を ViewEvent にして OnView のフックへ渡す。知らせでなければ何もしない。
func (s *Server) viewNotification(ctx context.Context, n *rpc.Notification) {
	var e ViewEvent
	var err error
	switch n.Method {
	case ext.MethodViewClosed:
		var p ext.ViewClosedParams
		err = decodeParams(n, &p)
		e = ViewEvent{Kind: ViewClosed, View: p.View}
	case ext.MethodViewSelect:
		var p ext.ViewSelectParams
		err = decodeParams(n, &p)
		e = ViewEvent{Kind: ViewSelected, View: p.View, Item: p.Item}
	case ext.MethodInputChange:
		var p ext.InputChangeParams
		err = decodeParams(n, &p)
		e = ViewEvent{Kind: ViewInputChanged, View: p.View, Item: p.Text}
	case ext.MethodViewKey:
		var p ext.ViewKeyParams
		err = decodeParams(n, &p)
		e = ViewEvent{Kind: ViewKey, View: p.View, Item: p.Item, Key: p.ID}
	case ext.MethodViewExpand:
		var p ext.ViewExpandParams
		err = decodeParams(n, &p)
		e = ViewEvent{Kind: ViewExpanded, View: p.View, Item: p.Item, Expanded: p.Expanded}
	default:
		return
	}
	if err != nil {
		s.logf("%v", err)
		return
	}
	if s.onView != nil {
		s.hook("view hook", func() { s.onView(ctx, &e) })
	}
}
