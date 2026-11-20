package editor

import (
	"github.com/piplupflipper/vi-imitation/internal/option"
)

// 設定の項目の値（基本設計 7.3。FR-131〜FR-134）。
//
// 範囲がエディタ全体の項目の値は Editor.options に、バッファごとの項目の値は bufferState.options に持つ。
// :option.set と :set（internal/command/ex）は、バッファの State の Ex.Options（optionAccess）を通して読み書きする。
// どちらの Values を使うかは項目の範囲で決め、項目の名前では分けない。
// 範囲がエディタ全体の項目は、使う側が使うたびに今の値を読む。範囲がバッファごとの項目を変えたら、
// 項目の名前によらず、拡張機能へバッファの属性を知らせ直す（notifyBufferAttrs）。

// optionAccess は、バッファ bs で実行する Ex コマンドが、設定の値を読み書きする口（ex.Options）である。
// バッファごとの項目は bs の値を、エディタ全体の項目はエディタに 1 つの値を読み書きする。
type optionAccess struct {
	e  *Editor
	bs *bufferState
}

// values は、項目 d の値を持つ Values を返す。
func (o optionAccess) values(d option.Def) *option.Values {
	if d.Scope == option.ScopeBuffer {
		return &o.bs.options
	}
	return &o.e.options
}

// Get は、項目 d の今の値を返す。
//
// req: FR-132
func (o optionAccess) Get(d option.Def) option.Value {
	return o.values(d).Get(d.Name)
}

// Set は、項目 d の値を v にする。範囲がバッファごとの項目なら、拡張機能へ bs の属性を知らせ直す（FR-134）。
//
// req: FR-132, FR-134
func (o optionAccess) Set(d option.Def, v option.Value) error {
	if err := o.values(d).Set(d, v); err != nil {
		return err
	}
	if d.Scope == option.ScopeBuffer {
		o.e.notifyBufferAttrs(o.bs)
	}
	return nil
}

// Revision は、項目 d に値を設定した回数を返す（option.Values.Revision。:search.clearHighlight が覚える。FR-138）。
//
// req: FR-138
func (o optionAccess) Revision(d option.Def) uint64 {
	return o.values(d).Revision(d.Name)
}

// notifyBufferAttrs は、拡張機能へ bs の属性（パスとファイルの種類）を知らせ直す（exthost.Host.UpdateBuffer）。
// 稼働中の拡張機能へ buffer/open を送り直し、新しいファイルの種類の契機が成り立つ未起動の拡張機能を起動する（FR-14）。
// 別名での保存と、バッファごとの設定の項目の変更で呼ぶ。属性が変わっていなければ、exthost は何も送らない。
//
// req: FR-134
func (e *Editor) notifyBufferAttrs(bs *bufferState) {
	if err := e.host.UpdateBuffer(bs.id, bs.path, bs.fileType()); err != nil {
		e.log.Error("buffer/open を送り直せない", "buffer", bs.id, "err", err)
	}
}

// fileType は、bs のファイルの種類（設定の項目 filetype の値）を返す。
func (bs *bufferState) fileType() string {
	return bs.options.Get(option.FileType).String()
}

// setFileType は、バッファを開くとき（と、名前の無いバッファに名前を付けたとき）に決めたファイルの種類を bs に入れる。
// 拡張機能へは知らせない（呼び出し側が buffer/open か notifyBufferAttrs で知らせる）。
func (bs *bufferState) setFileType(ft string) {
	d, _ := option.Lookup(option.FileType)
	// 型は表の型と同じなので、誤りにならない。
	_ = bs.options.Set(d, option.StringValue(ft))
}

// resetBufferOptions は、bs のバッファごとの設定の項目を、ファイルを開いたときの初めの値に戻す（FR-134）。
// ファイルから開き直したとき（:e と :e!）と、名前の無いバッファを開いたファイルのバッファとして使い回すときに呼ぶ。
// 項目の名前によらず、それまでの値をすべて捨て、ファイルを開くときに決まる値（bs.path からのファイルの種類の判定）だけを入れ直す。
// 拡張機能へは知らせない（呼び出し側が buffer/open か notifyBufferAttrs で知らせる）。
//
// req: FR-134
func (e *Editor) resetBufferOptions(bs *bufferState) {
	bs.options = option.Values{}
	bs.setFileType(e.detectFileType(bs.path))
}

// editorOptions は、設定ファイルから読んだエディタ全体の項目の値 vals から、エディタの初めの値を作る。
// 表に無い名前と、範囲がエディタ全体でない項目と、型の違う値は使わない（internal/config が読んだ値なので、ふつうは無い）。
func editorOptions(vals map[string]option.Value) option.Values {
	var vs option.Values
	for name, v := range vals {
		if d, ok := option.Lookup(name); ok && d.Scope == option.ScopeEditor {
			_ = vs.Set(d, v) // 型の違う値は使わない
		}
	}
	return vs
}
