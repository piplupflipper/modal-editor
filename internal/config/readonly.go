package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/tailscale/hujson"
)

// readOnlyPaths は、readonlyPaths の値を読む（基本設計 4.6、7 章。FR-120。項目の名前は仮）。
//
// 値は文字列の配列で、各項目は次のどれかである（仮。PR #199 のレビューを受けたオーケストレータの決定）。
//
//   - "/" で始まる絶対パス: そのディレクトリの下のファイルに当たる。filepath.Clean で整える
//   - "~" か "~/" で始まるパス: "~" を利用者のホームのディレクトリ（環境変数 HOME）に展開した絶対パスとして扱う
//   - "/" を含まないディレクトリの名前（"node_modules"）: ファイルのパスのどの階層のディレクトリの名前と一致しても当たる。そのまま返す
//
// それ以外（"a/b" のような "/" を含む相対パス、空の文字列、"~user" の形、HOME が無いときの "~"）は誤りとして記録し、その項目を使わない。
// 返す列の項目は、絶対パスかディレクトリの名前で、filepath.IsAbs で見分けられる。
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
		dir, err := readOnlyPath(p)
		if err != "" {
			s.report(el.StartOffset, "%s の %q: %s", path, p, err)
			continue
		}
		out = append(out, dir)
	}
	return out
}

// readOnlyPath は、readonlyPaths の 1 項目 p を、絶対パスかディレクトリの名前に整えて返す。
// 使えない項目なら、理由を problem に入れて返す。
func readOnlyPath(p string) (dir, problem string) {
	switch {
	case p == "":
		return "", "パスが空"
	case p == "~" || strings.HasPrefix(p, "~/"):
		home := os.Getenv("HOME")
		if !filepath.IsAbs(home) {
			return "", "HOME が絶対パスで設定されていないので ~ を展開できない"
		}
		return filepath.Join(home, p[1:]), ""
	case strings.HasPrefix(p, "~"):
		return "", "~user の形（他の利用者のホーム）は展開しない。~/ で始めるか、絶対パスで書く"
	case filepath.IsAbs(p):
		return filepath.Clean(p), ""
	case !strings.Contains(p, "/"):
		if p == "." || p == ".." {
			return "", ". と .. はディレクトリの名前として使えない"
		}
		return p, ""
	}
	return "", "/ を含む相対パスは使えない。絶対パスか ~/ で始まるパス、または / を含まないディレクトリの名前（node_modules など）にする"
}
