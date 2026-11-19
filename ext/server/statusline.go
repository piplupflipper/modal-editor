package server

import "github.com/piplupflipper/modal-editor/ext"

// SetStatusLine は、本体へ statusline/set を送り、宣言ファイルの contributes.statusline で足した部品 part の中身を text にする
// （基本設計 7.1。FR-92）。style は部品の色の名前で、空なら利用者の設定の statusline.ext の色で描く。text が空なら部品を出さない。
// 応答は待たない。どこから呼んでもよい。
//
// 本体は部品ごとに最後の中身を持つので、変わったときだけ送ればよい。定期的に変わる値（メモリの使用量など）は、
// 拡張機能が自分の goroutine で間隔を決めて送る。
func (s *Server) SetStatusLine(part, text, style string) error {
	return s.notify(ext.MethodStatusLineSet, ext.StatusLineSetParams{Part: part, Text: text, Style: style})
}
