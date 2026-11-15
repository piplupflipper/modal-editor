// Package version は、ビルド時に埋め込まれたバージョン文字列を提供する。
package version

// version は、ビルド時に -ldflags の -X で上書きされる。
// 上書きされなかった場合（go run や go test）は "dev" のままになる。
var version = "dev"

// String は、バージョン文字列を返す。
func String() string {
	return version
}
