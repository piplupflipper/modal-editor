package editor

import (
	"github.com/piplupflipper/vi-imitation/internal/option"
)

// 設定の項目の値（基本設計 7.3。FR-131〜FR-134、FR-163）。
//
// 範囲がエディタ全体の項目の値は Editor.options に持つ。
// 範囲がバッファごとの項目の値は、バッファごとに 2 つに分けて持つ。
//
//   - bufferState.layered: 初めの値の段（optionLayers）を順に当てた値。段の値が変わったら（バッファを開き直した、名前を付けた、
//     後で足す拡張機能の段の値が届いた）、作り直す（refreshOptionLayers）
//   - bufferState.options: 利用者が :option.set と :set で変えた値と、ファイルから判定して決める項目（filetype。option.Def.Detected）の値
//
// 読むときは、options に値があればそれを、無ければ layered の値を使う（bufferState.option）。
// このため、段を作り直しても、利用者が :set で変えた値はどの段よりも勝つ。
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

// values は、項目 d の値を書き込む Values を返す（バッファごとの項目は、利用者が変えた値の bufferState.options）。
func (o optionAccess) values(d option.Def) *option.Values {
	if d.Scope == option.ScopeBuffer {
		return &o.bs.options
	}
	return &o.e.options
}

// Get は、項目 d の今の値を返す。バッファごとの項目は、利用者が変えた値があればそれを、無ければ初めの値の段の値を返す。
//
// req: FR-132, FR-163
func (o optionAccess) Get(d option.Def) option.Value {
	if d.Scope == option.ScopeBuffer {
		return o.bs.option(d.Name)
	}
	return o.e.options.Get(d.Name)
}

// Set は、項目 d の値を v にする。範囲がバッファごとの項目なら、利用者が変えた値として持ち（初めの値の段より勝つ）、
// 拡張機能へ bs の属性を知らせ直す（FR-134）。
// 設定した結果、bs のファイルの種類が、初めの値の段を当てたときから変わっていれば、段を新しいファイルの種類で当て直す（applyOptionLayers。FR-163）。
// 項目の名前では分けず、ファイルの種類の値が変わったかで決める。
//
// 拡張機能へ buffer/open を送り直しても、拡張機能から設定の値を変える経路は無い。後で拡張機能から値を変える口を足すときも、
// その値は optionLayers の段に入り、ここで入れた値より負ける。
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

// option は、bs の範囲がバッファごとの項目 name の今の値を返す。利用者が変えた値（options）があればそれを、無ければ初めの値の段を
// 当てた値（layered）を返す。
func (bs *bufferState) option(name string) option.Value {
	if v, ok := bs.options.Lookup(name); ok {
		return v
	}
	return bs.layered.Get(name)
}

