package command

import (
	"strings"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
	"github.com/piplupflipper/vi-imitation/internal/option"
)

// 字下げ（FR-160〜FR-162。基本設計 3 章のインサートモードと、オペレーターの > と <）。
//
// 字下げの設定は、バッファごとの設定の項目 indent.useSpaces、indent.width、tab.width、indent.auto で、使うたびに今の値を読む（indentConfig）。
// Vim の 'softtabstop' と 'smarttab' は別の設定にせず、Neovim 0.12.5 の次の組と同じに動く（#318 の決め。'smarttab' は Neovim の既定でオン）。
//
//   - indent.useSpaces が true: expandtab shiftwidth=N softtabstop=N smarttab
//   - indent.useSpaces が false: noexpandtab shiftwidth=N softtabstop=0 smarttab
//
// N は indent.width で、0 なら tab.width（Vim の shiftwidth=0）、'tabstop' は tab.width である。
// <Tab> と <BS> の動きは、Neovim 0.12.5 の src/nvim/edit.c の ins_tab と ins_bs の、この組で通る道を写した。
// 期待値は 2026-11-21 に手元の Neovim 0.12.5（nvim --clean）で取った（indent_test.go）。

// indentConfig は、字下げの設定の今の値である。
type indentConfig struct {
	// useSpaces は indent.useSpaces である。
	useSpaces bool
	// width は字下げの 1 段の幅である。indent.width が 0 なら tab.width にしてある（Vim の shiftwidth()）。
	width int
	// tabWidth は tab.width である。
	tabWidth int
	// auto は indent.auto である。
	auto bool
}

// indentConfig は、バッファの字下げの設定の今の値を、Ex.Options（internal/editor が差し込む設定の口）から読む。
// 口が無ければ（internal/command のテスト）、表の既定値を使う。
func (s *State) indentConfig() indentConfig {
	get := func(name string) option.Value {
		d, _ := option.Lookup(name)
		if s.Ex.Options == nil {
			return d.Default
		}
		return s.Ex.Options.Get(d)
	}
	c := indentConfig{
		useSpaces: get(option.IndentUseSpaces).Bool(),
		width:     get(option.IndentWidth).Int(),
		tabWidth:  max(get(option.TabWidth).Int(), 1),
		auto:      get(option.IndentAuto).Bool(),
	}
	if c.width <= 0 {
		c.width = c.tabWidth
	}
	return c
}

// textWidth は、文字列 str を列 0 から並べたときの表示幅を、タブ文字の幅 tabWidth で数える（State.TextWidth）。
func (s *State) textWidth(str string, tabWidth int) int {
	if s.TextWidth != nil {
		return s.TextWidth(str, tabWidth)
	}
	w := 0
	for _, c := range str {
		if c == '\t' {
			w += tabWidth - w%tabWidth
		} else {
			w++
		}
	}
	return w
}

// isBlank は、c が空白（スペースかタブ文字）かを返す。
func isBlank(c byte) bool { return c == ' ' || c == '\t' }

// leadingBlanks は、str の先頭の空白のバイト数を返す。
func leadingBlanks(str string) int {
	return len(str) - len(strings.TrimLeft(str, " \t"))
}

// makeIndent は、表示幅 w の字下げを作る。indent.useSpaces ならスペースだけで、そうでなければタブ文字を使えるだけ使い、残りをスペースで埋める
// （Vim の 'copyindent' がオフの set_indent と同じ）。
func (c indentConfig) makeIndent(w int) string {
	w = max(w, 0)
	if c.useSpaces {
		return strings.Repeat(" ", w)
	}
	return strings.Repeat("\t", w/c.tabWidth) + strings.Repeat(" ", w%c.tabWidth)
}

// tabEdit は、インサートモードの <Tab>（editcmd.InsertTab）を位置 p で行う編集と、行った後の位置を返す（FR-160）。
//
//   - indent.useSpaces が false で、カーソルの前が字下げ（空白だけ）でないか、字下げの幅とタブ文字の幅が同じなら、タブ文字を 1 つ入れる
//   - それ以外は、次の字下げの幅の倍数の列までスペースを入れる（'softtabstop' と 'smarttab' の動き）
//   - indent.useSpaces が false なら、入れた後に、カーソルの前に続く空白を、タブ文字を使えるだけ使う形に直す（Neovim の ins_tab の
//     「Replace spaces by TABs where possible」。空白の途中にタブ文字があるときの結果も、Neovim と同じ手順で作る）
//
// 編集は行の中に収まる（同時の入力（FR-141）を終えない）。
//
// req: FR-160
func (s *State) tabEdit(p buffer.Position) (buffer.Edit, buffer.Position) {
	c := s.indentConfig()
	line := s.line(p.Line)
	prefix := line[:p.Byte]
	inIndent := leadingBlanks(prefix) == len(prefix)
	if !c.useSpaces && (!inIndent || c.width == c.tabWidth) {
		return buffer.Edit{Range: emptyRange(p), Text: "\t"}, pos(p.Line, p.Byte+1)
	}
	vcol := s.textWidth(prefix, c.tabWidth)
	n := c.width - vcol%c.width
	text := []byte(prefix + strings.Repeat(" ", n))
	want := vcol + n
	start := len(prefix)
	if !c.useSpaces {
		for start > 0 && isBlank(text[start-1]) {
			start--
		}
		v := s.textWidth(string(text[:start]), c.tabWidth)
		i, changed := start, false
		for i < len(text) && isBlank(text[i]) {
			tw := c.tabWidth - v%c.tabWidth
			if v+tw > want {
				break
			}
			if text[i] != '\t' {
				text[i] = '\t'
				changed = true
			}
			i++
			v += tw
		}
		if changed {
			// 要るスペースを残し、カーソルまでの残りを消す。
			for v < want && i < len(text) && text[i] == ' ' {
				v++
				i++
			}
			text = text[:i]
		}
	}
	run := string(text[start:])
	return buffer.Edit{Range: buffer.Range{Start: pos(p.Line, start), End: p}, Text: run}, pos(p.Line, start+len(run))
}

