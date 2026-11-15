package command

import (
	"github.com/piplupflipper/modal-editor/internal/editcmd"
)

// maxPlayDepth は、マクロの再生の入れ子の上限である。マクロが自分を呼ぶ（qa@aq）と、失敗するまで再帰するための歯止めである。
const maxPlayDepth = 100

// startRecording は、q{reg} である。以後 Feed に渡したキーを記録する（FR-05）。
func (s *State) startRecording(name rune) error {
	if s.recording != 0 {
		return failf("already recording")
	}
	if !recordable(name) {
		return failf("invalid register %q", name)
	}
	s.recording = name
	s.recorded = nil
	s.parser.SetRecording(true)
	return nil
}

// recordable は、マクロの記録先にできる名前であるかを返す。
func recordable(name rune) bool {
	return name >= 'a' && name <= 'z' || name >= 'A' && name <= 'Z' || name >= '0' && name <= '9' || name == '"'
}

// stopRecording は、記録中の q である。記録したキーの列（止めた q を除く）をレジスターに入れる。
// 大文字の名前は追記する。Vim と同じく、無名のレジスターは変えない。
//
// req: FR-05
func (s *State) stopRecording() {
	if s.recording == 0 {
		return
	}
	keys := s.recorded
	if n := len(keys); n > 0 {
		keys = keys[:n-1]
	}
	reg := Register{Text: rawText(keys), keys: append([]editcmd.Key{}, keys...)}
	name := s.recording
	if p := s.registers.slot(name); p != nil {
		if name >= 'A' && name <= 'Z' {
			reg = p.appendTo(reg)
		}
		*p = reg
	}
	s.recording = 0
	s.recorded = nil
	s.parser.SetRecording(false)
}

// play は、@{reg} と @@ である。レジスターのキーの列を、回数だけ 1 個ずつ Feed と同じ解釈に渡す（FR-05）。
// 途中の操作が失敗したら、そこで再生を止める（Vim と同じ）。@: は、直前のコマンドラインを実行し直す。
// 再生 1 回（回数の繰り返しを含む）の変更は、Vim の `:help undo-blocks` と同じくアンドゥの 1 単位にまとめる（FR-07）。
//
// req: FR-05, FR-07
func (s *State) play(cmd editcmd.Command) error {
	name := cmd.Char
	if name == '@' {
		name = s.lastPlayed
		if name == 0 {
			return failf("no previously used register")
		}
	}
	n := max(cmd.Count, 1)
	s.buf.BeginUndoGroup()
	defer s.endPlayGroup()
	if name == ':' {
		if s.lastCmd == "" {
			return failf("no previous command line")
		}
		s.lastPlayed = name
		for range n {
			s.runCommandLine(s.lastCmd)
			if s.res.Err != nil {
				break
			}
		}
		return nil
	}
	reg, ok := s.registers.Get(name)
	if !ok {
		return failf("invalid register %q", name)
	}
	if s.playDepth >= maxPlayDepth {
		return failf("macro nested too deeply")
	}
	s.lastPlayed = name
	keys := reg.macroKeys()
	s.playDepth++
	defer func() { s.playDepth-- }()
	for range n {
		for _, k := range keys {
			s.feed(k)
			if s.res.Err != nil {
				return nil
			}
		}
	}
	if s.mode == ModeNormal || s.mode.visual() {
		s.runResults(s.parser.Flush())
	}
	return nil
}

// endPlayGroup は、マクロの再生全体をまとめたアンドゥのグループを閉じる。
// 再生がインサートモードで終わった場合（"iX" を @ で再生）は、インサートモードを抜けるときに一緒に閉じる。
func (s *State) endPlayGroup() {
	if s.mode == ModeInsert {
		s.insert.extraGroups++
		return
	}
	s.buf.EndUndoGroup()
}

// recordDot は、実行した値を、ドットリピートの対象であれば記録する（FR-06）。
// インサートモードへ移る値は、インサートモードを抜けるときに finishInsert が記録する。
func (s *State) recordDot(cmd editcmd.Command) {
	if cmd.Mode == editcmd.ModeInsert || !repeatable(cmd) {
		return
	}
	c := cmd
	s.dot = &c
}

// repeatable は、ドットリピートで繰り返す値であるかを返す。
// ヤンク、移動、アンドゥ、モードの切り替えは繰り返さない。拡張機能のオペレーターと操作は、全て繰り返す（FR-12）。
func repeatable(cmd editcmd.Command) bool {
	switch cmd.Kind {
	case editcmd.KindOperator:
		return !isYank(cmd.Operator)
	case editcmd.KindAction:
		return !cmd.Action.Builtin() || repeatableActions[cmd.Action.ID]
	case editcmd.KindMotion, editcmd.KindRecordStart, editcmd.KindRecordStop, editcmd.KindPlay, editcmd.KindRepeat:
	}
	return false
}

// repeat は、. である。直前の変更の値をもう一度実行する（FR-06）。
//
// 回数を付けると、記録した値の回数を置き換える（2d3w の後の 3. は d3w）。置き換えた回数は以後の . にも残る。
// "1p の後の . は、"2p、"3p のようにレジスターの番号を 1 つ進める（Vim と同じ）。
// ビジュアルモードのオペレーターは、カーソルから同じ大きさの範囲に対して繰り返す。
// 拡張機能の操作は、記録した Ref と引数で同じ拡張機能へ問い合わせ直す（FR-12）。
//
// req: FR-06, FR-12
func (s *State) repeat(cmd editcmd.Command) error {
	if s.dot == nil {
		return failf("no previous change")
	}
	d := *s.dot
	if cmd.CountGiven {
		d.Count, d.CountGiven = cmd.Count, true
	}
	if d.Kind == editcmd.KindAction && d.Action.Builtin() && (d.Action.ID == "put-after" || d.Action.ID == "put-before") &&
		d.Register >= '1' && d.Register <= '8' {
		d.Register++
	}
	s.replaying = true
	defer func() { s.replaying = false }()
	if d.Kind == editcmd.KindOperator && d.Target.Kind == editcmd.TargetNone {
		rg := s.regionFromExtent(s.dotVisual)
		return s.change(d, func() error { return s.regionOperator(d, rg) })
	}
	return s.execute(d)
}
