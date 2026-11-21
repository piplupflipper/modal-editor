package server

import "github.com/piplupflipper/vi-imitation/ext"

// ReportProgress は、本体へ progress/report を送り、時間のかかる処理の進み具合を知らせる（基本設計 5.3、6.7。FR-178）。
// p.Kind が ext.ProgressBegin なら始め、ext.ProgressReport なら本文か百分率を変え、ext.ProgressEnd なら終える。
// 識別子 p.Token は拡張機能の中で一意にする。応答は待たない。どこから呼んでもよい。
//
// 本体は、続いている進み具合を編集の client の画面の右下に重ねて出す。始めた処理は、失敗や取り消しで止めたときも end で終える。
func (s *Server) ReportProgress(p ext.ProgressReportParams) error {
	return s.notify(ext.MethodProgressReport, p)
}
