package editor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/piplupflipper/modal-editor/internal/buffer"
	"github.com/piplupflipper/modal-editor/internal/command"
	"github.com/piplupflipper/modal-editor/internal/command/ex"
	"github.com/piplupflipper/modal-editor/internal/editcmd"
	"github.com/piplupflipper/modal-editor/internal/ui"
)

// DefaultCheckInterval は、開いているバッファのファイルが外で変わっていないかを確かめる間隔の既定値（仮）である（基本設計 6.7）。
// エージェントがディスクへ直接書いた変更を、利用者が気づく前に画面へ出すため、Vim の契機（フォーカスの復帰、:checktime）を待たずに定期的に確かめる。
// 1 回の確認はバッファごとに stat を 1 回呼ぶだけなので、1 秒にした。
const DefaultCheckInterval = time.Second

// errChangedOnDisk は、:w が、読み込んだ後にファイルが外で変わっていたことを見つけたことを表す。
// 呼び出し側（runActions）は誤りとして知らせず、Vim と同じく書き込むかを問う。
var errChangedOnDisk = errors.New("読み込んだ後にファイルが変更されています")

// fileStamp は、ディスク上のファイルの状態（有無、更新時刻、大きさ）である。外での変更の検知に使う（基本設計 6.7）。
// Vim と同じく、中身ではなく更新時刻と大きさで変更を見分ける。更新時刻の刻みより短い間に同じ大きさで書き換えられると見逃す。
type fileStamp struct {
	exists bool
	mtime  time.Time
	size   int64
}

// statFile は、path のファイルの状態を返す。ファイルが無ければ exists が false の値を返し、誤りにしない。
func statFile(path string) (fileStamp, error) {
	fi, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return fileStamp{}, nil
	}
	if err != nil {
		return fileStamp{}, err
	}
	return fileStamp{exists: true, mtime: fi.ModTime(), size: fi.Size()}, nil
}

// differs は、s と o が違う状態かを返す。
func (s fileStamp) differs(o fileStamp) bool {
	return s.exists != o.exists || !s.mtime.Equal(o.mtime) || s.size != o.size
}

// setDisk は、バッファとファイルの内容が一致した時点（読み込み、書き出し、読み直し）のファイルの状態を記録する。
func (bs *bufferState) setDisk(st fileStamp) {
	bs.disk, bs.seen = st, st
}

// prompt は、本体が出す問い（:w の上書きの確認、W11〜W13）である。問いの間は、Ex の確認の問い（Editor.confirm）と同じく、
// 拡張機能のイベントを処理せずに取っておき、外での変更も確かめない。
type prompt struct {
	text string
	// answer は、利用者のキーを受け取り、答えとして受け付けたら true を返す。受け付けないキーでは何もしない（問いは続く）。
	answer func(k editcmd.Key) bool
}

// asking は、確認の問い（Ex の確認か、本体の問い）を出しているかを返す。
func (e *Editor) asking() bool {
	return e.confirm != nil || e.prompt != nil
}

// promptKey は、本体の問いへのキーを処理する。答えの処理が新しい問いを出すことがある（:w|w のような続きの動作）。
func (e *Editor) promptKey(k editcmd.Key) {
	p := e.prompt
	e.prompt = nil
	e.message = noMessage
	if !p.answer(k) {
		e.prompt = p
	}
}

// runActions は、Ex コマンドの動作（:w、:q、:e など）を順に行う。すべて行えたら true を返す。
// 動作が失敗したら誤りを知らせ、残りを捨てて false を返す（Vim は :w が失敗すると右のコマンドを実行しない）。
// :w が外での変更を見つけたら、書き込むかを問い（askWrite）、残りの動作は答えが y のときに行う。このときも false を返す。
func (e *Editor) runActions(bs *bufferState, acts []ex.Action) bool {
	for i, a := range acts {
		err := e.doAction(bs, a)
		switch {
		case err == nil:
			continue
		case errors.Is(err, errChangedOnDisk):
			e.askWrite(bs, a, acts[i+1:])
		default:
			e.showError(err)
		}
		return false
	}
	return true
}

