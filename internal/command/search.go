package command

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
	"github.com/piplupflipper/vi-imitation/internal/command/ex"
	"github.com/piplupflipper/vi-imitation/internal/command/target"
	"github.com/piplupflipper/vi-imitation/internal/editcmd"
	"github.com/piplupflipper/vi-imitation/internal/option"
)

// Message は、操作が利用者に知らせる 1 行の通知である。
type Message struct {
	// Text は通知の文言である。空なら通知は無い。
	Text string
	// Warning は、警告として出すことを表す（検索がバッファの端で折り返したとき）。
	Warning bool
	// Count は、Text と同じ行の右に寄せて出す、検索の一致の件数（"[2/5]"、折り返したら "W [1/5]"）である（FR-167）。
	// 空なら出さない。設定の search.showCount が偽なら、常に空である。
	Count string
}

// 検索の一致の件数（FR-167）の上限。Neovim 0.12.5 の既定に合わせた。
const (
	// searchCountLimit は、数える一致の件数の上限である。超えたら ">999" と出す（Neovim 0.12.5 の 'maxsearchcount' の既定。
	// Vim 9.1 の既定は 99。2026-11-21 に :set maxsearchcount? で確かめた）。
	searchCountLimit = 999
)

// searchCountTimeout は、一致を数える時間の上限である。超えたら件数の代わりに "[?/??]" と出す。
// Neovim 0.12.5 の検索の件数の時間の上限（src/nvim/search.h の SEARCH_STAT_DEF_TIMEOUT。v0.12.5 で 40）と同じにした。
// 2026-11-21 に nvim --clean で、searchcount({'timeout': 0}) で測った数える時間が 36 ms のファイルでは件数が出て、
// 45 ms のファイルでは "[?/??]" になり、この値と合う。テストが差し替える。
var searchCountTimeout = 40 * time.Millisecond

// matchCache は、集めた一致の一覧（target.Matches）と、集めたときのバッファ、バージョン番号、パターン、件数の上限である（SearchMatches）。
// バッファが変わらず、同じパターンで n を続けて押したときに、集め直さずに件数を出すために持つ。
type matchCache struct {
	buf     *buffer.Buffer
	version uint64
	pattern string
	limit   int
	matches target.Matches
}

// SearchMatches は、このバッファの、パターン pat の一致の一覧を、バッファの先頭から limit+1 件まで返す（target.FindMatches）。
// 検索の一致の件数（FR-167）が使う。後に作る右端の列の一致の印も、この口か target.FindMatches で一覧を読める。
//
// 集めた一覧は、バッファ、バージョン番号、パターン、上限と組で 1 つだけ覚え、どれも同じ間は集め直さない。
// バッファを変えるとバージョン番号が進むので、編集、外からの編集、アンドゥの後は集め直す。
// 集めるのはイベントループの上で、時間の上限は timeout である。超えたら context.DeadlineExceeded を返し、覚えない。
// パターンが空なら target.ErrNoPattern、正しくなければ *target.PatternError を返す。
//
// req: FR-167
func (s *State) SearchMatches(pat string, limit int, timeout time.Duration) (target.Matches, error) {
	if c := s.matchCache; c != nil && c.buf == s.buf && c.version == s.buf.Version() && c.pattern == pat && c.limit == limit {
		return c.matches, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ms, err := target.FindMatches(ctx, s.buf, pat, limit)
	if err != nil {
		return target.Matches{}, err
	}
	s.matchCache = &matchCache{buf: s.buf, version: s.buf.Version(), pattern: pat, limit: limit, matches: ms}
	return ms, nil
}

// searchCount は、設定の search.showCount が真なら、パターン pat の一致のうち at が何件目かと件数を、メッセージの行の右に出す形
// （"[2/5]"。wrapped なら "W [1/5]"）で返す（FR-167）。偽か、設定を読めないなら空を返す。
//
// 形は Neovim 0.12.5 の既定（'shortmess' に S が無い）の検索の件数に合わせる（2026-11-21 に nvim --clean を tmux で動かして確かめた）。
//
//   - 件数が上限（searchCountLimit）を超えたら "[2/>999]"、at も上限を超えた先なら "[>999/>999]"
//   - 数える時間の上限（searchCountTimeout）を超えたら "[?/??]"
//   - バッファの端で折り返したら、前に "W " を付ける（Neovim は件数を出すとき、折り返しの警告の文の代わりに W を付ける）
//
// 一致は SearchMatches で集め、バッファとバージョン番号とパターンが同じ間は集め直さない。
// 時間の上限を超えたときは覚えないので、次の検索で数え直す。
//
// req: FR-167
func (s *State) searchCount(pat string, at buffer.Position, wrapped bool) string {
	if s.Ex.Options == nil {
		return ""
	}
	d, ok := option.Lookup(option.SearchShowCount)
	if !ok || !s.Ex.Options.Get(d).Bool() {
		return ""
	}
	ms, err := s.SearchMatches(pat, searchCountLimit, searchCountTimeout)
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return wrapMark(wrapped) + "[?/??]"
	case err != nil:
		return ""
	}
	cur, total, over := ms.Count(at)
	var count string
	switch {
	case over && cur > searchCountLimit:
		count = fmt.Sprintf("[>%d/>%d]", searchCountLimit, searchCountLimit)
	case over:
		count = fmt.Sprintf("[%d/>%d]", cur, searchCountLimit)
	default:
		count = fmt.Sprintf("[%d/%d]", cur, total)
	}
	return wrapMark(wrapped) + count
}

