package editor

import (
	"github.com/piplupflipper/vi-imitation/internal/editorconfig"
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
// 設定した結果、bs のファイルの種類が、初めの値の段を当てたときから変わっていれば、段を新しいファイルの種類で当て直す（applyOptionLayers。FR-163）。
// 項目の名前では分けず、ファイルの種類の値が変わったかで決める。
//
// req: FR-132, FR-134, FR-163
func (o optionAccess) Set(d option.Def, v option.Value) error {
	if err := o.values(d).Set(d, v); err != nil {
		return err
	}
	if d.Scope == option.ScopeBuffer {
		if o.bs.fileType() != o.bs.layeredType {
			o.e.applyOptionLayers(o.bs)
		}
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
// 項目の名前によらず、それまでの値をすべて捨て、ファイルを開くときに決まる値（bs.path からのファイルの種類の判定）を入れ直し、
// .editorconfig を読み直して、初めの値の段を当てる（applyOptionLayers。FR-163、FR-164）。
// 拡張機能へは知らせない（呼び出し側が buffer/open か notifyBufferAttrs で知らせる）。
//
// req: FR-134, FR-164
func (e *Editor) resetBufferOptions(bs *bufferState) {
	bs.options = option.Values{}
	bs.setFileType(e.detectFileType(bs.path))
	bs.editorConfig = e.readEditorConfig(bs.path)
	e.applyOptionLayers(bs)
}

// renameBufferOptions は、名前の無いバッファに名前（bs.path）を付けたときに、.editorconfig を新しい名前で読み直して当てる（FR-164）。
// ファイルの種類は呼び出し側が決めておく。名前からファイルの種類が決まって変わったなら、ファイルの種類を変えたとき（optionAccess.Set）と同じく
// 初めの値の段をすべて当て直す。変わらなければ、.editorconfig の段だけを今の値の上に当て、:set で変えたほかの項目の値を残す
// （Neovim 0.12.5 も、名前を付けたとき（BufFilePost）に .editorconfig の項目だけを当てる）。
//
// req: FR-164
func (e *Editor) renameBufferOptions(bs *bufferState) {
	bs.editorConfig = e.readEditorConfig(bs.path)
	if bs.fileType() != bs.layeredType {
		e.applyOptionLayers(bs)
		return
	}
	for name, v := range e.editorConfigOptions(bs.editorConfig) {
		if d, ok := option.Lookup(name); ok {
			_ = bs.options.Set(d, v) // editorConfigOptions が検査した値なので、誤りにならない
		}
	}
}

// applyOptionLayers は、bs の範囲がバッファごとの項目（ファイルから判定して決める項目を除く）を、初めの値の段を順に当てた値にする
// （option パッケージの doc。基本設計 7.3。FR-163、FR-164）。それまでの値（:set で変えた値を含む）は捨てる。
// 段は optionLayers の順で、後の段の値が勝つ。ファイルの種類（filetype）は bs の今の値を使い、変えない。
// 拡張機能へは知らせない（呼び出し側が buffer/open か notifyBufferAttrs で知らせる）。
//
// バッファを開いたとき（newBuffer）、ファイルから開き直したとき（resetBufferOptions）、名前を付けたとき（renameBufferOptions）、
// ファイルの種類が変わったとき（optionAccess.Set）に呼ぶ。項目の名前では分けない。
//
// req: FR-163, FR-164
func (e *Editor) applyOptionLayers(bs *bufferState) {
	ft := bs.fileType()
	var vs option.Values
	for _, d := range option.Defs() {
		if d.Scope != option.ScopeBuffer || !d.Detected {
			continue
		}
		_ = vs.Set(d, bs.options.Get(d.Name)) // 型は表の型と同じなので、誤りにならない
	}
	for _, layer := range e.optionLayers(ft, bs.editorConfig) {
		for name, v := range layer {
			if d, ok := option.Lookup(name); ok && d.Scope == option.ScopeBuffer && !d.Detected {
				_ = vs.Set(d, v) // 型の違う値と範囲の外の値は使わない
			}
		}
	}
	bs.options = vs
	bs.layeredType = ft
}

// optionLayers は、ファイルの種類 ft のバッファの初めの値の段を、当てる順に返す（表の既定値の段は、何も入れないことで表す）。
// 拡張機能から設定を変える口（#318 の「この上に何が載るか」）は、.editorconfig の後に段を足す。
//
//  1. 設定ファイルの最上位の値（Config.Options）
//  2. 本体のファイルの種類ごとの既定（option.FileTypeDefaults）
//  3. 設定ファイルのファイルの種類ごとの値（Config.FileTypeOptions）
//  4. .editorconfig の値（editorConfigOptions）
func (e *Editor) optionLayers(ft string, ec editorconfig.Properties) []map[string]option.Value {
	return []map[string]option.Value{
		e.cfg.Options,
		option.FileTypeDefaults(ft),
		e.cfg.FileTypeOptions[ft],
		e.editorConfigOptions(ec),
	}
}

// scratchTabWidth は、名前もファイルの種類も無いバッファ（ホバーとヘルプのポップアップ）を開いたときのタブ文字の幅である。
// そのバッファの中身を窓の幅で折り返すとき（ui.WrapText）に、描くときと同じ幅で数えるために使う。
func (e *Editor) scratchTabWidth() int {
	var vs option.Values
	d, _ := option.Lookup(option.TabWidth)
	for _, layer := range e.optionLayers("", nil) {
		if v, ok := layer[option.TabWidth]; ok {
			_ = vs.Set(d, v)
		}
	}
	return vs.Get(option.TabWidth).Int()
}

// tabWidth は、bs のタブ文字の表示幅（設定の項目 tab.width の今の値。FR-161）を返す。
func (bs *bufferState) tabWidth() int {
	return bs.options.Get(option.TabWidth).Int()
}

// editorOptions は、設定ファイルから読んだ項目の値 vals から、エディタの初めの値を作る。
// 表に無い名前と、範囲がエディタ全体でない項目と、型の違う値は使わない（範囲がバッファごとの項目は applyOptionLayers が使う）。
func editorOptions(vals map[string]option.Value) option.Values {
	var vs option.Values
	for name, v := range vals {
		if d, ok := option.Lookup(name); ok && d.Scope == option.ScopeEditor {
			_ = vs.Set(d, v) // 型の違う値は使わない
		}
	}
	return vs
}
