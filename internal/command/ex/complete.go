package ex

import (
	"cmp"
	"slices"
	"strings"

	"github.com/piplupflipper/vi-imitation/internal/option"
)

// コマンドラインの補完（FR-195。基本設計 7.4）のうち、コマンドラインを読んで、どこから何を補うかを決める部分。
//
// 補う語は、コマンドラインの行末までの最後の語である（本体のコマンドラインのカーソルは常に行末にある）。
// 読み方は Execute と同じ表（commands、別名の表、State.ExtCommand）を使い、"|" で続けたコマンドは最後のコマンドを読む。
// 候補を集めるのは、このパッケージが表を持つもの（設定の名前と値）だけで、コマンドの名前（拡張機能のコマンドを含む）、
// ファイル、バッファ、拡張機能のコマンドの口から呼ぶ本体のコマンドの引数は、一覧を持つ呼び出し側（internal/editor）が集める。

// CompletionKind は、コマンドラインの補完で補う語の種類である（Completion.Kind）。
type CompletionKind int

// 補う語の種類。
const (
	// CompleteNothing は、補うものが無いことを表す。呼び出し側は <Tab> を文字として入れる
	// （Vim 9.1 と Neovim 0.12.5 の EXPAND_NOTHING。:q <Tab> や :s/a/b<Tab> は、タブ文字が入る）。
	CompleteNothing CompletionKind = iota
	// CompleteCommand は、コマンドの名前を補うことを表す。範囲の後の名前の途中（:e、:%s、:finder.f、:w|e）である。
	CompleteCommand
	// CompleteFile は、ファイルとディレクトリのパスを補うことを表す（:e、:w、:wq、:x、:tab.new、:diffsplit と、
	// それらへ付け替えた別名）。語は "\ " と "\|" を含めたままの文字列で、UnescapeFileArg で戻してから探す。
	CompleteFile
	// CompleteBuffer は、バッファの名前を補うことを表す（:b、:bd）。語は引数の全体で、UnescapeFileArg で戻してから探す。
	CompleteBuffer
	// CompleteList は、Completion.Candidates に入れた候補で補うことを表す（設定の名前と値）。
	CompleteList
	// CompleteExtCommand は、拡張機能のコマンドの口から呼ぶコマンド（拡張機能のコマンドと、本体の :help、:ext など）の引数を
	// 補うことを表す。Completion.Command に、別名を付け替えた後のコマンドの ID を入れる。何を補うかは呼び出し側が決める。
	CompleteExtCommand
)

// Completion は、コマンドラインのどこから何を補うかである（Complete の結果）。
type Completion struct {
	Kind CompletionKind
	// Start は、補う語のコマンドラインの中のバイト位置である。語は Start から行末までで、候補はこの語を置き換える。
	Start int
	// Command は、CompleteExtCommand のコマンドの ID である。
	Command string
	// Candidates は、CompleteList の候補である。コマンドラインに入れる形（:set の値は "\" を付けた形）で、出す順に並ぶ。
	Candidates []string
}

// argKind は、Ex コマンドの表の行の、引数の位置に何を補うかである（command.complete）。
type argKind int

const (
	argNone       argKind = iota // 補わない
	argFile                      // ファイルとディレクトリのパス（最後の空白の後の語）
	argBuffer                    // バッファの名前（引数の全体）
	argOptionName                // 設定の正規の名前（:option.get）
	argOptionBool                // 真偽の設定の正規の名前（:option.toggle）
	argOptionSet                 // 設定の正規の名前と、その後の値（:option.set）
	argSet                       // :set の引数（Vim の名前、no を付けた名前、= の後の値）
)