// askWrite は、Vim の「WARNING: The file has been changed since reading it!!!」と同じく、外で変わったファイルへ書き込むかを問う。
// y なら、:w! と同じく確かめずに書き出し、続きの動作（:wq の :q など）を行う。n か <Esc> なら何もしない。
//
// req: FR-08
func (e *Editor) askWrite(bs *bufferState, a ex.Action, rest []ex.Action) {
	e.prompt = &prompt{
		text: "警告: 読み込んだ後にファイルが変更されています!!! 本当に書き込みますか (y/n)?",
		answer: func(k editcmd.Key) bool {
			switch k {
			case editcmd.Rune('y'):
				a.Force = true
				e.runActions(bs, append([]ex.Action{a}, rest...))
			case editcmd.Rune('n'), editcmd.Esc:
			default:
				return false
			}
			return true
		},
	}
}

// writeConflict は、:w（バッファのファイルへの書き出し）の前に、ファイルが外で変わっていないかを確かめる（Vim と同じ）。
//   - 読み込んだとき（最後に書き出したとき）より後に更新時刻か大きさが変わっていれば errChangedOnDisk を返す
//   - 読み込んだときに無かったファイルが、今はあれば E13 を返す（Vim 9.1 も、新しいファイルの編集中に外で作られたファイルへの :w は E13）
//
// 読み込んだ後に消されたファイルへは、確かめずに書き出す（Vim と同じ）。状態を得られなければ、書き出しの誤りに任せる。
func writeConflict(bs *bufferState) error {
	now, ok := stampOf(bs.path)
	switch {
	case !ok:
		// 状態を得られなければ、書き出しの誤りに任せる。
	case !bs.disk.exists && now.exists:
		return errFileExists
	case bs.disk.exists && now.exists && bs.disk.differs(now):
		return errChangedOnDisk
	}
	return nil
}

// stampOf は、statFile の結果を、得られたかどうかの真偽で返す。
func stampOf(path string) (fileStamp, bool) {
	st, err := statFile(path)
	return st, err == nil
}

// checkInterval は、外での変更を確かめる間隔を返す。0 なら確かめない。
func (e *Editor) checkInterval() time.Duration {
	switch {
	case e.cfg.CheckInterval < 0:
		return 0
	case e.cfg.CheckInterval == 0:
		return DefaultCheckInterval
	}
	return e.cfg.CheckInterval
}

// autoRead は、未変更のバッファのファイルが外で変わったときに、問わずに読み直すかを返す（Config.AutoRead）。
func (e *Editor) autoRead() bool {
	return e.cfg.AutoRead == nil || *e.cfg.AutoRead
}

// checkFiles は、開いているバッファのファイルが外で変わっていないかを確かめる（基本設計 6.7 の外での変更の確認）。
// Vim の :checktime と同じ扱いを、キー入力が無い間も定期的に行う。
//
// 利用者が入力している途中（ノーマルモード以外、キーの解釈の途中、処理していないキーがある）と、問いを出している間は確かめない。
// 編集中の行がその場で置き換わったり、問いが入力の途中に割り込んだりしないためである。
// 問いを出したら、残りのバッファは次の機会に確かめる。
// 外での変更を見つけたら（画面を描き直す要があれば）true を返す。
func (e *Editor) checkFiles() bool {
	st := e.cur.state
	if e.asking() || e.activeChoice() != nil || !e.keys.empty() || e.remap.pendingKeys() ||
		st.Mode() != command.ModeNormal || st.Pending() {
		return false
	}
	found := false
	for _, bs := range e.bufs {
		if bs.path == "" {
			continue
		}
		if e.checkFile(bs) {
			found = true
		}
		if e.prompt != nil {
			break
		}
	}
	return found
}

