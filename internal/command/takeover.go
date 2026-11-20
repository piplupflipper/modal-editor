package command

import "slices"

// TakeOver は、カーソルのあるバッファが prev の State からこの State へ移るときに、Vim でエディタ全体に 1 つの状態を引き継ぐ。
// 呼び出し側（internal/editor）は、バッファを切り替えるたびと、バッファの State を作り直すたびに呼ぶ。
//
// 引き継ぐのは、レジスター、直前の検索（n、N）と一致の色を消した状態（:nohlsearch）、直前の :s（:s//~/ と ~）、直前の f、F、t、T（;、,）、
// ドットリピートの値、@@ のレジスター、マクロの記録である。
// マクロを記録している途中なら、prev の記録を止めずにこの State へ移し、この State で続ける（qa:bn<CR>jq は、
// :bn の前後のキーをまとめて a に記録する。Vim 9.1 と同じ）。prev は記録していない状態に戻す。
//
// req: FR-05, FR-06
func (s *State) TakeOver(prev *State) {
	if prev == nil || prev == s {
		return
	}
	s.TakeOverShared(prev)
	s.recording, s.recorded = prev.recording, prev.recorded
	s.parser.SetRecording(s.recording != 0)
	if prev.recording != 0 {
		prev.recording, prev.recorded = 0, nil
		prev.parser.SetRecording(false)
	}
}

// TakeOverShared は、TakeOver のうち、マクロの記録を除いたもの（レジスター、直前の検索、直前の :s、直前の f、ドットリピートの値、
// @@ のレジスター）を prev から引き継ぐ。記録の状態は、この State と prev のそれぞれに残す。
//
// 段階 3 の server で、キーを打つ編集の client が替わるときに呼ぶ（基本設計 1.2、4.4）。
// レジスターなどは client の間で共有し（FR-58）、マクロの記録は記録を始めた client のキーだけを記録する（基本設計 10 章の仮の扱い）。
//
// req: FR-58
func (s *State) TakeOverShared(prev *State) {
	if prev == nil || prev == s {
		return
	}
	// 履歴の配列は push、replace、Promote がその場で書き換えるので、複製して prev と共有しない。
	// 切り替えの後も、prev の State には保留した拡張機能の結果（ApplyDeferred）が書き込みうる（PR #166 の再レビュー）。
	s.registers = prev.registers
	s.registers.history = slices.Clone(prev.registers.history)
	// 一致の色を消した状態（FR-138）は、直前の検索と組で引き継ぐ。値は消したときに作り、後から書き換えないので共有してよい。
	s.Ex.LastPattern, s.searchBackward, s.Ex.ClearedHighlight = prev.Ex.LastPattern, prev.searchBackward, prev.Ex.ClearedHighlight
	s.Ex.LastSubstitute = prev.Ex.LastSubstitute
	s.lastFind = prev.lastFind
	s.dot, s.lastChange, s.dotVisual, s.dotLines = prev.dot, prev.lastChange, prev.dotVisual, prev.dotLines
	s.lastPlayed = prev.lastPlayed
}
