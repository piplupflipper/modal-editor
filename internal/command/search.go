package command

import (
	"errors"
	"slices"

	"github.com/piplupflipper/vi-imitation/internal/command/ex"
	"github.com/piplupflipper/vi-imitation/internal/command/target"
	"github.com/piplupflipper/vi-imitation/internal/editcmd"
)

// Message は、操作が利用者に知らせる 1 行の通知である。
type Message struct {
	// Text は通知の文言である。空なら通知は無い。
	Text string
	// Warning は、警告として出すことを表す（検索がバッファの端で折り返したとき）。
	Warning bool
}

// pendingSearch は、/ か ? でパターンを入力している間の、パターンを待っている編集コマンドの値である。
type pendingSearch struct {
	cmd editcmd.Command
	// back は、入力を終えたときに戻るモード（ノーマルモードかビジュアルモード）である。
	back Mode
	// delim は、コマンドラインの先頭の文字（'/' か '?'）である。
	delim rune
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
//
// req: FR-139
func actSearchWord(backward bool) actionFunc {
	return func(s *State, _ editcmd.Command) error {
		pat, err := target.WordPattern(s.buf, s.cursor)
		if err != nil {
			return &ex.Error{Code: "E348", Msg: "カーソルの位置に文字列がありません"}
		}
		s.SetLastSearch(target.Search{Pattern: pat, Backward: backward})
		delim := "/"
		if backward {
			delim = "?"
		}
		s.res.Message = Message{Text: delim + pat}
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
	s.search = &pendingSearch{cmd: cmd, back: s.mode, delim: delim}
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
// req: FR-03, FR-09
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
//   - 誤りは、利用者に出す Vim のエラー番号の付いた誤り（*ex.Error）にする
//
// パターンが正しくない（E383）か無い（E35）ときは、記録せず、消した一致の色も戻さない。
//
// req: FR-03, FR-138
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
	switch {
	case res.Wrapped && res.Search.Backward:
		s.res.Message = Message{Text: "先頭まで検索したので末尾から続けました", Warning: true}
	case res.Wrapped:
		s.res.Message = Message{Text: "末尾まで検索したので先頭から続けました", Warning: true}
	case res.Search.Backward:
		s.res.Message = Message{Text: "?" + res.Search.Pattern}
	default:
		s.res.Message = Message{Text: "/" + res.Search.Pattern}
	}
	return nil
}
