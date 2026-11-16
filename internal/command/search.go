package command

import (
	"errors"

	"github.com/piplupflipper/modal-editor/internal/command/ex"
	"github.com/piplupflipper/modal-editor/internal/command/target"
	"github.com/piplupflipper/modal-editor/internal/editcmd"
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
var searchRecordMotions = map[string]bool{
	"search-forward": true, "search-backward": true, "search-word-forward": true, "search-word-backward": true,
}

// searchMotionIDs は、検索のモーションの ID である。
var searchMotionIDs = map[string]bool{
	"search-forward": true, "search-backward": true, "search-next": true, "search-prev": true,
	"search-word-forward": true, "search-word-backward": true,
}

// LastSearch は、n と N が繰り返す直前の検索のパターンと向きを返す。
// パターンは Ex の :s や :g と共有する（State.Ex.LastPattern）。向きは / と * の後は前向き、? と # の後は後ろ向きである。
func (s *State) LastSearch() target.Search {
	return target.Search{Pattern: s.Ex.LastPattern, Backward: s.searchBackward}
}

// SetLastSearch は、n と N が繰り返す直前の検索を sr にする。
// Vim と同じく直前の検索をバッファをまたいで 1 つにするために、呼び出し側がバッファを切り替えるときに前のバッファの LastSearch を渡す。
func (s *State) SetLastSearch(sr target.Search) {
	s.Ex.LastPattern = sr.Pattern
	s.searchBackward = sr.Backward
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

// searchResult は、検索のモーション t の結果 res と誤り err を、State に反映する。
//   - / ? * # は、見つからなかった場合も含めて、検索したパターンと向きを直前の検索として記録する（Vim と同じ）。
//     パターンは Ex と共有するので、/foo の後の :s//x/ は foo を置き換える
//   - 見つけたら、Vim と同じく検索したパターンを通知に出す（/foo、n の後も /foo）。バッファの端で折り返したら、代わりに警告を出す
//   - 誤りは、利用者に出す Vim のエラー番号の付いた誤り（*ex.Error）にする
func (s *State) searchResult(t editcmd.Target, res target.Result, err error) error {
	if res.Search.Pattern != "" && searchRecordMotions[t.Ref.ID] {
		s.Ex.LastPattern = res.Search.Pattern
		s.searchBackward = res.Search.Backward
	}
	var pe *target.PatternError
	switch {
	case err == nil:
	case errors.Is(err, target.ErrPatternNotFound):
		return &ex.Error{Code: "E486", Msg: "パターンが見つかりません: " + res.Search.Pattern}
	case errors.Is(err, target.ErrNoPattern):
		return &ex.Error{Code: "E35", Msg: "直前の正規表現がありません"}
	case errors.Is(err, target.ErrNoString):
		return &ex.Error{Code: "E348", Msg: "カーソルの位置に文字列がありません"}
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