// wrapMark は、検索がバッファの端で折り返したなら、件数の前に付ける "W " を返す（Vim の shm-s の説明の W）。
func wrapMark(wrapped bool) string {
	if wrapped {
		return "W "
	}
	return ""
}

// pendingSearch は、/ か ? でパターンを入力している間の、パターンを待っている編集コマンドの値である。
type pendingSearch struct {
	cmd editcmd.Command
	// back は、入力を終えたときに戻るモード（ノーマルモードかビジュアルモード）である。
	back Mode
	// delim は、コマンドラインの先頭の文字（'/' か '?'）である。
	delim rune
	// gen は、入力を始めるたびに進む番号（State.searchGen）である。打つたびの検索の問い（IncSearch.Gen）に入れる。
	gen uint64
	// steps は、<C-g> を打った数から <C-t> を打った数を引いたものである（target.Request.SearchSteps。FR-153）。
	steps int
	// shown は、打つたびの検索で最後に受け取った結果である（ShowIncSearch）。nil ならまだ受け取っていない。
	shown *incShown
}

// searchInputMotions は、パターンをコマンドラインで受け取る検索のモーションの ID と、コマンドラインの先頭の文字である。
var searchInputMotions = map[string]rune{"search-forward": '/', "search-backward": '?'}

// searchRecordMotions は、実行すると直前の検索のパターンと向きを記録するモーションである。n と N は記録しない（向きを変えない）。
// パターンと向きを決める操作の * と #（actSearchWord）も、同じ記録の経路（SetLastSearch）を通る。
var searchRecordMotions = map[string]bool{
	"search-forward": true, "search-backward": true,
}

// searchMotionIDs は、検索のモーションの ID である。
var searchMotionIDs = map[string]bool{
	"search-forward": true, "search-backward": true, "search-next": true, "search-prev": true,
}

// LastSearch は、n と N が繰り返す直前の検索のパターンと向きを返す。
// パターンは Ex の :s や :g と共有する（State.Ex.LastPattern）。向きは / と * の後は前向き、? と # の後は後ろ向きである。
func (s *State) LastSearch() target.Search {
	return target.Search{Pattern: s.Ex.LastPattern, Backward: s.searchBackward}
}

// SetLastSearch は、n と N が繰り返す直前の検索を sr にする。検索のパターンと向きを決める操作（/、?、*、#）は、どれもこれで記録する。
// 記録すると、:search.clearHighlight で消した一致の色を戻す（ex.State.UsePattern。FR-138）。
// n と N は、直前の検索をそのまま記録し直す（パターンと向きは変わらず、色だけが戻る）。
//
// req: FR-138, FR-139
func (s *State) SetLastSearch(sr target.Search) {
	s.Ex.UsePattern(sr.Pattern)
	s.searchBackward = sr.Backward
}

// HighlightPattern は、一致の色を付けるパターンを返す。色を消している（:search.clearHighlight）か、パターンが無ければ空を返す。
// rev は、設定の search.highlight を設定した回数（option.Values.Revision）である（ex.State.HighlightPattern）。
// 設定の search.highlight の値は見ない。描画（internal/editor）が、描くたびに設定の値と合わせて読む（FR-137）。
//
// req: FR-137
func (s *State) HighlightPattern(rev uint64) string {
	return s.Ex.HighlightPattern(rev)
}

