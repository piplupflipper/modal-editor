package editor

import (
	"path/filepath"
	"strings"

	"github.com/piplupflipper/modal-editor/internal/buffer"
	"github.com/piplupflipper/modal-editor/internal/rpc"
)

// readOnlyError は、編集を禁止したバッファへの buffer/edit に返す誤りの応答である（FR-118）。
//
// applied を false にした結果では返さない。applied の false は、版の不一致（拡張機能が新しい版で作り直せば通る）を表すので、
// 禁止と同じ応答にすると、拡張機能が作り直しを繰り返すためである（基本設計 4.6 の「applied を false で応答」からの変更。PR の説明に書いた）。
// code は、要求の形は正しいが今の状態では受け付けないことを表す -32600（Invalid Request）にし、message は画面に出す E21 の文言と同じにする。
//
// req: FR-118
func readOnlyError() error {
	return &rpc.Error{Code: rpc.CodeInvalidRequest, Message: buffer.ErrReadOnly.Error()}
}

// underReadOnlyPath は、ファイルの絶対パス path が、dirs のどれかのディレクトリの下にあるかを返す（設定の readonlyPaths。FR-120）。
//
// 照合はパスの文字列で行い、シンボリックリンクは解決しない（基本設計 4.6）。
// ディレクトリの区切りで比べるので、"/a/b" は "/a/bc/x" に当たらない。ディレクトリそのもののパスも当たる。
//
// req: FR-120
func underReadOnlyPath(path string, dirs []string) bool {
	for _, d := range dirs {
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
