package editor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

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
	}
	return fmt.Errorf("未知の動作 %d", a.Kind)
}

// write は、:w を行う。
// 引数が無いか、バッファのファイルと同じなら保存の流れ（基本設計 6.7）を通す。
// 名前の無いバッファに名前を付けて書くと、そのファイルのバッファにする（Vim と同じ）。
// 別のファイルへ書くときは、保存の流れを通さずに書き出すだけにし、バッファは保存済みにしない。
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
	b, err := buffer.ReadFile(bs.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		b = buffer.New("")
	case err != nil:
		return fmt.Errorf("%s を読めません: %w", filepath.Base(bs.path), err)
	}
	e.reload(bs, b)
	return nil
}

// reload は、バッファ bs の内容を、読み直した b の内容にする。バッファの番号は変えない（Vim の :e と同じ）。
//
// 末尾の改行の有無が同じなら、全体を置き換える 1 つの編集として適用する。
// バージョン番号は進み続け、拡張機能へは差分（buffer/change）で届き、アンドゥで読み直す前へ戻れる（Vim の 'undoreload' と同じ）。
// 末尾の改行の有無が違えば（編集では変えられないので）、同じ番号で buffer/close と buffer/open を送り直し、
// バージョン番号は 0 から数え直す。このとき、そのバッファの診断と印と色は消す。
func (e *Editor) reload(bs *bufferState, b *buffer.Buffer) {
	cur := bs.buf
	cursor := bs.state.Cursor()
	b.SetFixEndOfLine(cur.FixEndOfLine())
	if b.HasTrailingNewline() == cur.HasTrailingNewline() {
		if b.Text() != cur.Text() {
			last := cur.LineCount() - 1
			whole := buffer.Range{End: buffer.Position{Line: last, Byte: len(cur.Line(last))}}
			ch, err := cur.Apply(buffer.Transaction{BaseVersion: cur.Version(), Edits: []buffer.Edit{{Range: whole, Text: b.Text()}}})
			if err != nil {
				e.showError(err)
				return
			}
			e.commitChange(bs, ch)
		}
		if b.NoLines() {
			// 読み直したファイルが空なら、Vim と同じく行の無いバッファにする（書き出すと空のファイルになる）。
			cur.MarkNoLines()
		}
		cur.MarkSaved()
		bs.state.SetCursor(cursor)
		return
	}
	if err := e.host.CloseBuffer(bs.id); err != nil {
		e.log.Error("buffer/close を送れない", "err", err)
	}
	regs := *bs.state.Registers()
	bs.buf = b
	bs.history = nil
	bs.decorations = map[string]decorationSet{}
	bs.decoView = nil
	bs.diagnostics = map[string][]ext.Diagnostic{}
	bs.signs = map[string][]ext.Sign{}
	e.initState(bs)
	*bs.state.Registers() = regs
	bs.state.SetCursor(cursor)
}
