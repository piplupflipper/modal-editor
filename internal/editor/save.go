package editor

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/internal/buffer"
	"github.com/piplupflipper/modal-editor/internal/command/ex"
	"github.com/piplupflipper/modal-editor/internal/exthost"
	"github.com/piplupflipper/modal-editor/internal/ui"
)

// 画面に出す誤り（Vim のエラー番号を先頭に付ける）。
var (
	errNoFileName  = errors.New("E32: ファイル名がありません")
	errFileExists  = errors.New("E13: ファイルが存在します (! を付けると上書きします)")
	errOtherBuffer = errors.New("E162: 他のバッファに保存していない変更があります (! を付けると破棄します)")
)

// displayName は、バッファの名前（パス、無ければ "[No Name]"）を返す。
func displayName(bs *bufferState) string {
	if bs.path == "" {
		return "[No Name]"
	}
	return bs.path
}

// doAction は、Ex コマンドが求めるエディタの動作を 1 つ行う（:w、:q、:e）。
func (e *Editor) doAction(bs *bufferState, a ex.Action) error {
	switch a.Kind {
	case ex.ActionWrite:
		return e.write(bs, a)
	case ex.ActionQuit:
		if !a.Force {
			for _, o := range e.bufs {
				if o != bs && o.buf.Modified() {
					return errOtherBuffer
				}
			}
		}
		e.quit = true
		return nil
	case ex.ActionEdit:
		return e.edit(bs, a)
	case ex.ActionOutput:
		return e.outputAction(a)
	case ex.ActionCheckTime:
		e.checkTime()
		return nil
	case ex.ActionExtCommand:
		// command.State が問い合わせるので、Result.Actions には入らない。来たら内部の誤りである。
		return fmt.Errorf("内部の誤り: 拡張機能のコマンド %s が本体の動作として渡された", a.Command)
	}
	return fmt.Errorf("未知の動作 %d", a.Kind)
}

// write は、:w を行う。
// 引数が無いか、バッファのファイルと同じなら保存の流れ（基本設計 6.7）を通す。
// 名前の無いバッファに名前を付けて書くと、そのファイルのバッファにする（Vim と同じ）。
// 別のファイルへ書くときは、保存の流れを通さずに書き出すだけにし、バッファは保存済みにしない。
// バッファのファイルへ書くときは、! が無ければ、読み込んだ後にファイルが外で変わっていないかを先に確かめる（writeConflict）。
// 変わっていれば errChangedOnDisk を返し、呼び出し側が書き込むかを問う。
//
// req: FR-08
func (e *Editor) write(bs *bufferState, a ex.Action) error {
	path := bs.path
	if a.File != "" {
		path = e.absPath(a.File)
	}
	if path == "" {
		return errNoFileName
	}
	if path != bs.path {
		if _, err := os.Stat(path); err == nil && !a.Force {
			return errFileExists
		}
		if bs.path != "" {
			if err := bs.buf.WriteFile(path); err != nil {
				return fmt.Errorf("E212: %s を書けません: %w", path, err)
			}
			e.message = infoMessage(fmt.Sprintf("\"%s\" を書き込みました", path))
			return nil
		}
		bs.path = path
		bs.fileType = e.detectFileType(path)
		if err := e.host.UpdateBuffer(bs.id, bs.path, bs.fileType); err != nil {
			e.log.Error("buffer/open を送り直せない", "err", err)
		}
	}
	if !a.Force {
		if err := writeConflict(bs); err != nil {
			return err
		}
	}
	return e.save(bs, path, false)
}

// save は、バッファを保存の流れ（基本設計 6.7）で path へ書き出す。path はバッファのファイルである。
//
//  1. 宣言ファイルの saveHook が true の稼働中の拡張機能へ、1 つずつ順に buffer/willSave を送る（skipHooks なら飛ばす）
//  2. 応答のトランザクションの BaseVersion が現在と一致すれば適用し、次の拡張機能には適用した後のバージョン番号で問い合わせる
//  3. 期限を超えた拡張機能は待たずに次へ進み、1 行のメッセージで知らせる（exthost が $/cancel を送る）
//  4. 書き出し、稼働中の拡張機能すべてへ buffer/didSave を送る
//
// 応答を待つ間は入れ子の待ち（wait）なので、画面の描画は続き、キー入力と拡張機能からのリクエストは待ちが終わってから処理する。
func (e *Editor) save(bs *bufferState, path string, skipHooks bool) error {
	var notes []string
	if !skipHooks {
		for _, id := range e.host.SaveHookExtensions() {
			e.tagSeq++
			tag := queryTag(e.tagSeq)
			q, err := e.host.SendWillSave(id, ext.BufferWillSaveParams{Buffer: bs.id, Version: ext.Version(bs.buf.Version())}, exthost.SendOptions{Tag: tag})
			if err != nil {
				continue
			}
			r := e.wait(q, tag)
			var res ext.BufferWillSaveResult
			if err := r.Decode(&res); err != nil {
				notes = append(notes, fmt.Sprintf("拡張機能 %s の保存の前の処理が失敗しました: %v", id, err))
				continue
			}
			if res.Transaction == nil {
				continue
			}
			if _, err := e.applyExternal(bs, fromExtTx(*res.Transaction)); err != nil {
				notes = append(notes, fmt.Sprintf("拡張機能 %s の保存の前の編集を適用できません: %v", id, err))
			}
		}
	}
	if err := bs.buf.WriteFile(path); err != nil {
		return fmt.Errorf("E212: %s を書けません: %w", path, err)
	}
	if st, err := statFile(path); err == nil {
		bs.setDisk(st)
	}
	bs.buf.MarkSaved()
	if err := e.host.DidSave(ext.BufferDidSaveParams{Buffer: bs.id, Version: ext.Version(bs.buf.Version()), Path: path}); err != nil {
		e.log.Error("buffer/didSave を送れない", "err", err)
	}
	if len(notes) > 0 {
		e.message = ui.Message{Text: notes[0], Level: ui.MessageWarning}
	} else {
		e.message = infoMessage(fmt.Sprintf("\"%s\" を書き込みました", path))
	}
	return nil
}

