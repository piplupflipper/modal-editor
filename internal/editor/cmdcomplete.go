package editor

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/piplupflipper/vi-imitation/internal/command"
	"github.com/piplupflipper/vi-imitation/internal/command/ex"
	"github.com/piplupflipper/vi-imitation/internal/editcmd"
	"github.com/piplupflipper/vi-imitation/internal/option"
	"github.com/piplupflipper/vi-imitation/internal/ui"
)

// コマンドラインの補完（基本設計 7.4。FR-195〜FR-200）。
//
// Ex コマンドのコマンドライン（":"）で利用者が打った <Tab> と <S-Tab> で、行末の語を候補で補い、候補をコマンドラインの真上の
// ポップアップの一覧（ui.Frame.Completion）に出す。どこから何を補うかは internal/command/ex の Complete が決め、
// 候補はここで集める（コマンドの名前、ファイル、バッファ、:help の語、:ext の引数）。補い方は Neovim 0.12.5 の
// command_line_wildchar_complete と nextwild（src/nvim/ex_getln.c、src/nvim/cmdexpand.c）に倣い、設定の
// commandLine.completeMode（Vim の 'wildmode'）の部分を <Tab> を打った回数で選ぶ。
//
// 補ったテキストは、<BS> と文字のキーを State に渡して、コマンドラインの語を置き換える（wildSetWord）。
// このため、マクロを記録している間に補ったときは、補った後の文字が記録に入り、再生では同じコマンドラインになる
// （Vim は <Tab> そのものを記録し、再生の <Tab> は補わずにタブ文字を入れる。docs/vim-differences.md）。
// マクロとドットリピートの再生のキーは State の中で解釈するので、ここを通らず、<Tab> はこれまでどおりタブ文字として入る（Vim と同じ）。
// キーマップの展開で入った <Tab> は補う（Vim は 'wildcharm' に書いたキーでだけ補う）。
//
// 補う状態は client ごとに持つ（viewFields.wild）。キーを処理するたびと描くたびに、状態を作ったときのバッファとコマンドラインが
// 今のものと同じかを確かめ、違えば（実行した、<Esc> で抜けた、別のバッファへ移った）捨てる（currentWild）。
//
// ファイルの候補は、大きなディレクトリでも入力を止めないよう、イベントループの外の goroutine で集める（startWildFiles）。
// 集めている間に打った <Tab> は捨て、それ以外のキーは集めるのを取り消してから、ふつうに処理する。
// 集め終えたときにコマンドラインが変わっていなければ、その時点で <Tab> を処理したのと同じに補う（wildFilesDone）。

// wildMenu は、1 つの client の、コマンドラインの補完の状態である（viewFields.wild）。Neovim の expand_T と
// CommandLineState の wim_index、did_wild_list に当たる。
type wildMenu struct {
	// bs は、補っているコマンドラインを持つバッファである。
	bs *bufferState
	// line は、最後に補った後のコマンドラインである。今のコマンドラインと違えば、外で変わったので捨てる。
	line string
	// start は、補う語のコマンドラインの中のバイト位置である。語は start から行末までである。
	start int
	// orig は、補い始めたときの語である（Neovim の xp_orig）。候補を一回りすると、この語に戻る。
	orig string
	// items は、候補をコマンドラインに入れる形で並べたものである。shown は、一覧に出す形（ファイルは最後の "/" の後）である。
	items, shown []string
	// selected は、コマンドラインに入れている候補の添字である。-1 なら orig を入れている（Neovim の xp_selected）。
	selected int
	// index は、次の <Tab> で使う commandLine.completeMode の部分の番号である（Neovim の wim_index）。
	index int
	// menu は、候補の一覧を出しているかである。id は出している一覧の番号、menuStart は一覧の項目を並べ始める位置である。
	menu      bool
	id        uint64
	menuStart int
	// tail は、一覧に、ファイルのパスの最後の "/" の後だけを出すかである（Neovim の cmd_showtail）。
	tail bool
	// job は、ファイルの候補を集めている途中の仕事である。nil なら集め終えている。
	job *wildJob
}

// wildJob は、イベントループの外で集めているファイルの候補である（startWildFiles）。
type wildJob struct {
	cancel context.CancelFunc
	// reverse は、<S-Tab> で始めたかである。
	reverse bool
	// queue は、集めている間に打った補完のキー（wildQueued）である。集め終えて補った後に、打った順に処理する。
	queue []editcmd.Key
}

