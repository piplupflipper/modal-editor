package editor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
// 記録していた外での変更の問い（notice）は、もう当たらないので消す。
func (bs *bufferState) setDisk(st fileStamp) {
	bs.disk, bs.seen = st, st
	bs.notice = ""
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

// idle は、利用者が入力の途中でなく、外での変更を確かめて結果を反映してよいかを返す。
// ノーマルモードで、キーの解釈の途中でも利用者のキーマップの接頭辞の途中でもなく、処理していないキーが無く、
// 問い（Ex の確認、本体の問い、choice/ask、hit-enter）を出していないときである。
// 編集中の行がその場で置き換わったり、問いが入力の途中に割り込んだりしないためである。
func (e *Editor) idle() bool {
	st := e.cur.state
	return !e.asking() && e.shell == nil && !e.replayPending() && e.activeChoice() == nil && !e.hitEnter() && e.keys.empty() && !e.remap.pendingKeys() &&
		st.Mode() == command.ModeNormal && !st.Pending()
}

// fileResult は、別の goroutine で確かめた、1 つのバッファのファイルの状態である（startCheck）。
type fileResult struct {
	// bs は、結果を返す先のバッファである。確かめる goroutine は読まない（見分けるためだけに持つ）。
	bs *bufferState
	// path と seen は、確かめ始めた時点のバッファのパスと、最後に確かめた状態である。結果を反映する時点で違えば捨てる。
	path string
	seen fileStamp
	now  fileStamp
	// b は、ファイルが変わっていて、あったときに読んだ内容である。readErr は読めなかった理由である。
	b       *buffer.Buffer
	readErr error
}

// startCheck は、開いているバッファのファイルが外で変わっていないかを、別の goroutine で確かめ始める（基本設計 2.1、6.7）。
// 結果は checkDone からイベントループへ届き、applyCheck が反映する。
// stat とファイルの読み込みをイベントループで行わないのは、大きなファイルの読み込みと、止まりうるファイルシステムの stat で
// キー入力の処理を待たせないためである。
// ask が true なら（:checktime）、結果の問いをその場で出す。確かめている途中なら、新しく始めずに ask だけを引き継ぐ。
func (e *Editor) startCheck(ask bool) {
	if e.checking {
		e.checkAsk = e.checkAsk || ask
		return
	}
	var jobs []fileResult
	for _, bs := range e.bufs {
		if bs.path != "" {
			jobs = append(jobs, fileResult{bs: bs, path: bs.path, seen: bs.seen})
		}
	}
	if len(jobs) == 0 {
		return
	}
	e.checking, e.checkAsk = true, ask
	e.checkStart, e.checkWarned = time.Now(), false
	done, stop := e.checkDone, e.stop
	stat := e.statFn
	if stat == nil {
		stat = statFile
	}
	go func() {
		defer e.recoverPanic()
		for i := range jobs {
			j := &jobs[i]
			now, err := stat(j.path)
			if err != nil {
				// 状態を得られなければ、変わっていないものとして扱う。
				now = j.seen
			}
			j.now = now
			if now.exists && now.differs(j.seen) {
				j.b, j.readErr = buffer.ReadFile(j.path)
			}
		}
		select {
		case done <- jobs:
		case <-stop:
		}
	}()
}

// applyCheck は、別の goroutine で確かめた結果を反映する。画面を描き直す要があれば true を返す。
// 反映する時点で利用者が入力の途中なら（idle でない）、何もせずに捨てる。最後に確かめた状態を更新しないので、次の確認でまた見つかる。
// 確かめている間にバッファが読み直されたり、書き出されたり、閉じられたりしたら、そのバッファの結果は捨てる。
func (e *Editor) applyCheck(rs []fileResult) bool {
	e.checking = false
	ask := e.checkAsk
	e.checkAsk = false
	if !e.idle() {
		return false
	}
	changed := false
	for _, r := range rs {
		if !slices.Contains(e.bufs, r.bs) || r.bs.path != r.path || r.bs.seen.differs(r.seen) || !r.now.differs(r.seen) {
			continue
		}
		e.checkFile(r)
		changed = true
	}
	if ask && e.askNotice() {
		changed = true
	}
	return changed
}

// checkFile は、バッファのファイルが、最後に確かめた後に外で変わっていた結果 r を、Vim と同じく次のように扱う。
//   - 消えた: E211 の警告を出す
//   - 未変更のバッファで、ファイルの中身がバッファと同じ: 何も出さず、読み込んだときの状態を更新する
//   - 未変更のバッファで、autoread が有効: 読み直す（:e と同じくアンドゥで戻れる）
//   - それ以外: W11（未変更のバッファ）、W12（変更のあるバッファ）、W13（読み込んだときに無かったファイル）の問いを記録し、
//     警告のメッセージで知らせる。問いは安全な時点（askNotice）で出す
//
// 一度知らせた変更は、ファイルがまた変わるまで知らせない（Vim と同じ）。ただし問いで読み込まなかった変更は、
// :w の確認（writeConflict）では変わったままに扱う（Vim 9.1 で、W11 に OK と答えた後の :w は警告を出す）。
//
// req: FR-08
func (e *Editor) checkFile(r fileResult) {
	bs, now := r.bs, r.now
	bs.seen = now
	name := e.shortName(bs)
	if !now.exists {
		e.message = ui.Message{Text: fmt.Sprintf("E211: ファイル \"%s\" はもう存在しません", name), Level: ui.MessageWarning}
		return
	}
	if !bs.buf.Modified() {
		if r.readErr != nil {
			e.showError(fmt.Errorf("%s を読めません: %w", name, r.readErr))
			return
		}
		if sameContent(bs.buf, r.b) {
			bs.disk = now
			return
		}
		if e.autoRead() {
			r.b.SetFixEndOfLine(bs.buf.FixEndOfLine())
			e.reload(bs, r.b)
			bs.setDisk(now)
			e.message = infoMessage(fmt.Sprintf("\"%s\" は外で変更されたので読み直しました", name))
			return
		}
	}
	switch {
	case bs.buf.Modified():
		bs.notice = fmt.Sprintf("W12: 警告: ファイル \"%s\" が変更され、バッファも変更されています", name)
	case !bs.disk.exists:
		bs.notice = fmt.Sprintf("W13: 警告: 編集を始めた後にファイル \"%s\" が作成されました", name)
	default:
		bs.notice = fmt.Sprintf("W11: 警告: 編集を始めた後にファイル \"%s\" が変更されました", name)
	}
	e.message = ui.Message{Text: bs.notice + " (:checktime で確かめる)", Level: ui.MessageWarning}
}

// askNotice は、記録した外での変更の問い（W11〜W13）を、出してよい時点（idle）なら 1 つ出す。出したら true を返す。
// 出してよい時点は、:checktime の後と、拡張機能のコマンドの後である（Vim が問いを出す :checktime とシェルのコマンドの後に当たる）。
// 定期的な確認の時点では出さない。利用者がノーマルモードのコマンドを打っている途中に割り込み、
// 続くキー（o や l）を答えとして取ってしまうためである（PR #149 のレビュー）。
// :w の前は、書き込むかの問い（askWrite）が同じ変更を問うので、ここでは出さない。
func (e *Editor) askNotice() bool {
	if !e.idle() {
		return false
	}
	for _, bs := range e.bufs {
		if bs.notice == "" {
			continue
		}
		text := bs.notice
		bs.notice = ""
		e.prompt = &prompt{
			text: text + " [O]K, (L)oad File:",
			answer: func(k editcmd.Key) bool {
				switch k {
				case editcmd.Rune('l'), editcmd.Rune('L'), editcmd.Rune('a'), editcmd.Rune('A'):
					// a（Vim の Load File (a)nd Options）は、読み直すオプションが無いので l と同じにする。
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
	return false
}

// checkTime は、:checktime を行う。記録した問いがあれば出し、無ければ外での変更をすぐに確かめ、見つけた問いを出す。
//
// req: FR-08
func (e *Editor) checkTime() {
	if e.askNotice() {
		return
	}
	if e.checking && time.Since(e.checkStart) >= e.stallTimeout() {
		// 前の確認が戻らない（stat が固まっている）。黙って待たずに知らせる。終われば、その結果で問う。
		e.checkAsk = true
		e.warnStalled()
		return
	}
	e.startCheck(true)
}

// checkStallTimeout は、外での変更の確認が終わらないと知らせるまでの時間（仮）である。
// NFS のように stat が戻らないファイルシステムで、定期的な確認と :checktime が黙って止まって見えないようにする。
const checkStallTimeout = 5 * time.Second

func (e *Editor) stallTimeout() time.Duration {
	if e.checkStall > 0 {
		return e.checkStall
	}
	return checkStallTimeout
}

// checkStalled は、定期的な確認の時点で、前の確認が期限を過ぎても終わっていなければ、1 回だけ知らせる。知らせたら true を返す。
// 固まった goroutine の上に新しい確認を重ねても同じところで止まるので、新しくは始めず、終わるのを待つ。
func (e *Editor) checkStalled() bool {
	if !e.checking || e.checkWarned || time.Since(e.checkStart) < e.stallTimeout() {
		return false
	}
	e.warnStalled()
	return true
}

// warnStalled は、外での変更の確認が終わらないことを知らせる。
func (e *Editor) warnStalled() {
	e.checkWarned = true
	e.message = ui.Message{
		Text:  fmt.Sprintf("警告: 外での変更の確認が %d 秒たっても終わりません（ファイルシステムが応答していない可能性があります）", int(time.Since(e.checkStart).Seconds())),
		Level: ui.MessageWarning,
	}
}

// shortName は、問いと警告に出すバッファの名前を返す。作業ディレクトリの下のファイルなら相対パスにする。
// 問いの答え方（[O]K, (L)oad File:）を行の末尾に置くので、絶対パスで行の幅を超えて答え方が見えなくなるのを避けるためである（Vim も開いたときの名前で出す）。
func (e *Editor) shortName(bs *bufferState) string {
	if rel, err := filepath.Rel(e.workDir(), bs.path); err == nil && filepath.IsLocal(rel) {
		return rel
	}
	return displayName(bs)
}

// sameContent は、バッファ a と b の内容（書き出したときのファイルの中身を決めるもの）が同じかを返す。
// 全文の文字列を作らずに、行ごとに比べる。
func sameContent(a, b *buffer.Buffer) bool {
	if a.LineCount() != b.LineCount() || a.HasTrailingNewline() != b.HasTrailingNewline() || a.NoLines() != b.NoLines() {
		return false
	}
	for i := range a.LineCount() {
		if a.Line(i) != b.Line(i) {
			return false
		}
	}
	return true
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
