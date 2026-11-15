package main

import "reflect"

// 記号の表は yaegi extract で生成する。yaegi の版は go.mod と揃える（TestSymbolsUpToDate が食い違いを検出する）。
// ext か ext/server の公開の型や関数を変えたら、go generate ./cmd/modal-editor-ext-script を打つ。
//go:generate go run github.com/traefik/yaegi/cmd/yaegi@v0.16.3 extract -name main github.com/piplupflipper/modal-editor/ext github.com/piplupflipper/modal-editor/ext/server

// Symbols は、スクリプトに使わせる ext と ext/server の記号の表である。
// キーは yaegi の決まりで「import のパス/パッケージ名」である。
// 中身は、同じディレクトリの github_com-piplupflipper-modal-editor-ext*.go（生成したファイル）の init が入れる。
var Symbols = map[string]map[string]reflect.Value{}