// edit は、:e を行う。引数が無ければ、バッファのファイルを読み直す（未保存の変更の確認は ex が済ませている）。
func (e *Editor) edit(bs *bufferState, a ex.Action) error {
	if a.File != "" {
		_, err := e.openFile(a.File, true)
		return err
	}
	if bs.path == "" {
		return errNoFileName
	}
	return e.reloadFile(bs)
}

// reload は、バッファ bs の内容を、読み直した b の内容にする。バッファの番号は変えない（Vim の :e と同じ）。
//
// 読み直しは、前後の共通の行を除いた、変わった行の範囲だけを置き換える 1 つの編集（reloadEdits）として適用する（#143）。
// アンドゥの単位と拡張機能へ送る差分（buffer/change）の大きさは変わった行の数に比例し、変わっていない行の印と診断は動かない。
// 末尾の改行の有無の記録も同じ単位で変える（buffer.Buffer.ApplyReload）ので、アンドゥ 1 回で、末尾の改行も含めて読み直す前へ戻れる
// （Vim 9.1 の 'undoreload' の範囲の読み直しと同じ）。
// 外での変更を問わずに読み直す（autoread）ときに毎秒呼ばれても、ログの追記のような変更ならアンドゥの履歴は追記した行の分しか増えない。
func (e *Editor) reload(bs *bufferState, b *buffer.Buffer) {
	cur := bs.buf
	cursor := bs.state.Cursor()
	ch, err := cur.ApplyReload(buffer.Transaction{BaseVersion: cur.Version(), Edits: reloadEdits(cur, b)}, b.HasTrailingNewline())
	if err != nil {
		e.showError(err)
		return
	}
	if ch.Before != ch.After {
		cur.SetUndoCursor(cursor)
		e.commitChange(bs, ch)
	}
	// 読み直したファイルが空なら、Vim と同じく行の無いバッファにする（書き出すと空のファイルになる）。
	// 空の行 1 つ（"\n"）なら、テキストが同じでも行の無いバッファの記録を外す。
	if b.NoLines() {
		cur.MarkNoLines()
	} else {
		cur.MarkHasLines()
	}
	e.syncFinalNewline(bs)
	cur.MarkSaved()
	bs.state.SetCursor(cursor)
}

// reloadEdits は、バッファ cur の行を、読み直した b の行にする最小の編集を返す。
// 先頭と末尾から同じ行を数えて除き、残った行の範囲を 1 つの編集で置き換える。行が同じなら空を返す。
func reloadEdits(cur, b *buffer.Buffer) []buffer.Edit {
	n, m := cur.LineCount(), b.LineCount()
	p := 0
	for p < n && p < m && cur.Line(p) == b.Line(p) {
		p++
	}
	s := 0
	for s < n-p && s < m-p && cur.Line(n-1-s) == b.Line(m-1-s) {
		s++
	}
	if n == m && p == n {
		return nil
	}
	lines := make([]string, 0, m-p-s)
	for i := p; i < m-s; i++ {
		lines = append(lines, b.Line(i))
	}
	text := strings.Join(lines, "\n")
	endOf := func(i int) buffer.Position { return buffer.Position{Line: i, Byte: len(cur.Line(i))} }
	switch {
	case n-s > p && m-s > p:
		// 両方に違う行がある: cur の p〜n-s-1 行を置き換える。
		return []buffer.Edit{{Range: buffer.Range{Start: buffer.Position{Line: p}, End: endOf(n - s - 1)}, Text: text}}
	case m-s > p && p < n:
		// 行を足すだけ（p 行目の前）。
		at := buffer.Position{Line: p}
		return []buffer.Edit{{Range: buffer.Range{Start: at, End: at}, Text: text + "\n"}}
	case m-s > p:
		// 末尾に行を足すだけ。
		at := endOf(n - 1)
		return []buffer.Edit{{Range: buffer.Range{Start: at, End: at}, Text: "\n" + text}}
	case n-s < n:
		// 行を消すだけ（後ろに行が残る）。
		return []buffer.Edit{{Range: buffer.Range{Start: buffer.Position{Line: p}, End: buffer.Position{Line: n - s}}}}
	default:
		// 末尾の行を消すだけ（p > 0。行は少なくとも 1 つ残る）。
		return []buffer.Edit{{Range: buffer.Range{Start: endOf(p - 1), End: endOf(n - 1)}}}
	}
}