// actSearchWord は、*（backward が true なら #）である（FR-139）。
// カーソルの単語に全体で一致するパターン（target.WordPattern）と向きを、/ と同じ経路（SetLastSearch）で直前の検索として記録する。
// カーソルは動かさず、ジャンプリストにも記録しない。次の一致へは n で移る。回数は使わない。
// 移らないので、モーションでなく対象を取らない操作にした（オペレーターの後には使えない）。ビジュアルモードでは、選択を変えない。
// 記録したパターンは、/ で見つけたときと同じく通知に出す（* は "/"、# は "?" を前に付ける）。
// 設定の search.showCount が真なら、パターンにした単語の先頭が何件目の一致かと件数も出す（FR-167）。
// カーソルでなく単語の先頭で数えるので、カーソルが単語の手前の空白にいても、オーナーの nvim の * の割り当て（*N。FR-139）で
// 単語の先頭へ移ったときと同じ番号になる（2 行目の "baz foo" の空白で [2/4]、"  foo"、"foo" の先頭の空白で [1/2]。
// 2026-11-21 に nvim 0.12.5 の *N で確かめた。PR #322 のレビューの推奨 5）。カーソルは動かさない。
//
// req: FR-139, FR-167
func actSearchWord(backward bool) actionFunc {
	return func(s *State, _ editcmd.Command) error {
		pat, start, err := target.WordPatternAt(s.buf, s.cursor)
		if err != nil {
			return &ex.Error{Code: "E348", Msg: "カーソルの位置に文字列がありません"}
		}
		s.SetLastSearch(target.Search{Pattern: pat, Backward: backward})
		delim := "/"
		if backward {
			delim = "?"
		}
		s.res.Message = Message{Text: delim + pat, Count: s.searchCount(pat, start, false)}
		return nil
	}
}

// CommandLineType は、コマンドラインモードの種類を、行の先頭に出す文字で返す。
// Ex コマンドは ':'、前向きの検索（/）は '/'、後ろ向きの検索（?）は '?' である。コマンドラインモードでなければ ':' を返す。
func (s *State) CommandLineType() rune {
	if s.search != nil {
		return s.search.delim
	}
	return ':'
}

// startSearch は、パターンの無い / か ? の値 cmd を取っておき、パターンを入力するコマンドラインモードへ移る。
// オペレーターの後（d/）なら、オペレーターごと取っておく。回数とレジスターも値に残る。
// 戻ってきたら finishSearch がパターンを埋めて実行する。
//
// req: FR-01, FR-03
func (s *State) startSearch(cmd editcmd.Command, delim rune) {
	s.searchGen++
	s.search = &pendingSearch{cmd: cmd, back: s.mode, delim: delim, gen: s.searchGen}
	s.cmdline = s.cmdline[:0]
	s.mode = ModeCommandLine
}

// cancelSearch は、パターンの入力を取り消し、入力を始めたモードへ戻る（<Esc> と、空の行の <BS>）。
// オペレーターの後なら、オペレーターも取り消す。ビジュアルモードから始めたなら、選択を残したままビジュアルモードへ戻る（Vim と同じ）。
func (s *State) cancelSearch() {
	s.mode = s.search.back
	s.search = nil
	s.cmdline = s.cmdline[:0]
}

// finishSearch は、<CR> で確定したパターン text を、取っておいた値に埋めて実行する。
// text が空なら（/<CR>）、直前のパターンを使う。直前のパターンも無ければ E35 で失敗する。
// オフセット（/foo/e の e）は未対応で、空でないオフセットは E319 で失敗する。空のオフセット（/foo/）は無いものとして扱う。
//
// 打っている間に <C-g> と <C-t> で一致を動かしていれば、その数（pendingSearch.steps）だけ動かした一致へ移る（target.Request.SearchSteps）。
// 一致は、打つたびの検索と同じ target.FindMatch で、時間の上限を付けずに決めるので、打っている間に出した一致と同じになる（FR-153）。
// ジャンプリスト、直前の検索、消した一致の色を戻すことは、ここから実行する値（s.run）だけが行う。打つたびの検索は、どれも変えない。
// 動かした数は、実行する値には残さない。ドットリピートは、パターンと回数だけを繰り返す。
//
// req: FR-03, FR-09, FR-153
func (s *State) finishSearch(text string) {
	ps := s.search
	s.mode = ps.back
	s.search = nil
	pat, offset, _ := target.SplitSearch(text, ps.delim)
	if offset != "" {
		s.fail(&ex.Error{Code: "E319", Msg: "検索のオフセットは使えません: " + offset})
		return
	}
	if pat == "" {
		pat = s.Ex.LastPattern
	}
	if pat == "" {
		s.fail(&ex.Error{Code: "E35", Msg: "直前の正規表現がありません"})
		return
	}
	cmd := ps.cmd
	cmd.Target.Pattern = pat
	s.searchSteps = ps.steps
	defer func() { s.searchSteps = 0 }()
	s.run(cmd)
}

