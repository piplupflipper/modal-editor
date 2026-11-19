// Package docs は、本体の実行ファイルに埋め込む文書を持つ（FR-89。基本設計 7.2）。
//
// 埋め込み（embed）はパッケージのディレクトリの外のファイルを読めないので、文書と同じディレクトリにこのパッケージを置く。
// 他のパッケージを import しない。
package docs

import _ "embed" // 埋め込みのため

// Usage は、docs/usage.md の中身である。:help が、ファイルを読まずに出す（internal/editor の help.go）。
//
//go:embed usage.md
var Usage string