// wildQueued は、k が、ファイルの候補を集めている間に打ったら取っておき、集め終えてから処理する補完のキーかを返す。
// 続けて打った <Tab><Tab><C-y> が、1 回目の <Tab> の候補を集め終える前に届いても、集め終えてから順に効くようにする。
// これ以外のキーは、集めるのを取り消してから、その場で処理する（入力を止めない。FR-199）。
func wildQueued(k editcmd.Key) bool {
	switch k {
	case wildTabKey, wildShiftTabKey, wildNextKey, wildPrevKey, wildCancelKey, wildAcceptKey,
		editcmd.Special(editcmd.CodeUp), editcmd.Special(editcmd.CodeDown), editcmd.Special(editcmd.CodeLeft),
		editcmd.Special(editcmd.CodeRight), editcmd.Special(editcmd.CodePageUp), editcmd.Special(editcmd.CodePageDown):
		return true
	}
	return false
}

// wimFlags は、commandLine.completeMode の 1 つの部分である（Neovim の wim_flags の 1 つ）。
type wimFlags struct {
	full, longest, noselect bool
}

// wildFlags は、設定の commandLine.completeMode の今の値を、4 つの部分にして返す。書いた部分より後ろは、最後に書いた部分と同じにする
// （Vim 9.1 と Neovim 0.12.5 の check_opt_wim）。値は設定の検査（option.Def.Words）を通っているので、読めなければ full にする。
//
// req: FR-196
func (e *Editor) wildFlags() [4]wimFlags {
	var out [4]wimFlags
	parts, err := option.CompleteModeWords.Parse(e.options.Get(option.CommandLineCompleteMode).String())
	if err != nil {
		parts = [][]string{{"full"}}
	}
	for i := range out {
		if i >= len(parts) {
			if i > 0 {
				out[i] = out[i-1]
			}
			continue
		}
		for _, w := range parts[i] {
			switch w {
			case "full":
				out[i].full = true
			case "longest":
				out[i].longest = true
			case "noselect":
				out[i].noselect = true
			}
		}
	}
	return out
}

// 補完で読むキー。
var (
	wildTabKey      = editcmd.Special(editcmd.CodeTab)
	wildShiftTabKey = editcmd.Key{Code: editcmd.CodeTab, Mod: editcmd.ModShift}
	wildNextKey     = editcmd.Key{Code: editcmd.CodeRune, Rune: 'n', Mod: editcmd.ModCtrl}
	wildPrevKey     = editcmd.Key{Code: editcmd.CodeRune, Rune: 'p', Mod: editcmd.ModCtrl}
	wildCancelKey   = editcmd.Key{Code: editcmd.CodeRune, Rune: 'e', Mod: editcmd.ModCtrl}
	wildAcceptKey   = editcmd.Key{Code: editcmd.CodeRune, Rune: 'y', Mod: editcmd.ModCtrl}
)

// currentWild は、読み込んでいる client の補う状態を、今のコマンドラインのものであれば返す。
// 状態を作ったバッファがカーソルのあるバッファでない、Ex コマンドのコマンドラインモードでない、コマンドラインが最後に補った後と違う
// ときは、状態を捨てて nil を返す。
func (e *Editor) currentWild() *wildMenu {
	w := e.wild
	if w == nil {
		return nil
	}
	st := w.bs.state
	if w.bs != e.cur || st.Mode() != command.ModeCommandLine || st.CommandLineType() != ':' || st.CommandLine() != w.line {
		e.endWild()
		return nil
	}
	return w
}

// endWild は、読み込んでいる client の補う状態を捨てる。ファイルの候補を集めている途中なら取り消す。
func (e *Editor) endWild() {
	if w := e.wild; w != nil && w.job != nil {
		w.job.cancel()
		w.job = nil
	}
	e.wild = nil
}