// checkFile は、バッファ bs のファイルが、最後に確かめた後に外で変わっていれば、Vim と同じく次のように扱う。
//   - 消えた: E211 の警告を出す
//   - 未変更のバッファで、ファイルの中身がバッファと同じ: 何も出さず、読み込んだときの状態を更新する
//   - 未変更のバッファで、autoread が有効: 読み直す（:e と同じくアンドゥで戻れる）
//   - それ以外: W11（未変更のバッファ）、W12（変更のあるバッファ）、W13（読み込んだときに無かったファイル）の問いを出す
//
// 一度知らせた変更は、ファイルがまた変わるまで知らせない（Vim と同じ）。ただし問いで読み込まなかった変更は、
// :w の確認（writeConflict）では変わったままに扱う（Vim 9.1 で、W11 に OK と答えた後の :w は警告を出す）。
//
// 変更を見つけたら true を返す。
//
// req: FR-08
func (e *Editor) checkFile(bs *bufferState) bool {
	now, err := statFile(bs.path)
	if err != nil || !now.differs(bs.seen) {
		return false
	}
	bs.seen = now
	name := e.shortName(bs)
	if !now.exists {
		e.message = ui.Message{Text: fmt.Sprintf("E211: ファイル \"%s\" はもう存在しません", name), Level: ui.MessageWarning}
		return true
	}
	if !bs.buf.Modified() {
		b, err := buffer.ReadFile(bs.path)
		if err != nil {
			e.showError(fmt.Errorf("%s を読めません: %w", name, err))
			return true
		}
		if sameContent(bs.buf, b) {
			bs.disk = now
			return true
		}
		if e.autoRead() {
			e.reload(bs, b)
			bs.disk = now
			e.message = infoMessage(fmt.Sprintf("\"%s\" は外で変更されたので読み直しました", name))
			return true
		}
	}
	var text string
	switch {
	case bs.buf.Modified():
		text = fmt.Sprintf("W12: 警告: ファイル \"%s\" が変更され、バッファも変更されています", name)
	case !bs.disk.exists:
		text = fmt.Sprintf("W13: 警告: 編集を始めた後にファイル \"%s\" が作成されました", name)
	default:
		text = fmt.Sprintf("W11: 警告: 編集を始めた後にファイル \"%s\" が変更されました", name)
	}
	e.prompt = &prompt{
		text: text + " [O]K, (L)oad File:",
		answer: func(k editcmd.Key) bool {
			switch k {
			case editcmd.Rune('l'), editcmd.Rune('L'):
				if err := e.reloadFile(bs); err != nil {
					e.showError(err)
				}
			case editcmd.Rune('o'), editcmd.Rune('O'), editcmd.Esc, editcmd.Special(editcmd.CodeEnter):
			default:
				return false
			}
			return true
		},
	}
	return true
}

// shortName は、問いと警告に出すバッファの名前を返す。作業ディレクトリの下のファイルなら相対パスにする。
// 問いの答え方（[O]K, (L)oad File:）を行の末尾に置くので、絶対パスで行の幅を超えて答え方が見えなくなるのを避けるためである（Vim も開いたときの名前で出す）。
func (e *Editor) shortName(bs *bufferState) string {
	if rel, err := filepath.Rel(e.cfg.WorkDir, bs.path); err == nil && filepath.IsLocal(rel) {
		return rel
	}
	return displayName(bs)
}

// sameContent は、バッファ a と b の内容（書き出したときのファイルの中身を決めるもの）が同じかを返す。
func sameContent(a, b *buffer.Buffer) bool {
	return a.Text() == b.Text() && a.HasTrailingNewline() == b.HasTrailingNewline() && a.NoLines() == b.NoLines()
}

// reloadFile は、バッファ bs をファイルから読み直す（:e、:e!、W11〜W13 の問いの (L)oad File）。
// ファイルが無ければ、行の無いバッファにする。読み直したときのファイルの状態を記録する。
func (e *Editor) reloadFile(bs *bufferState) error {
	st, _ := statFile(bs.path)
	b, err := buffer.ReadFile(bs.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		b = buffer.New("")
	case err != nil:
		return fmt.Errorf("%s を読めません: %w", filepath.Base(bs.path), err)
	}
	e.reload(bs, b)
	bs.setDisk(st)
	return nil
}