// fileType は、bs のファイルの種類（設定の項目 filetype の値）を返す。
func (bs *bufferState) fileType() string {
	return bs.option(option.FileType).String()
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

// renameBufferOptions は、名前の無いバッファに名前（bs.path）を付けたときに、.editorconfig を新しい名前で読み直す（FR-164）。
// ファイルの種類は呼び出し側が決めておく。名前からファイルの種類が決まって変わったなら、ファイルの種類を変えたとき（optionAccess.Set）と同じく
// 利用者が変えた値を捨てて初めの値の段を当て直す（applyOptionLayers）。変わらなければ、段を作り直すだけにし（refreshOptionLayers）、
// :set で変えた値を残す。Neovim 0.12.5 は :w ファイル では .editorconfig を読まず、:file と :saveas（BufFilePost）では読んで
// .editorconfig の項目を :set の値の上にも当てる（docs/vim-differences.md）。
// どちらも段の並びは optionLayers のとおりで、.editorconfig がどの段かをここでは前提にしない。
//
// req: FR-164
func (e *Editor) renameBufferOptions(bs *bufferState) {
	bs.editorConfig = e.readEditorConfig(bs.path)
	if bs.fileType() != bs.layeredType {
		e.applyOptionLayers(bs)
		return
	}
	e.refreshOptionLayers(bs)
}

// applyOptionLayers は、bs の範囲がバッファごとの項目（ファイルから判定して決める項目を除く）を、初めの値の段を順に当てた値にする
// （option パッケージの doc。基本設計 7.3。FR-163、FR-164）。利用者が :set で変えた値は捨てる。
// ファイルの種類（filetype）は bs の今の値を使い、変えない。
// 拡張機能へは知らせない（呼び出し側が buffer/open か notifyBufferAttrs で知らせる）。
//
// バッファを開いたとき（newBuffer）、ファイルから開き直したとき（resetBufferOptions）、ファイルの種類が変わったとき（optionAccess.Set と
// renameBufferOptions）に呼ぶ。項目の名前では分けない。
//
// req: FR-163, FR-164
func (e *Editor) applyOptionLayers(bs *bufferState) {
	var kept option.Values
	for _, d := range option.Defs() {
		if d.Scope != option.ScopeBuffer || !d.Detected {
			continue
		}
		if v, ok := bs.options.Lookup(d.Name); ok {
			_ = kept.Set(d, v) // 型は表の型と同じなので、誤りにならない
		}
	}
	bs.options = kept
	e.refreshOptionLayers(bs)
}

// refreshOptionLayers は、bs の初めの値の段を当てた値（bufferState.layered）を、e.layers の段を順に当てて作り直す。
// 利用者が :set で変えた値（bufferState.options）は変えないので、それはどの段の値よりも勝つ。
// 段の値が変わったとき（名前を付けて .editorconfig を読み直したとき。後で足す拡張機能の段の値が届いたとき）に呼ぶ。
//
// req: FR-163
func (e *Editor) refreshOptionLayers(bs *bufferState) {
	var vs option.Values
	for _, layer := range e.layers {
		for name, v := range layer(e, bs) {
			if d, ok := option.Lookup(name); ok && d.Scope == option.ScopeBuffer && !d.Detected {
				_ = vs.Set(d, v) // 型の違う値と範囲の外の値は使わない
			}
		}
	}
	bs.layered = vs
	bs.layeredType = bs.fileType()
}

// optionLayer は、初めの値の段の 1 つで、バッファ bs に当てる値（正規の名前から値への対応）を返す。
type optionLayer func(e *Editor, bs *bufferState) map[string]option.Value

// optionLayers は、バッファごとの項目の初めの値の段を、当てる順に返す（表の既定値の段は、何も入れないことで表す）。後の段の値が勝つ。
// 段の並びはここの 1 か所で決め、ほかの箇所は段の数と順を前提にしない（Editor.layers に写して使う）。
// 拡張機能から設定を変える口（#318 の「この上に何が載るか」）は、.editorconfig の後に段を足し、値が届いたら refreshOptionLayers を呼ぶ。
// 利用者が :set で変えた値は段に入らず、どの段よりも勝つ（bufferState.option）。
//
//  1. 設定ファイルの最上位の値（Config.Options）
//  2. 本体のファイルの種類ごとの既定（option.FileTypeDefaults）
//  3. 設定ファイルのファイルの種類ごとの値（Config.FileTypeOptions）
//  4. .editorconfig の値（editorConfigOptions）
func optionLayers() []optionLayer {
	return []optionLayer{
		func(e *Editor, _ *bufferState) map[string]option.Value { return e.cfg.Options },
		func(_ *Editor, bs *bufferState) map[string]option.Value {
			return option.FileTypeDefaults(bs.fileType())
		},
		func(e *Editor, bs *bufferState) map[string]option.Value { return e.cfg.FileTypeOptions[bs.fileType()] },
		func(e *Editor, bs *bufferState) map[string]option.Value {
			return e.editorConfigOptions(bs.editorConfig)
		},
	}
}

// scratchTabWidth は、名前もファイルの種類も無いバッファ（ホバーとヘルプのポップアップ）を開いたときのタブ文字の幅である。
// そのバッファの中身を窓の幅で折り返すとき（ui.WrapText）に、描くときと同じ幅で数えるために使う。
func (e *Editor) scratchTabWidth() int {
	var bs bufferState
	e.refreshOptionLayers(&bs)
	return bs.tabWidth()
}

// tabWidth は、bs のタブ文字の表示幅（設定の項目 tab.width の今の値。FR-161）を返す。
func (bs *bufferState) tabWidth() int {
	return bs.option(option.TabWidth).Int()
}

// editorOptions は、設定ファイルから読んだ項目の値 vals から、エディタの初めの値を作る。
// 表に無い名前と、範囲がエディタ全体でない項目と、型の違う値は使わない（範囲がバッファごとの項目は optionLayers の最初の段が使う）。
func editorOptions(vals map[string]option.Value) option.Values {
	var vs option.Values
	for name, v := range vals {
		if d, ok := option.Lookup(name); ok && d.Scope == option.ScopeEditor {
			_ = vs.Set(d, v) // 型の違う値は使わない
		}
	}
	return vs
}