// wildKey は、利用者のキー k がコマンドラインの補完のキーなら処理して true を返す（feedKey が State へ渡す前に呼ぶ）。
// true を返したキーは State へ渡さない。補完のキーでなければ、補う状態を終えて false を返す（キーはふつうに処理される）。
//
//   - <Tab>: 補う。補うものが無いところ（ex.CompleteNothing）では false を返し、タブ文字が入る（Neovim と同じ）
//   - <S-Tab>: 候補が無ければ、補い始めて最後の候補を入れる。あれば <C-p> と同じ
//   - <C-n>、<C-p>: 候補があれば、次と前の候補を入れる
//   - 一覧を出している間の <Down>、<Right> と <Up>、<Left>: 次と前の候補（<Down> と <Up> はオーナーの nvim の割り当て。
//     <Left> と <Right> は Neovim の wildmenu_translate_key）。<PageDown> と <PageUp>: 一覧の高さだけ先と前の候補
//   - 一覧を出している間の <C-e>: 補う前の語に戻して一覧を閉じる。<C-y>: 入れている候補のまま一覧を閉じる
//   - 一覧を出している間のマウスの出来事: 何もしない（コマンドラインはマウスを読まない。一覧も閉じない）
//
// req: FR-195, FR-198
func (e *Editor) wildKey(k editcmd.Key) bool {
	st := e.cur.state
	if st.Mode() != command.ModeCommandLine || st.CommandLineType() != ':' {
		if e.wild != nil {
			e.endWild()
		}
		return false
	}
	w := e.currentWild()
	if w != nil && w.job != nil {
		if wildQueued(k) {
			w.job.queue = append(w.job.queue, k)
			return true
		}
		e.endWild()
		return false
	}
	switch {
	case k == wildTabKey:
		return e.wildTab(w)
	case k == wildShiftTabKey && (w == nil || len(w.items) == 0):
		return e.wildStart(true)
	case w == nil || len(w.items) == 0:
	case k == wildNextKey || (w.menu && (k == editcmd.Special(editcmd.CodeDown) || k == editcmd.Special(editcmd.CodeRight))):
		e.wildStep(w, 1)
		return true
	case k == wildPrevKey || k == wildShiftTabKey || (w.menu && (k == editcmd.Special(editcmd.CodeUp) || k == editcmd.Special(editcmd.CodeLeft))):
		e.wildStep(w, -1)
		return true
	case w.menu && (k == editcmd.Special(editcmd.CodePageDown) || k == editcmd.Special(editcmd.CodePageUp)):
		e.wildPage(w, k.Code == editcmd.CodePageDown)
		return true
	case w.menu && k == wildCancelKey:
		e.wildSetWord(w, w.orig)
		e.endWild()
		return true
	case w.menu && k == wildAcceptKey:
		e.endWild()
		return true
	case w.menu && k.IsMouse():
		return true
	}
	if w != nil {
		e.endWild()
	}
	return false
}

// wildTab は、<Tab> を処理する。候補を集めていれば（2 回目からの <Tab>）、今の部分の補い方で補う。
// 集めていなければ補い始める（wildStart）。Neovim の command_line_wildchar_complete の、xp_numfiles > 0 の側である。
//
// req: FR-195, FR-196
func (e *Editor) wildTab(w *wildMenu) bool {
	if w == nil || len(w.items) == 0 {
		return e.wildStart(false)
	}
	f := e.wildFlags()[w.index]
	switch {
	case f.longest:
		e.wildLongest(w)
	case f.full:
		e.wildStep(w, 1)
	}
	if w.index < 3 {
		w.index++
	}
	return true
}

// wildStart は、行末の語を補い始める。reverse は <S-Tab> で始めたかである。補うものが無ければ false を返す。
// ファイルの候補はイベントループの外で集め、集め終えてから補う（startWildFiles）。それ以外はその場で集めて補う。
func (e *Editor) wildStart(reverse bool) bool {
	bs := e.cur
	line := bs.state.CommandLine()
	c := ex.Complete(&bs.state.Ex, line)
	w := &wildMenu{bs: bs, line: line, start: c.Start, orig: line[c.Start:], selected: -1}
	switch c.Kind {
	case ex.CompleteNothing:
		return false
	case ex.CompleteFile:
		e.endWild()
		e.wild = w
		e.startWildFiles(w, reverse)
		return true
	case ex.CompleteCommand:
		w.items = withPrefix(e.completionCommandNames(), w.orig)
	case ex.CompleteList:
		w.items = c.Candidates
	case ex.CompleteBuffer:
		w.items = e.completionBuffers(ex.UnescapeFileArg(w.orig))
	case ex.CompleteExtCommand:
		items, ok := e.completionExtArgs(c.Command, w.orig)
		if !ok {
			return false
		}
		w.items = items
	}
	w.shown = w.items
	e.endWild()
	e.wild = w
	e.wildBegin(w, reverse)
	return true
}

