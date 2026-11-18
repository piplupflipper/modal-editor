package editor

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/internal/buffer"
	"github.com/piplupflipper/modal-editor/internal/rpc"
)

// readOnlyError は、編集を禁止したバッファへの buffer/edit に返す誤りの応答である（FR-118）。
//
// applied を false にした結果では返さない。applied の false は、版の不一致（拡張機能が新しい版で作り直せば通る）を表すので、
// 禁止と同じ応答にすると、拡張機能が作り直しを繰り返すためである（基本設計 4.6 の「applied を false で応答」からの変更。PR の説明に書いた）。
// code は ext.ErrorCodeReadOnly（実装が定める範囲の -32001）にし、message は画面に出す E21 の文言と同じにする。
//
// req: FR-118
func readOnlyError() error {
	return &rpc.Error{Code: ext.ErrorCodeReadOnly, Message: buffer.ErrReadOnly.Error()}
}

// underReadOnlyPath は、ファイルの絶対パス path が、設定の readonlyPaths の項目 dirs のどれかに当たるかを返す（FR-120）。
//
// 絶対パスの項目は、path がそのディレクトリの下にあれば当たる。区切りで比べるので、"/a/b" は "/a/bc/x" に当たらない。ディレクトリそのもののパスも当たる。
// 絶対パスでない項目（"/" を含まないディレクトリの名前。"node_modules"）は、path のどの階層のディレクトリの名前と一致しても当たる
// （"/p/node_modules/x/y.js" は当たり、"/p/node_modules.js" は当たらない）。
// 照合はパスの文字列で行い、シンボリックリンクは解決しない（基本設計 4.6）。
//
// req: FR-120
func underReadOnlyPath(path string, dirs []string) bool {
	var names []string
	for _, d := range dirs {
		if !filepath.IsAbs(d) {
			if names == nil {
				names = strings.Split(filepath.Dir(path), string(filepath.Separator))
			}
			if slices.Contains(names, d) {
				return true
			}
			continue
		}
		d = filepath.Clean(d)
		if path == d {
			return true
		}
		if d == string(filepath.Separator) || strings.HasPrefix(path, d+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
