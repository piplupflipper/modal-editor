package editor

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/internal/buffer"
	"github.com/piplupflipper/modal-editor/internal/exthost"
)

// 拡張機能が作るファイルでないバッファ（buffer/create）と、拡張機能から始める見比べ（diff/start）（基本設計 5.3、6.8、6.9。FR-88、FR-99、FR-104）。
//
// git 連携の拡張機能（cmd/modal-editor-ext-git）は、HEAD の版を buffer/create で編集を禁止したファイルでないバッファにし、
// diff/start でファイルのバッファと見比べさせる。
//
// buffer/create で作ったバッファは、どのタブにもしない（:ls には出し、:b で開ける）。
// diff/start で、片方のバッファだけがタブなら、:diffsplit と同じくそのタブの client のペインを分割し、ファイルを渡さずに新しい編集の client を起動する。
// その client が繋いだら（attach）、名前の無い空のタブを作る代わりに、もう片方のバッファをタブにして（diffAdopt）、見比べを始める（diffAttached）。
// 新しい client は起動の引数で見分けられないので、待っている間（diffPendingTimeout）に、ファイルを渡さずに繋いだ最初の編集の client を使う（仮）。

// bufferCreateRequest は、buffer/create を処理する。ファイルでないバッファを作り、稼働中の拡張機能へ buffer/open を送ってから応答する。
// Writable が false なら（既定）、編集を禁止した状態で始める（FR-88）。
//
// req: FR-88
func (e *Editor) bufferCreateRequest(ev *exthost.BufferCreateRequest) {
	p := ev.Params
	b := buffer.New(p.Text)
	b.SetFixEndOfLine(e.fixEOL)
	b.SetReadOnly(!p.Writable)
	bs := e.newBuffer(b, "", p.FileType)
	bs.title = p.Title
	_ = ev.Reply(ext.BufferCreateResult{Buffer: bs.id, Version: ext.Version(b.Version())})
}

// diffStartRequest は、diff/start を処理する。始められなければ、画面に出せる日本語の誤りで応答する。
func (e *Editor) diffStartRequest(ev *exthost.DiffStartRequest) {
	res, err := e.diffStart(ev.Params)
	if err != nil {
		_ = ev.ReplyError(err)
		return
	}
	_ = ev.Reply(res)
}

// errNoTab は、diff/start の 2 つのバッファのどちらもタブでないことを表す。
var errNoTab = errors.New("見比べる 2 つのバッファのどちらもタブではありません")

// diffStart は、diff/start の params の 2 つのバッファの見比べを始める（ext.DiffStartParams の doc の場合分け）。
//
// req: FR-99
func (e *Editor) diffStart(p ext.DiffStartParams) (ext.DiffStartResult, error) {
	a, b := e.findBuffer(p.A), e.findBuffer(p.B)
	for i, bs := range []*bufferState{a, b} {
		id := []ext.BufferID{p.A, p.B}[i]
		if bs == nil {
			return ext.DiffStartResult{}, invalidParams("unknown buffer %d", id)
		}
		if bs.popup {
			return ext.DiffStartResult{}, invalidParams("buffer %d is a popup buffer", id)
		}
	}
	switch {
	case a.owner != nil && b.owner != nil:
		if a.owner == b.owner {
			return ext.DiffStartResult{}, fmt.Errorf("%s と %s は同じ client のタブです（見比べるには別の client のタブにしてください）", e.bufferName(a), e.bufferName(b))
		}
		e.startDiff(a, b)
		return ext.DiffStartResult{}, nil
	case a.owner == nil && b.owner == nil:
		return ext.DiffStartResult{}, errNoTab
	}
	from, other, swap := a, b, false
	if a.owner == nil {
		from, other, swap = b, a, true
	}
	c := from.owner
	if err := e.splitForDiff(c, "", &diffPending{from: from, buf: other, swap: swap, at: time.Now()}); err != nil {
		return ext.DiffStartResult{}, err
	}
	text := fmt.Sprintf("%s を別のペインで開いて見比べます", e.bufferName(other))
	e.withView(c, func() { e.message = infoMessage(text) })
	return ext.DiffStartResult{Split: true}, nil
}

// diffAdopt は、繋ぐ client（読み込んでいる client。まだタブが無い）が、diff/start で分割したペインの client なら、
// 待っているバッファをその client のタブにする。attach が startClient の前に呼ぶ。files は client の起動の引数のファイルである。
func (e *Editor) diffAdopt(files []string) {
	p := e.diff.pending
	if p == nil || p.buf == nil || len(files) != 0 || e.cur != nil {
		return
	}
	if time.Since(p.at) > diffPendingTimeout || p.buf.owner != nil || !slices.Contains(e.bufs, p.buf) {
		e.diff.pending = nil
		return
	}
	e.enter(p.buf)
}