// Complete は、コマンドラインの文字列 line（先頭の ":" を除く）の行末で <Tab> を打ったときに、どこから何を補うかを返す（FR-195）。
// 読み方は Vim 9.1 と Neovim 0.12.5 の set_one_cmd_context に倣い、Execute と同じ表で読む。範囲は評価せずに読み飛ばす。
//
//   - 範囲の後のコマンドの名前の途中なら、コマンドの名前を補う（:e、:3d、:'<,'>s）。名前の前の空白とコロンは読み飛ばす
//   - "|" で区切ったコマンド（:w|e a）は、"|" で引数を終えるコマンドの後の、最後のコマンドを読む。"|" を引数に含めるコマンド
//     （:g、:s、:option.set、:terminal.popup、拡張機能のコマンド）の後は、そのコマンドの引数として読む
//   - :!、:s、:g などの補う引数を持たないコマンドの引数、知らないコマンド、:" の注釈の後は、補うものが無い
//
// 設定の名前と値（:set、:option.set、:option.get、:option.toggle）は、st.Options の今の値を使って候補まで決める（completeArg）。
//
// req: FR-195
func Complete(st *State, line string) Completion {
	if st == nil {
		st = &State{}
	}
	s := &scanner{src: line}
	for {
		s.skipBlanksAndColons()
		skipRange(s)
		start := s.i
		if s.eof() {
			return Completion{Kind: CompleteCommand, Start: start}
		}
		if s.peek() == '|' {
			// 範囲だけのコマンドの後に続けたコマンド（:3|d）。
			s.next()
			continue
		}
		if s.peek() == '!' || s.peek() == '"' {
			return Completion{}
		}
		id := extCommandName(s.rest())
		if start+len(id) == len(line) && !strings.ContainsFunc(id, notNameRune) {
			return Completion{Kind: CompleteCommand, Start: start}
		}
		if c, ok := st.extCommandCompletion(s, id); ok {
			return c
		}
		cmd := st.Aliases.resolve(readName(s))
		if cmd == nil {
			return Completion{}
		}
		if s.peek() == '!' {
			if !cmd.bang {
				return Completion{}
			}
			s.next()
		}
		if !s.eof() && s.peek() != ' ' && s.peek() != '\t' && s.peek() != '|' {
			// 名前の直後に引数が続く形（:e3）は補わない。
			return Completion{}
		}
		s.skipBlanks()
		argStart := s.i
		if cmd.bar == barSplit {
			if _, n, more := splitBar(s.rest()); more {
				s.i = argStart + n
				continue
			}
		}
		return st.completeArg(cmd.complete, line, argStart)
	}
}

// notNameRune は、r がコマンドの名前を打っている途中とみなす文字（英数字、"."、"-"、"_"）でないかを返す。
// 拡張機能のコマンドの ID は空白、"|"、"!" の手前までを名前として読むが（extCommandName）、補うときは :s/a/b のような
// 名前の後に引数が続く形を名前の途中とみなさないため、名前に使われる文字に限る。
func notNameRune(r rune) bool {
	switch {
	case 'a' <= r && r <= 'z', 'A' <= r && r <= 'Z', '0' <= r && r <= '9', r == '.', r == '-', r == '_':
		return false
	}
	return true
}

// extCommandCompletion は、s の位置の名前 id が拡張機能のコマンドの口から呼ぶコマンドなら、その引数を補う Completion と true を返す。
// 名前の規則は runExtCommand と同じである（本体のコマンドの名前を先に引き、拡張機能のコマンドへ付け替える別名を付け替える）。
func (st *State) extCommandCompletion(s *scanner, id string) (Completion, bool) {
	if st.ExtCommand == nil || id == "" || st.Aliases.resolve(id) != nil {
		return Completion{}, false
	}
	cmdID := id
	if a := st.Aliases.lookup(id); a != nil && a.target != "" {
		cmdID = a.target
	}
	if _, ok := st.ExtCommand(cmdID); !ok {
		return Completion{}, false
	}
	s.i += len(id)
	if s.peek() == '!' {
		return Completion{}, true
	}
	s.skipBlanks()
	return Completion{Kind: CompleteExtCommand, Start: s.i, Command: cmdID}, true
}