// softBackspace は、位置 p での <BS>（editcmd.InsertDeletePrev）を、字下げの幅の単位で消すかを返す（FR-160）。
// 位置の前が字下げ（空白だけ）なら消す（'smarttab'）。indent.useSpaces が true なら、位置の前の文字がタブ文字か、スペースで、このインサートで
// スペースを打っていない（typedSpace。Vim の inserted_space）ときにも消す（'softtabstop'）。行の先頭では消さない（前の行とつなぐ）。
func (s *State) softBackspace(p buffer.Position) bool {
	if p.Byte == 0 {
		return false
	}
	prefix := s.line(p.Line)[:p.Byte]
	if leadingBlanks(prefix) == len(prefix) {
		return true
	}
	prev := prefix[len(prefix)-1]
	return s.indentConfig().useSpaces && (prev == '\t' || (prev == ' ' && !s.insert.typedSpace))
}

// softBackspaceEdit は、softBackspace が true の位置 p で、<BS> が行う編集と、行った後の位置を返す（FR-160）。
// 位置の前の文字の最後の列より左の、字下げの幅の倍数の列（want）まで、位置の前の空白を消す。消しすぎた（タブ文字を消した）なら、
// want までスペースで埋める（Neovim の ins_bs の「Handle deleting one 'shiftwidth' or 'softtabstop'」）。
//
// req: FR-160
func (s *State) softBackspaceEdit(p buffer.Position) (buffer.Edit, buffer.Position) {
	c := s.indentConfig()
	prefix := s.line(p.Line)[:p.Byte]
	vcol := s.textWidth(prefix, c.tabWidth)
	want := (vcol - 1) / c.width * c.width
	j, v := len(prefix), vcol
	for v > want && j > 0 && isBlank(prefix[j-1]) {
		j--
		v = s.textWidth(prefix[:j], c.tabWidth)
	}
	pad := strings.Repeat(" ", max(want-v, 0))
	return buffer.Edit{Range: buffer.Range{Start: pos(p.Line, j), End: p}, Text: pad}, pos(p.Line, j+len(pad))
}

// lineBreakEdit は、インサートモードの <CR>（editcmd.InsertLineBreak）を位置 p で行う編集と、行った後の位置を返す（FR-162）。
//
// indent.auto が false なら、行を分けるだけである。true なら、Neovim 0.12.5 の open_line と同じく次のとおりにする。
//
//   - 新しい行の字下げは、位置より前の部分の先頭の空白の幅にする（位置が字下げの途中なら、位置までの幅）。作り方は indent.useSpaces による（makeIndent）
//   - 位置より後ろの部分の先頭の空白は消す
//   - 位置の行が自動インデントで入れた字下げのままなら（insertState.autoIndent）、位置より前の部分の末尾の空白も消す
//
// req: FR-162
func (s *State) lineBreakEdit(p buffer.Position) (buffer.Edit, buffer.Position) {
	c := s.indentConfig()
	if !c.auto {
		return buffer.Edit{Range: emptyRange(p), Text: "\n"}, pos(p.Line+1, 0)
	}
	line := s.line(p.Line)
	left, right := line[:p.Byte], line[p.Byte:]
	indent := c.makeIndent(s.textWidth(left[:leadingBlanks(left)], c.tabWidth))
	start := p.Byte
	if s.insert.autoIndent {
		start = len(strings.TrimRight(left, " \t"))
	}
	end := p.Byte + leadingBlanks(right)
	return buffer.Edit{Range: buffer.Range{Start: pos(p.Line, start), End: pos(p.Line, end)}, Text: "\n" + indent},
		pos(p.Line+1, len(indent))
}

// lineIndent は、行 l の字下げの表示幅を返す。
func (s *State) lineIndent(l int) int {
	line := s.line(l)
	return s.textWidth(line[:leadingBlanks(line)], s.indentConfig().tabWidth)
}

// dropAutoIndent は、入力の位置 p の行が自動インデントで入れた字下げのまま（insertState.autoIndent）で入力を終えるか、別の行へ移るときに、
// p から空白を消す（Neovim 0.12.5 の stop_insert の「If we just did an auto-indent, remove the white space from the end of the line」）。
// Neovim と同じく、p から後ろへ続く空白を消し、行末まで空白なら p より前の空白も消す。p の後ろに空白でない文字があれば
// （<CR> で行を分けた後ろの部分など）、何も消さない。カーソルがその行の消した範囲より後ろにあれば、消した範囲の始まりへ寄せる。
// autoIndent は呼び出し側が戻す。
//
// req: FR-162
func (s *State) dropAutoIndent(p buffer.Position) error {
	line := s.line(p.Line)
	c := min(p.Byte, len(line))
	end := c
	for end < len(line) && isBlank(line[end]) {
		end++
	}
	start := c
	if end == len(line) {
		start = len(strings.TrimRight(line[:c], " \t"))
	}
	if start == end {
		return nil
	}
	if err := s.apply(buffer.Edit{Range: buffer.Range{Start: pos(p.Line, start), End: pos(p.Line, end)}}); err != nil {
		return err
	}
	if s.cursor.Line == p.Line && s.cursor.Byte > start {
		s.cursor.Byte = start
	}
	return nil
}
