package ui

import (
	"errors"
	"fmt"
	"maps"
)

// ファイルの種類のアイコン（FR-152。基本設計 1.4 の「#295 のアイコン」と 7.1）。
//
// 一覧と木の項目がファイルかディレクトリを指すとき（ext.ViewItem.File）、本体は項目の前にアイコンと空白 1 つを置いて描く。
// アイコンは拡張子でなく、本体が決めたファイルの種類（internal/editor の fileTypes の対応表で決まる "go" などの名前）ごとに引く。
// 表はデータだけで持ち、ファイルの種類の名前で分岐する処理は書かない（#295 のオーナーの決め）。
// 既定の表は icons_default.go にある。

// アイコンの名前。設定の icons の下の位置と同じにした（"fileTypes.go" は "icons": { "fileTypes": { "go": … } } の位置）。
const (
	// IconDirectory は、閉じたディレクトリのアイコンである。一覧（木でない部品）のディレクトリにも使う。
	IconDirectory = "directory"
	// IconDirectoryOpen は、木で子を開いたディレクトリのアイコンである。
	IconDirectoryOpen = "directoryOpen"
	// IconFile は、種類の分からないファイル（種類が決まらないか、表に無い種類）のアイコンである。
	IconFile = "file"
	// IconFileTypePrefix は、ファイルの種類のアイコンの名前の接頭辞である。後に種類の名前をつなぐ（IconFileType）。
	IconFileTypePrefix = "fileTypes."
)

// iconStylePrefix は、アイコンの色を Theme に入れるときの色の名前の接頭辞である（IconStyle）。
// この名前は StyleNames に無いので、設定の colors と配色のファイルには書けない（アイコンの色は設定の icons に書く）。
const iconStylePrefix = "icon."

// Icon は、アイコン 1 つの、描く文字列と色である。
type Icon struct {
	// Text は、描く文字列である。CheckIcon を通るもの（表示幅が 1 か 2）にする。
	Text string
	// Attr は、Text の色と飾りである。窓の色（StylePopup、選んでいる行なら StylePopupSelected）の上に重ねる（Attr.Over）。
	Attr Attr
}

// Icons は、アイコンの名前（IconDirectory、IconFileType の値など）からアイコンへの対応である。
type Icons map[string]Icon

// IconFileType は、ファイルの種類 fileType のアイコンの名前を返す。
func IconFileType(fileType string) string {
	return IconFileTypePrefix + fileType
}

// IconStyle は、アイコンの名前 name の色を Theme から引く色の名前を返す（Theme.WithIcons が入れる名前）。
func IconStyle(name string) string {
	return iconStylePrefix + name
}

// DefaultIcons は、既定のアイコンの表（defaultIcons）の写しを返す。
func DefaultIcons() Icons {
	return maps.Clone(defaultIcons)
}

// With は、ic を写したものに、over の名前ごとのアイコンを上書きした表を返す（設定の icons）。
// 上書きは名前ごとの置き換えで、既定の文字と色を混ぜない（色の名前ごとに置き換える設定の colors と同じ規則。Theme.WithColors）。
// ic は変更しない。
//
// req: FR-152
func (ic Icons) With(over Icons) Icons {
	out := make(Icons, len(ic)+len(over))
	maps.Copy(out, ic)
	maps.Copy(out, over)
	return out
}

// Lookup は、項目が指すもののアイコンの名前とアイコンを返す。
// dir ならディレクトリで、open なら IconDirectoryOpen、そうでなければ IconDirectory である。
// ファイルなら、ファイルの種類 fileType のアイコン（IconFileType）で、種類が空か表に無ければ IconFile である。
// 表にその名前が無ければ（既定の表を使わずに作った表）、ok が false である。
//
// req: FR-152
func (ic Icons) Lookup(fileType string, dir, open bool) (name string, icon Icon, ok bool) {
	switch {
	case dir && open:
		name = IconDirectoryOpen
	case dir:
		name = IconDirectory
	default:
		name = IconFile
		if fileType != "" {
			if _, found := ic[IconFileType(fileType)]; found {
				name = IconFileType(fileType)
			}
		}
	}
	icon, ok = ic[name]
	return name, icon, ok
}

// Width は、表のアイコンの表示幅の最大（アイコンの列の幅）を返す。表が空なら 1 を返す。
// 一覧と木は、どのアイコンもこの幅まで空白で埋めて置くので、幅 2 のアイコンを書いた表でも表示の文字列の列がそろう
// （PR #305 のレビューの推奨 2）。CheckIcon を通ったアイコンだけの表なら、1 か 2 である。
//
// req: FR-152
func (ic Icons) Width() int {
	w := 1
	for _, icon := range ic {
		w = max(w, StringWidth(icon.Text, DefaultTabStop))
	}
	return w
}

// WithIcons は、t を写したものに、ic の各アイコンの色を IconStyle の名前で足した対応を返す。
// 描画は、アイコンの文字の範囲に付けた IconStyle の名前の色を、ほかの色の名前と同じく t から引く。t は変更しない。
func (t Theme) WithIcons(ic Icons) Theme {
	out := make(Theme, len(t)+len(ic))
	maps.Copy(out, t)
	for name, icon := range ic {
		out[IconStyle(name)] = icon.Attr
	}
	return out
}

// CheckIcon は、アイコンの文字列 text を使えるかを確かめ、使えなければ理由を返す（設定の icons の検査。FR-152）。
// 1 つの文字（後に続く結合文字を含む。描画が 1 つのセルに置く単位）で、表示幅が 1 か 2 で、タブと制御文字でないものだけを通す。
// 1 つの文字に限るのは、U+FFFF より大きいグリフを JSON の \u の 4 桁で書き損じたもの（\u の後に 5 桁の f0868 を書くと、U+F086 と "8" の 2 文字になる）を、
// 誤りとして知らせるためである（PR #305 のレビューの推奨 3）。
// Nerd Font のグリフ（私用領域の文字）は、本体は 1 列と数える（Vim の 'ambiwidth' の既定の single と同じ。width.go）。
//
// req: FR-152
func CheckIcon(text string) error {
	if text == "" {
		return errors.New("アイコンは 1 つの文字にする（空である）")
	}
	gl := nextGlyph(text, 0, 0, DefaultTabStop)
	switch {
	case gl.kind != glyphText:
		return errors.New("タブと制御文字は使えない")
	case gl.size != len(text):
		return fmt.Errorf("アイコンは 1 つの文字にする（%q の後に %q がある。U+FFFF より大きい文字は、サロゲートの組か文字そのもので書く）",
			text[:gl.size], text[gl.size:])
	case gl.width < 1 || gl.width > 2:
		return fmt.Errorf("アイコンの表示幅は 1 か 2 にする（%d ある）", gl.width)
	}
	return nil
}