// completeArg は、line の argStart から行末までのコマンドの引数で、何を補うかを返す。
func (st *State) completeArg(kind argKind, line string, argStart int) Completion {
	arg := line[argStart:]
	switch kind {
	case argFile:
		if strings.HasPrefix(arg, "!") || strings.HasPrefix(arg, ">>") {
			// :w !cmd と :w >> file は使えない（fileArg が断る）。
			return Completion{}
		}
		return Completion{Kind: CompleteFile, Start: argStart + lastWordStart(arg)}
	case argBuffer:
		return Completion{Kind: CompleteBuffer, Start: argStart}
	case argOptionName, argOptionBool:
		if strings.ContainsAny(arg, " \t") {
			return Completion{}
		}
		return Completion{Kind: CompleteList, Start: argStart, Candidates: optionNames(arg, kind == argOptionBool)}
	case argOptionSet:
		i := strings.IndexAny(arg, " \t")
		if i < 0 {
			return Completion{Kind: CompleteList, Start: argStart, Candidates: optionNames(arg, false)}
		}
		d, ok := optionLookup(arg[:i])
		if !ok {
			return Completion{}
		}
		s := &scanner{src: arg, i: i}
		s.skipBlanks()
		return st.completeValue(d, line, argStart+s.i, false)
	case argSet:
		return st.completeSet(line, argStart+lastWordStart(arg))
	case argNone:
	}
	return Completion{}
}

// lastWordStart は、引数 arg の、"\" の付かない最後の空白の後のバイト位置を返す（Vim 9.1 の set_one_cmd_context が
// ファイルの引数の始まりを探すのと同じ読み方。"\" は次の 1 文字を飛ばす）。
func lastWordStart(arg string) int {
	start := 0
	for i := 0; i < len(arg); i++ {
		switch arg[i] {
		case ' ', '\t':
			start = i + 1
		case '\\':
			if i+1 < len(arg) {
				i++
			}
		}
	}
	return start
}

// completeSet は、:set の引数の 1 つ（wordStart から行末まで）で何を補うかを返す（Neovim 0.12.5 の :set の補完に倣う）。
//
//   - "=" を含めば、"=" の前の名前の項目の値を補う（completeValue）
//   - "no" で始まれば、"no" の後に真偽の項目の名前を補う（:set no<Tab> は真偽の項目の一覧）
//   - "?"、"!"、"&" を含めば、補うものが無い
//   - それ以外は、項目の名前（Vim の名前）を補う
func (st *State) completeSet(line string, wordStart int) Completion {
	word := line[wordStart:]
	if name, _, ok := strings.Cut(word, "="); ok {
		d, ok := vimOption(name)
		if !ok {
			return Completion{}
		}
		return st.completeValue(d, line, wordStart+len(name)+1, true)
	}
	if strings.ContainsAny(word, "?!&") {
		return Completion{}
	}
	if rest, ok := strings.CutPrefix(word, "no"); ok {
		return Completion{Kind: CompleteList, Start: wordStart + 2, Candidates: vimOptionCandidates(rest, true)}
	}
	return Completion{Kind: CompleteList, Start: wordStart, Candidates: vimOptionCandidates(word, false)}
}

