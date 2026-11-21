package editor

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/piplupflipper/vi-imitation/ext"
	"github.com/piplupflipper/vi-imitation/internal/exthost"
	"github.com/piplupflipper/vi-imitation/internal/option"
)

// 設定の項目の値（基本設計 7.3。FR-131〜FR-134、FR-163）。
//
// 範囲がエディタ全体の項目の値は Editor.options に持つ。拡張機能が option/set で入れた値は Editor.extOptions に分けて持ち、
// 利用者が :set で変えていない項目（Editor.userOptions に無い項目）だけ、Editor.options に写す（applyEditorOption。FR-191）。
// 範囲がバッファごとの項目の値は、バッファごとに 2 つに分けて持つ。
//
//   - bufferState.layered: 初めの値の段（optionLayers）を順に当てた値。段の値が変わったら（バッファを開き直した、名前を付けた、
//     拡張機能の段の値が option/set で届いた）、作り直す（refreshOptionLayers）
//   - bufferState.options: 利用者が :option.set と :set で変えた値と、ファイルから判定して決める項目（filetype。option.Def.Detected）の値
//
// 読むときは、options に値があればそれを、無ければ layered の値を使う（bufferState.option）。
// このため、段を作り直しても、利用者が :set で変えた値はどの段よりも勝つ。
// ただし、ファイルの種類が変わったときは、新しい種類の段が値を持つ項目だけ :set の値を隠し（bufferState.masked）、段の値を使う
// （changeFileTypeOptions。Neovim の ftplugin が触る項目だけを変えるのと同じ）。
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
// 入れた項目は、ファイルの種類が変わって隠していた（bufferState.masked）なら、隠すのをやめる。
// 設定した結果、bs のファイルの種類が、初めの値の段を当てたときから変わっていれば、段を新しいファイルの種類で当て直す（changeFileTypeOptions。FR-163）。
// 項目の名前では分けず、ファイルの種類の値が変わったかで決める。
//
// 拡張機能へ buffer/open を送り直すと、拡張機能が option/set で値を送り直すことがあるが、その値は optionLayers の最後の段に入り、
// ここで入れた値より負ける（FR-190）。範囲がエディタ全体の項目は、利用者が変えた項目として覚え（Editor.userOptions）、
// 拡張機能の値を当てない（FR-191）。
//
// req: FR-132, FR-134, FR-163, FR-191
func (o optionAccess) Set(d option.Def, v option.Value) error {
	if err := o.values(d).Set(d, v); err != nil {
		return err
	}
	if d.Scope == option.ScopeEditor {
		if o.e.userOptions == nil {
			o.e.userOptions = map[string]bool{}
		}
		o.e.userOptions[d.Name] = true
	}
	if d.Scope == option.ScopeBuffer {
		delete(o.bs.masked, d.Name)
		o.bs.extMask.userSet(d.Name)
		if o.bs.fileType() != o.bs.layeredType {
			o.e.changeFileTypeOptions(o.bs)
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
// ファイルの種類が変わって、新しい種類の段が値を持つ項目（masked）は、利用者が変えた値を隠し、段の値を返す。
func (bs *bufferState) option(name string) option.Value {
	if v, ok := bs.options.Lookup(name); ok && !bs.masked[name] && bs.extMask.names[name] == "" {
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
// changeFileTypeOptions で段を当て直す。変わらなければ、段を作り直すだけにする（refreshOptionLayers）。
// Neovim 0.12.5 は :w ファイル では .editorconfig を読まず、:file と :saveas（BufFilePost）では読んで
// .editorconfig の項目を :set の値の上にも当てる（docs/vim-differences.md）。
// どちらも段の並びは optionLayers のとおりで、.editorconfig がどの段かをここでは前提にしない。
//
// req: FR-164
func (e *Editor) renameBufferOptions(bs *bufferState) {
	bs.editorConfig = e.readEditorConfig(bs.path)
	if bs.fileType() != bs.layeredType {
		e.changeFileTypeOptions(bs)
		return
	}
	e.refreshOptionLayers(bs)
}

// changeFileTypeOptions は、bs のファイルの種類が変わったとき（:set filetype=、:w ファイル で種類の決まる名前を付けたとき）に、
// 段を新しい種類で作り直し、新しい種類の段（optionLayer.perType）が値を持つ項目だけ、利用者が :set で変えた値を隠す（bufferState.masked）。
// ほかの項目の :set の値は残す。前の種類で隠していた項目は、新しい種類の段が値を持たなければ、:set の値に戻す。
// 隠した項目を後で :set で変えると、その値を使う（optionAccess.Set）。
//
// Neovim 0.12.5 の FileType と同じにした。ftplugin は触る項目だけを setlocal で変え、前の種類の ftplugin が変えた項目は
// b:undo_ftplugin で :set の値（全体の値）に戻す（2026-11-21 に、:set sw=3 et ts=5 の後の :w n.c は 3 つとも残し、:w n.go と
// :w Makefile は et と sw だけを go と make の値にし、:set ft=go の後の :set ft=c は et=1 sw=3 に戻ることを確かめた）。
// 項目の名前では分けず、段が値を持つかで決める。
//
// req: FR-163
func (e *Editor) changeFileTypeOptions(bs *bufferState) {
	e.refreshOptionLayers(bs)
	bs.masked = nil
	// 拡張機能の段は、新しい種類の値が後から届く（buffer/open を受けてから送る）ので、届いたときに隠す（extMaskState）。
	bs.extMask = extMaskState{fileType: bs.fileType()}
	for _, layer := range e.layers {
		if !layer.perType {
			continue
		}
		for name := range layer.values(e, bs) {
			if d, ok := option.Lookup(name); ok && d.Scope == option.ScopeBuffer && !d.Detected {
				if bs.masked == nil {
					bs.masked = map[string]bool{}
				}
				bs.masked[name] = true
			}
		}
	}
}

// applyOptionLayers は、bs の範囲がバッファごとの項目（ファイルから判定して決める項目を除く）を、初めの値の段を順に当てた値にする
// （option パッケージの doc。基本設計 7.3。FR-163、FR-164）。利用者が :set で変えた値は捨てる。
// ファイルの種類（filetype）は bs の今の値を使い、変えない。
// 拡張機能へは知らせない（呼び出し側が buffer/open か notifyBufferAttrs で知らせる）。
//
// バッファを開いたとき（newBuffer）と、ファイルから開き直したとき（resetBufferOptions）に呼ぶ。項目の名前では分けない。
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
	bs.masked = nil
	bs.extMask = extMaskState{}
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
		for name, v := range layer.values(e, bs) {
			if d, ok := option.Lookup(name); ok && d.Scope == option.ScopeBuffer && !d.Detected {
				_ = vs.Set(d, v) // 型の違う値と範囲の外の値は使わない
			}
		}
	}
	bs.layered = vs
	bs.layeredType = bs.fileType()
}

// optionLayer は、初めの値の段の 1 つである。
type optionLayer struct {
	// values は、バッファ bs に当てる値（正規の名前から値への対応）を返す。
	values func(e *Editor, bs *bufferState) map[string]option.Value
	// perType は、ファイルの種類ごとの段（値が bs のファイルの種類で決まる段）であることを表す。ファイルの種類が変わったとき、
	// この段が値を持つ項目だけ、利用者が :set で変えた値を隠す（changeFileTypeOptions）。
	perType bool
}

// optionLayers は、バッファごとの項目の初めの値の段を、当てる順に返す（表の既定値の段は、何も入れないことで表す）。後の段の値が勝つ。
// 段の並びはここの 1 か所で決め、ほかの箇所は段の数と順を前提にしない（Editor.layers に写して使う）。
// 拡張機能から設定を変える口（option/set。#349）は、.editorconfig の後の段で、値が届いたら refreshOptionLayers を呼ぶ（optionSetRequest）。
// 利用者が :set で変えた値は段に入らず、どの段よりも勝つ（bufferState.option）。
//
//  1. 設定ファイルの最上位の値（Config.Options）
//  2. 本体のファイルの種類ごとの既定（option.FileTypeDefaults）
//  3. 設定ファイルのファイルの種類ごとの値（Config.FileTypeOptions）
//  4. .editorconfig の値（editorConfigOptions）
//  5. 拡張機能が option/set で入れた値（extLayerValues。FR-190）。値は拡張機能が後から送るので perType にはせず、ファイルの種類が
//     変わった後に届いた最初の値で、その値を持つ項目の :set の値を隠す（extMaskState）
func optionLayers() []optionLayer {
	return []optionLayer{
		{values: func(e *Editor, _ *bufferState) map[string]option.Value { return e.cfg.Options }},
		{values: func(_ *Editor, bs *bufferState) map[string]option.Value {
			return option.FileTypeDefaults(bs.fileType())
		}, perType: true},
		{values: func(e *Editor, bs *bufferState) map[string]option.Value {
			return e.cfg.FileTypeOptions[bs.fileType()]
		}, perType: true},
		{values: func(e *Editor, bs *bufferState) map[string]option.Value {
			return e.editorConfigOptions(bs.editorConfig)
		}},
		{values: func(e *Editor, bs *bufferState) map[string]option.Value {
			return e.extLayerValues(bs.extOptions)
		}},
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

// 拡張機能から設定を変える口（option/set）と読む口（option/get）（ext.MethodOptionSet の doc。基本設計 5.3、7.3。#349）。
//
// 拡張機能の値は拡張機能の ID ごとに分けて持ち（バッファごとの項目は bufferState.extOptions、エディタ全体の項目は Editor.extOptions）、
// 同じ項目に複数の拡張機能が値を入れたら、宣言ファイルを先に読んだ拡張機能（exthost.Host.Rank の小さいもの）の値を使う
// （本体の操作の提供者の選び方と同じ順。基本設計 5.5）。届いた順にしないのは、拡張機能の起動の速さで値が変わらないようにするためである。
// どちらの範囲も、利用者が :set で変えた値には勝たない。拡張機能が止まったら、その拡張機能の値をすべて捨てる（clearExtOptions）。

// optionSetRequest は、option/set を処理する。名前と値をすべて確かめてから入れ、1 つでも誤りがあればどの値も入れずに誤りで応答する。
// 確かめるのは、表にある名前か、ファイルから判定して決める項目（filetype）でないか、範囲が params の Buffer の有無と合うか、
// 値が項目の型の JSON の値で、:option.set と同じ検査（option.Def.Check）を通るかである。
// バッファごとの項目は、そのバッファの初めの値の段を作り直す（refreshOptionLayers）。:set で変えた値は変えない。
// 拡張機能へバッファの属性を知らせ直さない（拡張機能はファイルの種類を変えられず、パスとファイルの種類は変わらないため）。
//
// req: FR-190, FR-191
func (e *Editor) optionSetRequest(ev *exthost.OptionSetRequest) {
	var bs *bufferState
	if ev.Params.Buffer != 0 {
		if bs = e.findBuffer(ev.Params.Buffer); bs == nil {
			_ = ev.ReplyError(invalidParams("option/set: unknown buffer %d", ev.Params.Buffer))
			return
		}
	}
	set := map[string]option.Value{}
	var unset []string
	for _, name := range slices.Sorted(maps.Keys(ev.Params.Options)) {
		d, err := extOptionDef(name, bs != nil)
		if err != nil {
			_ = ev.ReplyError(invalidParams("option/set: %v", err))
			return
		}
		raw := ev.Params.Options[name]
		if string(bytes.TrimSpace(raw)) == "null" {
			unset = append(unset, name)
			continue
		}
		v, err := d.ParseJSON(raw)
		if err != nil {
			_ = ev.ReplyError(invalidParams("option/set: %s: %v", name, err))
			return
		}
		set[name] = v
	}
	if bs != nil {
		bs.extOptions = mergeExtValues(bs.extOptions, ev.Extension, set, unset)
		bs.extMask.received(ev.Extension, ev.Params.FileType, bs.fileType(), set, unset)
		e.refreshOptionLayers(bs)
	} else {
		e.extOptions = mergeExtValues(e.extOptions, ev.Extension, set, unset)
		for _, name := range slices.Concat(slices.Collect(maps.Keys(set)), unset) {
			e.applyEditorOption(name)
		}
	}
	_ = ev.Reply()
}

// extMaskState は、ファイルの種類が変わった後に拡張機能が送った値で、利用者が :set で変えた値を隠すための状態である
// （PR #352 のレビューの推奨 9 へのオーケストレータの決め。基本設計 7.3 の「拡張機能から変えた値」）。
//
// 本体のファイルの種類ごとの段（optionLayer.perType）は、ファイルの種類が変わったときに、値を持つ項目の :set の値を隠す
// （changeFileTypeOptions。Neovim の ftplugin と同じ）。拡張機能の段の値は、本体が新しい種類で buffer/open を送り直した後に
// 非同期に届くので、changeFileTypeOptions の時点では隠せない。そこで、ファイルの種類が変わった後に、各拡張機能がそのバッファへ
// 送る最初の option/set のうち、FileType（ext.OptionSetParams）が今のファイルの種類と同じものだけ、値を書いた項目の :set の値を隠す。
// ファイルの種類の変更と関係なく届いた値（設定ファイルを保存した、など。2 つ目からの option/set）と、FileType を省いた値は隠さない。
// ファイルの種類が変わった後、値が届く前に利用者が :set で変えた項目は隠さない（Neovim の ftplugin は :set より前に当たるため）。
type extMaskState struct {
	// fileType は、変わった後のファイルの種類である。バッファを開いたとき（と :e）は空で、何も隠さない。
	fileType string
	// done は、fileType になった後に、最初の option/set を受け取った拡張機能の ID である。
	done map[string]bool
	// typed は、fileType になった後に利用者が :set で変えた項目の名前である。
	typed map[string]bool
	// names は、:set の値を隠している項目の名前から、隠させた拡張機能の ID への対応である。
	names map[string]string
}

// userSet は、利用者が :set で項目 name を変えたことを記録し、その項目を隠すのをやめる。
func (m *extMaskState) userSet(name string) {
	delete(m.names, name)
	if m.fileType == "" {
		return
	}
	if m.typed == nil {
		m.typed = map[string]bool{}
	}
	m.typed[name] = true
}

// received は、拡張機能 extID が、バッファ（今のファイルの種類 cur）へ option/set で値 set を入れ、unset を取り下げたことを記録する。
// その拡張機能が取り下げた項目は、隠すのをやめる。ファイルの種類が変わった後の、その拡張機能の最初の option/set で、
// 値を決めた種類 ft が cur と同じなら、set の項目を隠す（:set の後に変えた項目を除く）。
//
// req: FR-190
func (m *extMaskState) received(extID, ft, cur string, set map[string]option.Value, unset []string) {
	for _, name := range unset {
		if m.names[name] == extID {
			delete(m.names, name)
		}
	}
	if m.fileType == "" || m.fileType != cur || ft != cur || m.done[extID] {
		return
	}
	if m.done == nil {
		m.done = map[string]bool{}
	}
	m.done[extID] = true
	for name := range set {
		if m.typed[name] {
			continue
		}
		if m.names == nil {
			m.names = map[string]string{}
		}
		m.names[name] = extID
	}
}

// forget は、拡張機能 extID が隠させた項目を、隠すのをやめる（拡張機能が止まったとき）。
func (m *extMaskState) forget(extID string) {
	for name, id := range m.names {
		if id == extID {
			delete(m.names, name)
		}
	}
}

// extOptionDef は、option/set が名前 name の項目を変えられるかを確かめて、表の行を返す。
// buffer が true なら範囲がバッファごとの項目だけを、false ならエディタ全体の項目だけを受け付ける。
// 誤りは拡張機能へ返す（英語の小文字始まり）。
func extOptionDef(name string, buffer bool) (option.Def, error) {
	d, ok := option.Lookup(name)
	switch {
	case !ok:
		return option.Def{}, fmt.Errorf("unknown option %q", name)
	case d.Detected:
		return option.Def{}, fmt.Errorf("%s is detected from the file and cannot be set by extensions", name)
	case buffer && d.Scope != option.ScopeBuffer:
		return option.Def{}, fmt.Errorf("%s is an editor-wide option; omit buffer", name)
	case !buffer && d.Scope != option.ScopeEditor:
		return option.Def{}, fmt.Errorf("%s is a buffer option; set buffer", name)
	}
	return d, nil
}

// mergeExtValues は、拡張機能 extID の値 all[extID] に set を入れて unset を取り下げた対応を返す。値の無くなった拡張機能は除く。
func mergeExtValues(all map[string]map[string]option.Value, extID string, set map[string]option.Value, unset []string) map[string]map[string]option.Value {
	m := all[extID]
	if m == nil {
		m = map[string]option.Value{}
	}
	maps.Copy(m, set)
	for _, name := range unset {
		delete(m, name)
	}
	if len(m) == 0 {
		delete(all, extID)
		return all
	}
	if all == nil {
		all = map[string]map[string]option.Value{}
	}
	all[extID] = m
	return all
}

// extOrder は、拡張機能の値の対応 all の拡張機能の ID を、当てる順（後に当てたものが勝つ）に返す。
// 宣言ファイルを後に読んだ拡張機能から先に当て、先に読んだ拡張機能の値が勝つようにする。Rank が同じ（知らない ID）なら ID の順にする。
func (e *Editor) extOrder(all map[string]map[string]option.Value) []string {
	ids := slices.Collect(maps.Keys(all))
	slices.SortFunc(ids, func(a, b string) int {
		if c := cmp.Compare(e.host.Rank(b), e.host.Rank(a)); c != 0 {
			return c
		}
		return cmp.Compare(a, b)
	})
	return ids
}

// extLayerValues は、バッファの拡張機能の値 all を、初めの値の段の 1 つの対応にまとめる（optionLayers の 5。FR-190）。
func (e *Editor) extLayerValues(all map[string]map[string]option.Value) map[string]option.Value {
	if len(all) == 0 {
		return nil
	}
	out := map[string]option.Value{}
	for _, id := range e.extOrder(all) {
		maps.Copy(out, all[id])
	}
	return out
}

// applyEditorOption は、範囲がエディタ全体の項目 name の値を、拡張機能の値（無ければ設定ファイルの値か表の既定値）にする（FR-191）。
// 利用者が :set で変えた項目（Editor.userOptions）は変えない。値が今と同じなら入れない（option.Values.Revision を増やさない）。
//
// req: FR-191
func (e *Editor) applyEditorOption(name string) {
	d, ok := option.Lookup(name)
	if !ok || d.Scope != option.ScopeEditor || e.userOptions[name] {
		return
	}
	v := d.Default
	if c, ok := e.cfg.Options[name]; ok && d.Check(c) == nil {
		v = c
	}
	for _, id := range e.extOrder(e.extOptions) {
		if x, ok := e.extOptions[id][name]; ok {
			v = x
		}
	}
	if e.options.Get(name) != v {
		_ = e.options.Set(d, v) // 型と範囲は確かめてある
	}
}

// clearExtOptions は、拡張機能 extID が option/set で入れた値をすべて捨てる（拡張機能が異常で終わった、無効になった、
// :ext sync で止めたとき。stateChange）。バッファごとの項目はそのバッファの段を作り直し、エディタ全体の項目は
// ほかの拡張機能の値か設定ファイルの値に戻す。:set で変えた値は変えない。
//
// req: FR-190, FR-191
func (e *Editor) clearExtOptions(extID string) {
	for _, bs := range e.bufs {
		bs.extMask.forget(extID)
		if _, ok := bs.extOptions[extID]; ok {
			delete(bs.extOptions, extID)
			e.refreshOptionLayers(bs)
		}
	}
	if m, ok := e.extOptions[extID]; ok {
		delete(e.extOptions, extID)
		for name := range m {
			e.applyEditorOption(name)
		}
	}
}

// optionGetRequest は、option/get を処理し、項目の今の値（:set の値、拡張機能の値、初めの値の段を重ねた、本体が使う値）を返す。
// params の Names が空なら、Buffer を書いたときはすべての項目を、省いたときはエディタ全体のすべての項目を返す。
// 表に無い名前と、Buffer を省いたときのバッファごとの項目は、誤りで応答する。
//
// req: FR-192
func (e *Editor) optionGetRequest(ev *exthost.OptionGetRequest) {
	var bs *bufferState
	if ev.Params.Buffer != 0 {
		if bs = e.findBuffer(ev.Params.Buffer); bs == nil {
			_ = ev.ReplyError(invalidParams("option/get: unknown buffer %d", ev.Params.Buffer))
			return
		}
	}
	names := ev.Params.Names
	if len(names) == 0 {
		for _, d := range option.Defs() {
			if d.Scope == option.ScopeEditor || bs != nil {
				names = append(names, d.Name)
			}
		}
	}
	values := map[string]json.RawMessage{}
	for _, name := range names {
		d, ok := option.Lookup(name)
		var v option.Value
		switch {
		case !ok:
			_ = ev.ReplyError(invalidParams("option/get: unknown option %q", name))
			return
		case d.Scope == option.ScopeBuffer && bs == nil:
			_ = ev.ReplyError(invalidParams("option/get: %s is a buffer option; set buffer", name))
			return
		case d.Scope == option.ScopeBuffer:
			v = bs.option(name)
		default:
			v = e.options.Get(name)
		}
		raw, _ := v.MarshalJSON() // 誤りにならない
		values[name] = raw
	}
	_ = ev.Reply(ext.OptionGetResult{Values: values})
}
