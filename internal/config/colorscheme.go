package config

import (
	"embed"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tailscale/hujson"
)

// ColorSchemeDir は、配色のファイルを置くディレクトリの名前である（基本設計 7.1。FR-125。名前は仮）。
// 利用者の配色は設定ファイルと同じディレクトリの下のこの名前のディレクトリに、同梱の配色はこのパッケージの同じ名前のディレクトリに置く。
const ColorSchemeDir = "colors"

// colorSchemeExt は、配色のファイルの拡張子である。
const colorSchemeExt = ".jsonc"

// bundledColorSchemes は、本体に同梱した配色のファイルである（#253。go:embed）。
// 配色を足すときは、colors の下に <名前>.jsonc を 1 つ置くだけでよい。
//
//go:embed colors/*.jsonc
var bundledColorSchemes embed.FS

// colorSchemeName は、配色の名前に使える文字である（仮）。
// "/" と "." を許さないので、名前から作るパスは配色のディレクトリの外のファイルを指せない。
var colorSchemeName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// BundledColorSchemes は、同梱した配色の名前を、名前の順に返す。
func BundledColorSchemes() []string {
	entries, err := bundledColorSchemes.ReadDir(ColorSchemeDir)
	if err != nil {
		// 埋め込んだディレクトリなので、ここには来ない。
		return nil
	}
	var names []string
	for _, e := range entries {
		if name, ok := strings.CutSuffix(e.Name(), colorSchemeExt); ok {
			names = append(names, name)
		}
	}
	return names
}

// colorScheme は、colorscheme の値を読む（基本設計 7.1。FR-125）。値は配色の名前の文字列で、
// 英数字と "_" と "-" だけからなる名前でなければ誤りとして既定値（空。配色を選ばない）を返す。
// 名前の配色があるかは LoadColorScheme で確かめる。
//
// req: FR-125
func (s *source) colorScheme(v *hujson.Value, cfg *Config) string {
	str, ok := s.str(v, "colorscheme")
	if !ok {
		return ""
	}
	if !colorSchemeName.MatchString(str) {
		s.report(v.StartOffset, "colorscheme %q は使えない名前。英数字と _ と - だけにする", str)
		return ""
	}
	cfg.pos.colorScheme = s.at(v.StartOffset, "")
	return str
}

// LoadColorScheme は、ColorScheme の名前の配色のファイルを読んで SchemeColors に入れ、誤りを位置の順に返す（基本設計 7.1。FR-125）。
// ColorScheme が空なら何もしない。
//
// 配色のファイルは、dir（設定ファイルのディレクトリ）の下の colors/<名前>.jsonc を探し、無ければ同梱の配色を探す。
// 同じ名前なら利用者のファイルを使う。dir が空なら利用者のファイルを探さない。
// ファイルは設定の colors の値と同じ形（色の名前から属性のオブジェクトへの対応）の JSONC で、読み方と誤りの扱いも colors と同じである。
// 色の名前は check.Color で確かめ（CheckAppearance と同じ）、本体の知らない名前は誤りとして除く。
// どちらにも無い名前は、設定ファイルの colorscheme の位置の誤りにして、配色を使わない（SchemeColors は nil のまま）。
//
// req: FR-125
func (c *Config) LoadColorScheme(dir string, check AppearanceCheck) []error {
	if c.ColorScheme == "" {
		return nil
	}
	p, data, err := c.findColorScheme(dir)
	if err != nil {
		return []error{err}
	}
	colors, pos, errs := parseColorScheme(p, data)
	if check.Color != nil {
		for name := range colors {
			if !check.Color(name) {
				errs = append(errs, pos[name].with("配色 %s の %s は未知の色の名前なので無視する", c.ColorScheme, name))
				delete(colors, name)
			}
		}
	}
	c.SchemeColors = colors
	SortErrors(errs)
	return errs
}

// findColorScheme は、ColorScheme の配色のファイルを、dir の下の colors、同梱の配色の順に探し、パスと内容を返す。
// 同梱の配色のパスは、埋め込んだディレクトリの中のパス（colors/<名前>.jsonc）である。
func (c *Config) findColorScheme(dir string) (string, []byte, error) {
	file := c.ColorScheme + colorSchemeExt
	if dir != "" {
		p := filepath.Join(dir, ColorSchemeDir, file)
		data, err := os.ReadFile(p)
		if err == nil {
			return p, data, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", nil, &Error{Path: p, Msg: "配色のファイルを読めない: " + err.Error()}
		}
	}
	p := path.Join(ColorSchemeDir, file)
	data, err := bundledColorSchemes.ReadFile(p)
	if err != nil {
		return "", nil, c.pos.colorScheme.with("E185: 配色 %s が見つからない", c.ColorScheme)
	}
	return p, data, nil
}

// parseColorScheme は、data を配色のファイルとして読み、色の名前から属性への対応と、名前を書いた位置と、誤りを返す。
// p は誤りの文言に出すパスである。空のファイルと、空白とコメントだけのファイルは、名前の無い配色として読む。
func parseColorScheme(p string, data []byte) (map[string]Style, map[string]*Error, []error) {
	s := newSource(p, data)
	pos := map[string]*Error{}
	if s.onlyExtra() {
		return map[string]Style{}, pos, nil
	}
	root, err := s.parseJSONC()
	if err != nil {
		return map[string]Style{}, pos, []error{err}
	}
	obj, ok := root.Value.(*hujson.Object)
	if !ok {
		s.report(root.StartOffset, "配色のファイルの最上位はオブジェクトにする")
		return map[string]Style{}, pos, s.problems
	}
	colors := s.styles(obj, "", pos)
	return colors, pos, s.problems
}
