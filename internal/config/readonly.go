package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/tailscale/hujson"
)

// readOnlyPaths は、readonlyPaths の値を読む（基本設計 4.6、7 章。FR-120。項目の名前は仮）。
//
// 値はディレクトリのパスの文字列の配列である。
// 先頭の "~" と "~/" は利用者のホームのディレクトリ（環境変数 HOME）に展開し、filepath.Clean で整える。
// 相対パス（作業ディレクトリによって指す先が変わる）、空の文字列、HOME が無いときの "~" は誤りとして記録し、その項目を使わない。
// "~user" の形は展開しない（相対パスとして誤りにする）。
//
// req: FR-120
func (s *source) readOnlyPaths(v *hujson.Value) []string {
	const path = "readonlyPaths"
	arr, ok := s.array(v, path)
	if !ok {
		return nil
	}
	out := []string{}
	for i := range arr.Elements {
		el := &arr.Elements[i]
		p, ok := s.str(el, path+" の項目")
		if !ok {
			continue
		}
		dir, err := expandHome(p)
		if err != "" {
			s.report(el.StartOffset, "%s の %q: %s", path, p, err)
			continue
		}
		out = append(out, dir)
	}
	return out
}

// expandHome は、p の先頭の "~" をホームのディレクトリに展開し、絶対パスに整えて返す。
// 使えないパスなら、理由を problem に入れて返す。
func expandHome(p string) (dir, problem string) {
	if p == "" {
		return "", "パスが空"
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		home := os.Getenv("HOME")
		if !filepath.IsAbs(home) {
			return "", "HOME が絶対パスで設定されていないので ~ を展開できない"
		}
		p = filepath.Join(home, p[1:])
	}
	if !filepath.IsAbs(p) {
		return "", "絶対パスか ~ で始まるパスにする"
	}
	return filepath.Clean(p), ""
}