// completeValue は、項目 d の値（valStart から行末まで）で何を補うかを返す。forSet は :set の値（"\" を付けて入れる）かである。
//
//   - 真偽の項目: :option.set では true と false を補う。:set の "=" の後は補わない（Vim でも誤りになる）
//   - 語の列の項目（option.Def.Words）: 最後のコンマかコロンの後の語を、語の候補で補う。値が空なら、今の値を最初の候補にする
//     （Neovim 0.12.5 の :set wildmode=<Tab> と同じ）
//   - それ以外: 値が空なら、今の値を補う（Vim 9.1 と Neovim 0.12.5 の :set ts=<Tab> が 8 を入れるのと同じ）
func (st *State) completeValue(d option.Def, line string, valStart int, forSet bool) Completion {
	text := line[valStart:]
	cur := ""
	if st.Options != nil {
		cur = st.Options.Get(d).String()
	}
	escape := func(s string) string { return s }
	if forSet {
		escape = escapeSetValue
	}
	c := Completion{Kind: CompleteList, Start: valStart}
	switch {
	case d.Type == option.Bool:
		if forSet {
			return Completion{}
		}
		c.Candidates = withPrefix([]string{"true", "false"}, text)
	case d.Words != nil:
		seg := strings.LastIndexAny(text, ",:") + 1
		c.Start += seg
		c.Candidates = withPrefix(d.Words.Words, text[seg:])
		if text == "" && cur != "" && !slices.Contains(c.Candidates, cur) {
			c.Candidates = append([]string{escape(cur)}, c.Candidates...)
		}
	case text == "" && cur != "":
		c.Candidates = []string{escape(cur)}
	}
	return c
}

// withPrefix は、words のうち prefix で始まるものを、元の順に返す。
func withPrefix(words []string, prefix string) []string {
	var out []string
	for _, w := range words {
		if strings.HasPrefix(w, prefix) {
			out = append(out, w)
		}
	}
	return out
}

// optionNames は、設定の表の正規の名前のうち prefix で始まるものを、名前の順に返す。boolOnly なら真偽の項目だけにする。
func optionNames(prefix string, boolOnly bool) []string {
	var out []string
	for _, def := range option.Defs() {
		if d, ok := optionLookup(def.Name); ok && (!boolOnly || d.Type == option.Bool) && strings.HasPrefix(d.Name, prefix) {
			out = append(out, d.Name)
		}
	}
	slices.Sort(out)
	return out
}

// vimOptionCandidates は、:set の Vim の名前（vimOptionNames）を補う候補を、名前の順に返す。boolOnly なら真偽の項目だけにする。
// 候補は項目ごとに長い名前 1 つで、長い名前か短い名前（ts、sw など）が prefix で始まる項目を出す
// （Neovim 0.12.5 の :set ts<Tab> が tabstop を出すのと同じ）。
func vimOptionCandidates(prefix string, boolOnly bool) []string {
	long := map[string]string{} // 正規の名前から長い Vim の名前へ
	match := map[string]bool{}
	for name, canon := range vimOptionNames {
		d, ok := optionLookup(canon)
		if !ok || (boolOnly && d.Type != option.Bool) {
			continue
		}
		if l, ok := long[canon]; !ok || len(name) > len(l) || (len(name) == len(l) && name < l) {
			long[canon] = name
		}
		if strings.HasPrefix(name, prefix) {
			match[canon] = true
		}
	}
	var out []string
	for canon := range match {
		out = append(out, long[canon])
	}
	slices.SortFunc(out, cmp.Compare)
	return out
}

// escapeSetValue は、:set の値 v を、unescapeSetValue で v に戻る形にする（"\" と空白の前に "\" を付け、"|" を "\|" にする）。
func escapeSetValue(v string) string {
	var sb strings.Builder
	for _, r := range v {
		if r == '\\' || r == ' ' || r == '\t' || r == '|' {
			sb.WriteByte('\\')
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

// EscapeFileArg は、ファイルとバッファの名前 name を、コマンドラインの引数に書く形にする（FR-195）。
// 空白の前と "|" の前に "\" を付ける。fileArg と splitBar が戻す形（"\ " と "\|"）である。
func EscapeFileArg(name string) string {
	if !strings.ContainsAny(name, " |") {
		return name
	}
	var sb strings.Builder
	for _, r := range name {
		if r == ' ' || r == '|' {
			sb.WriteByte('\\')
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

// UnescapeFileArg は、コマンドラインの引数に書いたファイルとバッファの名前 arg を、EscapeFileArg の前の形に戻す（"\ " と "\|"）。
func UnescapeFileArg(arg string) string {
	return strings.NewReplacer(`\ `, " ", `\|`, "|").Replace(arg)
}