// wildBegin は、集めた候補で、補い始めの <Tab>（reverse なら <S-Tab>）を処理する。
// Neovim の command_line_wildchar_complete の、初めて <Tab> を打った側（xp_numfiles が -1 の側）と、<S-Tab> の側である。
//
//   - 部分に longest があれば、候補の共通の先頭を入れる。打った語より短くなるなら入れない（nextwild の WILD_LONGEST）。
//     共通の先頭は大文字と小文字を区別して数える（Neovim は 'fileignorecase' のときだけ区別しない）
//   - longest が無く noselect があれば、何も入れずに一覧を出す。どちらも無ければ最初の候補を入れる
//   - 候補が 1 つだけなら、入れて補う状態を終える（次の <Tab> はその後から補い始める。:e dir/ の後の <Tab> は dir の中を補う）
//   - 候補が 2 つ以上なら、部分の full か noselect で一覧を出す。longest で語が伸びなかったときは、次の部分の full で最初の候補を入れる
//
// req: FR-195, FR-196
func (e *Editor) wildBegin(w *wildMenu, reverse bool) {
	n := len(w.items)
	if n == 0 {
		// 候補が無い（Neovim は音を鳴らす）。打った語のまま、補う状態を終える。
		e.endWild()
		return
	}
	flags := e.wildFlags()
	if reverse {
		// <S-Tab>: 最初の候補を入れてから一覧を出し、2 回戻して最後の候補にする（Neovim の "<S-Tab> goes to last match"）。
		e.wildSetWord(w, w.items[0])
		w.selected = 0
		if n == 1 {
			e.endWild()
			return
		}
		e.wildShowMenu(w, flags[0].noselect)
		e.wildStep(w, -1)
		e.wildStep(w, -1)
		return
	}
	f0 := flags[0]
	before := len(w.bs.state.CommandLine())
	switch {
	case f0.longest:
		e.wildLongest(w)
	case f0.noselect:
	default:
		e.wildSetWord(w, w.items[0])
		w.selected = 0
	}
	if n == 1 && (f0.longest || !f0.noselect) {
		e.endWild()
		return
	}
	if n > 1 || f0.noselect {
		switch {
		case f0.longest && f0.full:
			e.wildShowMenu(w, true)
		case f0.longest && len(w.bs.state.CommandLine()) == before:
			if f1 := flags[1]; f1.full && !f1.noselect {
				e.wildStep(w, 1)
			} else if f1.full || f1.noselect {
				e.wildShowMenu(w, true)
			}
		case !f0.longest && (f0.full || f0.noselect):
			e.wildShowMenu(w, f0.noselect)
		}
	}
	if w.index < 3 {
		w.index++
	}
}

// wildLongest は、候補の共通の先頭を入れ、候補を選んでいない状態にする（Neovim の find_longest_match）。
// 共通の先頭が今の語より短ければ入れない（Neovim の nextwild の WILD_LONGEST）。
func (e *Editor) wildLongest(w *wildMenu) {
	p := w.items[0]
	for _, it := range w.items[1:] {
		n := 0
		for n < len(p) && n < len(it) {
			r1, s1 := utf8.DecodeRuneInString(p[n:])
			r2, _ := utf8.DecodeRuneInString(it[n:])
			if r1 != r2 {
				break
			}
			n += s1
		}
		p = p[:n]
	}
	w.selected = -1
	if len(p) >= len(w.bs.state.CommandLine())-w.start {
		e.wildSetWord(w, p)
	}
}

// wildStep は、delta が正なら次の、負なら前の候補を入れる。最後の候補の次と最初の候補の前は、打った語に戻る
// （Neovim の get_next_or_prev_match。打った語の前は最後の候補）。一覧を出していなければ出す。
//
// req: FR-198
func (e *Editor) wildStep(w *wildMenu, delta int) {
	n := len(w.items)
	i := w.selected
	if delta < 0 {
		if i == -1 {
			i = n
		}
		i--
	} else {
		i++
	}
	if i < 0 || i >= n {
		i = -1
	}
	e.wildSelect(w, i)
}

// wildPage は、一覧の高さ（2 行を残す）だけ先（down なら）か前の候補を入れる（Neovim の WILD_PAGEDOWN と WILD_PAGEUP）。
// 最後の候補で先へ、最初の候補で前へ送ると打った語に戻り、打った語からは最初か最後の候補へ移る。
//
// req: FR-198
func (e *Editor) wildPage(w *wildMenu, down bool) {
	n := len(w.items)
	ht := e.renderer.CompletionRows()
	if ht > 3 {
		ht -= 2
	}
	ht = max(ht, 1)
	i := w.selected
	switch {
	case down && i == n-1, !down && i == 0:
		i = -1
	case down && i < 0:
		i = 0
	case !down && i < 0:
		i = n - 1
	case down:
		i = min(i+ht, n-1)
	default:
		i = max(i-ht, 0)
	}
	e.wildSelect(w, i)
}

