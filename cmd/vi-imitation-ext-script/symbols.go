package main

import "reflect"

// 記号の表は yaegi extract で生成する。版を書かずに go run するので、go.mod の yaegi の版で生成される。
// ext か ext/server の公開の型や関数を変えたら、go generate ./cmd/vi-imitation-ext-script を打つ（TestSymbolsUpToDate が打ち忘れを検出する）。
//go:generate go run github.com/traefik/yaegi/cmd/yaegi extract -name main github.com/piplupflipper/vi-imitation/ext github.com/piplupflipper/vi-imitation/ext/server

// Symbols は、スクリプトに使わせる ext と ext/server の記号の表である。
// キーは yaegi の決まりで「import のパス/パッケージ名」である。
// 中身は、同じディレクトリの github_com-piplupflipper-vi-imitation-ext*.go（生成したファイル）の init が入れる。
var Symbols = map[string]map[string]reflect.Value{}