// vimStyleSeqs は、Vim の正規表現では特別な意味を持つが、Go の regexp（RE2）では文字そのもの（\v は垂直タブ）に一致する
// "\" の組である。これを含むパターンが見つからなかったら、Vim の書き方で打った見込みがあるので、E486 に vimStyleHint を添える。
var vimStyleSeqs = []string{`\<`, `\>`, `\|`, `\(`, `\)`, `\{`, `\+`, `\=`, `\v`}

// vimStyleHint は、Vim の書き方の見込みがあるパターンの E486 に添える案内である。対応の全体は docs/vim-differences.md にある。
const vimStyleHint = `（正規表現は Go の regexp（RE2）の文法で書きます。例: \<foo\> は \bfoo\b、a\|b は a|b、\(a\) は (a)）`

// vimStyle は、パターンが vimStyleSeqs のどれかを含むかを返す。"\\" の組は飛ばす。
func vimStyle(pat string) bool {
	for i := 0; i+1 < len(pat); i++ {
		if pat[i] != '\\' {
			continue
		}
		if slices.Contains(vimStyleSeqs, pat[i:i+2]) {
			return true
		}
		i++
	}
	return false
}

// searchResult は、検索のモーション t の結果 res と誤り err を、State に反映する。
//   - / と ? は、見つからなかった場合も含めて、検索したパターンと向きを直前の検索として記録する（Vim と同じ）。
//     パターンは Ex と共有するので、/foo の後の :s//x/ は foo を置き換える
//   - n と N は、パターンを検索したら（見つからなかった場合も含めて）直前の検索をそのまま記録し直し、消した一致の色を戻す（FR-138）
//   - 見つけたら、Vim と同じく検索したパターンを通知に出す（/foo、n の後も /foo）。バッファの端で折り返したら、代わりに警告を出す
//   - 設定の search.showCount が真なら、見つけた一致が何件目かと件数を、パターンと同じ行の右に出す（FR-167）。
//     このときは、折り返しの警告の文を出さず、件数の前に W を付けて、パターンを出す（Neovim 0.12.5 と同じ）。
//     件数は、オペレーターがバッファを変える前に、移り先の一致の位置で数える（d/foo でも、移り先の一致の番号を出す）
//   - 誤りは、利用者に出す Vim のエラー番号の付いた誤り（*ex.Error）にする。見つからなかったとき（E486）は件数を出さない
//
// パターンが正しくない（E383）か無い（E35）ときは、記録せず、消した一致の色も戻さない。
//
// req: FR-03, FR-138, FR-167
func (s *State) searchResult(t editcmd.Target, res target.Result, err error) error {
	switch {
	case res.Search.Pattern == "":
	case searchRecordMotions[t.Ref.ID]:
		s.SetLastSearch(res.Search)
	default:
		s.SetLastSearch(s.LastSearch())
	}
	var pe *target.PatternError
	switch {
	case err == nil:
	case errors.Is(err, target.ErrPatternNotFound):
		msg := "パターンが見つかりません: " + res.Search.Pattern
		if vimStyle(res.Search.Pattern) {
			msg += vimStyleHint
		}
		return &ex.Error{Code: "E486", Msg: msg}
	case errors.Is(err, target.ErrNoPattern):
		return &ex.Error{Code: "E35", Msg: "直前の正規表現がありません"}
	case errors.As(err, &pe):
		return &ex.Error{Code: "E383", Msg: "正規表現が正しくありません（Go の regexp の文法で書きます）: " + pe.Pattern + ": " + pe.Err.Error()}
	default:
		return err
	}
	delim := "/"
	if res.Search.Backward {
		delim = "?"
	}
	if count := s.searchCount(res.Search.Pattern, res.Pos, res.Wrapped); count != "" {
		s.res.Message = Message{Text: delim + res.Search.Pattern, Count: count}
		return nil
	}
	switch {
	case res.Wrapped && res.Search.Backward:
		s.res.Message = Message{Text: "先頭まで検索したので末尾から続けました", Warning: true}
	case res.Wrapped:
		s.res.Message = Message{Text: "末尾まで検索したので先頭から続けました", Warning: true}
	default:
		s.res.Message = Message{Text: delim + res.Search.Pattern}
	}
	return nil
}