// wildSelect は、i 番目の候補（-1 なら打った語）をコマンドラインに入れる。一覧を出していなければ、入れる前に出す。
func (e *Editor) wildSelect(w *wildMenu, i int) {
	if !w.menu {
		e.wildShowMenu(w, false)
	}
	w.selected = i
	if i < 0 {
		e.wildSetWord(w, w.orig)
		return
	}
	e.wildSetWord(w, w.items[i])
}

// wildShowMenu は、候補の一覧を出す。一覧の項目は、ファイルの候補なら今のコマンドラインの語のパスの最後の部分（pathTail）から、
// それ以外は語の始まりから並べる（Neovim の cmdline_pum_create の compl_startcol）。noselect なら、何も選んでいない一覧にする。
func (e *Editor) wildShowMenu(w *wildMenu, noselect bool) {
	w.menu = true
	e.wildMenuID++
	w.id = e.wildMenuID
	w.menuStart = w.start
	if w.tail {
		w.menuStart += pathTail(w.bs.state.CommandLine()[w.start:], noselect)
	}
	if noselect {
		w.selected = -1
	}
}

// pathTail は、パス s の最後の部分の始まりのバイト位置を返す（Neovim の showmatches_gettail）。
// eager なら最後の "/" の後である。eager でなければ、後に "/" でない文字が続く最後の "/" の後で（"a/b/" は "b/" の始まり）、
// そのような "/" が無ければ 0 である。Neovim は、選んでいない一覧を出すときだけ eager で数える。
func pathTail(s string, eager bool) int {
	t, sep := 0, false
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '/' && eager:
			t = i + 1
		case s[i] == '/':
			sep = true
		case sep:
			t, sep = i, false
		}
	}
	return t
}

// wildSetWord は、コマンドラインの補う語（start から行末）を text に置き換え、置き換えた後のコマンドラインを覚える。
// 語と text の共通の先頭は残し、残りを <BS> で消して、text の残りを文字のキーで入れる（マクロの記録に入れるため。ファイルの doc）。
func (e *Editor) wildSetWord(w *wildMenu, text string) {
	st := w.bs.state
	word := []rune(st.CommandLine()[w.start:])
	want := []rune(text)
	n := 0
	for n < len(word) && n < len(want) && word[n] == want[n] {
		n++
	}
	feed := st.Feed
	if e.noRecord {
		feed = st.FeedUnrecorded
	}
	for range len(word) - n {
		feed(editcmd.Special(editcmd.CodeBackspace))
	}
	for _, r := range want[n:] {
		feed(editcmd.Rune(r))
	}
	w.line = st.CommandLine()
}

// wildMenuFrame は、読み込んでいる client の、バッファ bs のコマンドラインの候補の一覧の描画の入力を返す。
// 一覧を出していなければ nil を返す。一覧の高さの上限は、描くたびに設定の completionMenu.height の今の値を読む（FR-197）。
//
// req: FR-197
func (e *Editor) wildMenuFrame(bs *bufferState) *ui.CompletionMenu {
	w := e.wild
	if w == nil || !w.menu || w.bs != bs || w.bs.state.Mode() != command.ModeCommandLine || w.bs.state.CommandLine() != w.line {
		return nil
	}
	return &ui.CompletionMenu{
		ID: w.id, Items: w.shown, Selected: w.selected, Start: w.menuStart, Match: w.orig,
		MaxHeight: e.options.Get(option.CompletionMenuHeight).Int(),
	}
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

// completionCommandNames は、コマンドの名前の補完の候補（FR-195）を、名前の順に返す。本体の Ex コマンドと別名の省略しない名前
// （ex.Names）と、拡張機能のコマンドの口から呼ぶコマンドの名前（exCommands。拡張機能のコマンド、provides の操作の名前、:help、:keys、
// :ext、:messages）である。本体が同じコマンドのために足した短い名前（:h、:mes から :message まで）は出さない
// （Vim の補完も省略形を出さない）。
//
// req: FR-195
func (e *Editor) completionCommandNames() []string {
	seen := map[string]bool{}
	var out []string
	add := func(name string) {
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	for _, n := range ex.Names(e.aliases) {
		add(n.Full)
	}
	for name, id := range e.exCommands {
		switch {
		case id == helpExtension && name != keysCommandName && name != helpCommandNames[0]:
		case id == messagesExtension && name != messagesCommandNames[len(messagesCommandNames)-1]:
		default:
			add(name)
		}
	}
	slices.Sort(out)
	return out
}

// completionBuffers は、バッファの名前の補完の候補（FR-195）を、コマンドラインに書く形（ex.EscapeFileArg）で返す。
// 名前は :ls の名前（bufferName）で、ファイルの無いバッファとポップアップのバッファは出さない。
// 名前かファイルの名前が pat で始まるバッファを番号の順に並べ、その後に名前が pat を含むバッファを番号の順に並べる
// （:b の名前の引き方 bufferByName と同じ段の順）。
//
// req: FR-195
func (e *Editor) completionBuffers(pat string) []string {
	var head, rest []string
	for _, bs := range e.bufs {
		if bs.popup || bs.path == "" {
			continue
		}
		name := e.bufferName(bs)
		switch {
		case strings.HasPrefix(name, pat) || strings.HasPrefix(filepath.Base(bs.path), pat):
			head = append(head, ex.EscapeFileArg(name))
		case strings.Contains(name, pat):
			rest = append(rest, ex.EscapeFileArg(name))
		}
	}
	return append(head, rest...)
}

// helpCompletionLimit は、:help の語の候補の数の上限である（Neovim 0.12.5 の :help の補完の上限の 300 と同じ）。
const helpCompletionLimit = 300

// completionExtArgs は、拡張機能のコマンドの口から呼ぶコマンド id の引数の、語 word の候補を返す。
// 本体の :help と :h は :help で引ける語（listHelpWords。語で始まるものを名前の順に、その後に語を大文字と小文字を区別せずに含むものを
// 名前の順に、300 個まで）、:ext は sync と update である。ほかのコマンド（拡張機能のコマンドと provides の操作）は、
// 拡張機能に補わせる口が無いので補わず、ok を false で返す（<Tab> はタブ文字として入る）。
//
// req: FR-195
func (e *Editor) completionExtArgs(id, word string) (items []string, ok bool) {
	switch e.exCommands[id] {
	case helpExtension:
		if !slices.Contains(helpCommandNames, id) {
			return nil, false
		}
		var head, rest []string
		lw := strings.ToLower(word)
		for _, w := range e.helpWords() {
			switch {
			case strings.HasPrefix(w, word):
				head = append(head, w)
			case strings.Contains(strings.ToLower(w), lw):
				rest = append(rest, w)
			}
		}
		slices.Sort(head)
		slices.Sort(rest)
		items = append(head, rest...)
		return items[:min(len(items), helpCompletionLimit)], true
	case extManagerExtension:
		return withPrefix([]string{"sync", "update"}, word), true
	}
	return nil, false
}

// helpWords は、:help で引ける語の一覧（listHelpWords の語）を返す。語は起動した後に変わらないので、最初に求めたものを使い回す。
func (e *Editor) helpWords() []string {
	if e.helpWordCache == nil {
		ws := e.listHelpWords()
		e.helpWordCache = make([]string, len(ws))
		for i, w := range ws {
			e.helpWordCache[i] = w.Word
		}
	}
	return e.helpWordCache
}

// fileQuery は、ファイルとディレクトリの補完の候補を集める問いである（collectFiles）。
type fileQuery struct {
	// word は、コマンドラインの語（"\ " と "\|" を含めたままの形）である。
	word string
	// workDir は、相対パスの基点（client の作業ディレクトリ）である。home は "~" を展開するホームディレクトリである。
	workDir, home string
	// env は、"$名前" を展開する環境変数を引く。
	env func(name string) (string, bool)
	// ignoreCase は、名前の大文字と小文字を区別しないかである（commandLine.completeIgnoreCase）。
	ignoreCase bool
}

// startWildFiles は、ファイルの候補を、イベントループの外の goroutine で集め始める（FR-199）。
// 集め終えたら、inspect でイベントループへ戻して補う（wildFilesDone）。
func (e *Editor) startWildFiles(w *wildMenu, reverse bool) {
	ctx, cancel := context.WithCancel(context.Background())
	job := &wildJob{cancel: cancel, reverse: reverse}
	w.job = job
	w.tail = strings.Contains(w.orig, "/")
	home, _ := os.UserHomeDir()
	q := fileQuery{
		word: w.orig, workDir: e.workDir(), home: home, env: envLookup(e.childEnv()),
		ignoreCase: e.options.Get(option.CommandLineCompleteIgnoreCase).Bool(),
	}
	c, stop := e.view, e.stop
	go func() {
		defer e.recoverPanic()
		defer cancel()
		items, shown, err := collectFiles(ctx, q)
		select {
		case e.inspect <- func() { e.wildFilesDone(c, w, job, items, shown, err) }:
		case <-stop:
		}
	}()
}

// wildFilesDone は、client c の補う状態 w の仕事 job で集めたファイルの候補を受け取る。イベントループの goroutine で呼ぶ。
// 仕事を取り消していた（その後にほかのキーを打った）か、コマンドラインが変わっていれば捨てる。
// 集められなかった（ディレクトリが無い、読めない）ときは、候補が無いものとして扱う（Neovim は音を鳴らすだけである）。
//
// req: FR-199
func (e *Editor) wildFilesDone(c *clientView, w *wildMenu, job *wildJob, items, shown []string, err error) {
	if !c.attached() || e.viewOf(c).wild != w || w.job != job {
		return
	}
	e.useClient(c)
	w.job = nil
	if e.currentWild() != w {
		return
	}
	if err != nil {
		items, shown = nil, nil
	}
	w.items, w.shown = items, shown
	e.wildBegin(w, job.reverse)
	// 集めている間に打った補完のキーを、打った順に処理する。続く <Tab> がまた候補を集め始めたら、残りはその仕事が取っておく。
	for _, k := range job.queue {
		e.feedKey(k)
	}
}

// envLookup は、client の外部のコマンドに足す環境変数 env（"名前=値" の列。後のものが勝つ）を先に、server のプロセスの環境変数を後に
// 引く関数を返す。
func envLookup(env []string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		for i := len(env) - 1; i >= 0; i-- {
			if k, v, ok := strings.Cut(env[i], "="); ok && k == name {
				return v, true
			}
		}
		return os.LookupEnv(name)
	}
}

// expandFilePath は、Ex コマンドのファイルの引数 path の、先頭の "~"（"~" だけか "~/" で始まるもの）を home に、
// "$名前" と "${名前}" を env で引いた値に置き換える（FR-200。Vim 9.1 と Neovim 0.12.5 の :e ~/a と :e $HOME/a と同じ）。
// 引けない環境変数と、"~ユーザー名" はそのまま残す。home が空なら "~" を残す。
//
// req: FR-200
func expandFilePath(path, home string, env func(string) (string, bool)) string {
	if home != "" && (path == "~" || strings.HasPrefix(path, "~/")) {
		path = home + path[1:]
	}
	if !strings.Contains(path, "$") {
		return path
	}
	var sb strings.Builder
	for i := 0; i < len(path); i++ {
		if path[i] != '$' {
			sb.WriteByte(path[i])
			continue
		}
		j, name := i+1, ""
		if j < len(path) && path[j] == '{' {
			if k := strings.IndexByte(path[j:], '}'); k > 1 {
				name = path[j+1 : j+k]
				j += k + 1
			}
		} else {
			for j < len(path) && (path[j] == '_' || isAlnum(path[j])) {
				j++
			}
			name = path[i+1 : j]
		}
		v, ok := "", false
		if name != "" {
			v, ok = env(name)
		}
		if !ok {
			sb.WriteByte('$')
			continue
		}
		sb.WriteString(v)
		i = j - 1
	}
	return sb.String()
}

// isAlnum は、c が ASCII の英数字かを返す。
func isAlnum(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9'
}

// fileSuffixes は、補完の候補の後ろへ回すファイルの名前の末尾である（Vim 9.1 と Neovim 0.12.5 の 'suffixes' の既定）。
var fileSuffixes = []string{".bak", "~", ".o", ".h", ".info", ".swp", ".obj"}

// fileReadChunk は、ディレクトリを一度に読む項目の数である。読むたびに取り消されたかを確かめる。
const fileReadChunk = 1024

// collectFiles は、ファイルとディレクトリの補完の候補を集める（FR-199）。items はコマンドラインに入れる形（打った語の最後の "/" までに
// 名前を足し、ex.EscapeFileArg で "\" を付けたもの）、shown は一覧に出す形（名前だけ）である。ディレクトリの名前には "/" を付ける。
// 読み方は Neovim 0.12.5 の :e の補完に合わせた（2026-11-22 に、名前に空白を含むファイル、隠しファイル、'suffixes' に当たるファイルの
// あるディレクトリで確かめた）。
//
//   - 語の "\ " と "\|" を戻し、"~" と環境変数を展開する（expandFilePath）。展開した環境変数は候補にも残る（Neovim の :e $HOME/<Tab> が
//     絶対パスを入れるのと同じ）。"~" は候補に残す
//   - 語の最後の "/" までをディレクトリ（無ければ workDir、相対パスなら workDir から）として、その中の、"/" の後で始まる名前を集める。
//     ignoreCase なら大文字と小文字を区別しない
//   - "." で始まる名前は、"/" の後が "." で始まるときだけ集める。そのときは "." と ".." も集める（Neovim の :e .<Tab> と同じ）
//   - 名前のバイトの順に並べ、末尾が fileSuffixes に当たるファイルを後ろへ回す
//
// 読む途中で ctx が終わったら、ctx.Err() を返す。
func collectFiles(ctx context.Context, q fileQuery) (items, shown []string, err error) {
	if q.word == "~" && q.home != "" {
		// "~" だけなら、ホームディレクトリの絶対パスを補う（Neovim 0.12.5 の :e ~<Tab> と同じ。"~/" で始まる語は "~" を残す）。
		home := ex.EscapeFileArg(strings.TrimSuffix(q.home, "/") + "/")
		return []string{home}, []string{home}, nil
	}
	pat := expandFilePath(ex.UnescapeFileArg(q.word), q.home, q.env)
	// 候補に残す "~" の形のディレクトリと、読むディレクトリ。
	shownDir := ex.UnescapeFileArg(q.word)
	if strings.Contains(shownDir, "$") {
		shownDir = pat
	} else if q.home != "" && strings.HasPrefix(shownDir, "~/") {
		shownDir = "~" + pat[len(q.home):]
	}
	slash := strings.LastIndexByte(pat, '/')
	dir, base := pat[:slash+1], pat[slash+1:]
	shownSlash := strings.LastIndexByte(shownDir, '/')
	prefix := shownDir[:shownSlash+1]
	readDir := dir
	switch {
	case dir == "":
		readDir = q.workDir
	case !filepath.IsAbs(dir):
		readDir = filepath.Join(q.workDir, dir)
	}
	f, err := os.Open(readDir)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	hidden := strings.HasPrefix(base, ".")
	matches := func(name string) bool {
		if len(name) < len(base) {
			return false
		}
		if q.ignoreCase {
			return strings.EqualFold(name[:len(base)], base)
		}
		return strings.HasPrefix(name, base)
	}
	type entry struct {
		name string
		dir  bool
	}
	var found []entry
	if hidden {
		for _, n := range []string{".", ".."} {
			if matches(n) {
				found = append(found, entry{name: n, dir: true})
			}
		}
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		des, rerr := f.ReadDir(fileReadChunk)
		for _, de := range des {
			name := de.Name()
			if (!hidden && strings.HasPrefix(name, ".")) || !matches(name) {
				continue
			}
			isDir := de.IsDir()
			if de.Type()&os.ModeSymlink != 0 {
				if st, err := os.Stat(filepath.Join(readDir, name)); err == nil {
					isDir = st.IsDir()
				}
			}
			found = append(found, entry{name: name, dir: isDir})
		}
		if errors.Is(rerr, io.EOF) || (rerr == nil && len(des) == 0) {
			break
		}
		if rerr != nil {
			return nil, nil, rerr
		}
	}
	suffixed := func(en entry) bool {
		return !en.dir && slices.ContainsFunc(fileSuffixes, func(s string) bool { return strings.HasSuffix(en.name, s) })
	}
	slices.SortStableFunc(found, func(a, b entry) int {
		if sa, sb := suffixed(a), suffixed(b); sa != sb {
			if sa {
				return 1
			}
			return -1
		}
		return strings.Compare(a.name, b.name)
	})
	items, shown = make([]string, len(found)), make([]string, len(found))
	for i, en := range found {
		name := en.name
		if en.dir {
			name += "/"
		}
		items[i] = ex.EscapeFileArg(prefix + name)
		shown[i] = ex.EscapeFileArg(name)
	}
	return items, shown, nil
}
