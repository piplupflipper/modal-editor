// Package distcheck は、`make build` と `make dist` が作った実行ファイルと配布物を検査するテストを置く。
//
// 検査の対象は、ソースではなくビルドの生成物である。
// 生成物のディレクトリを環境変数で受け取り、指定が無ければテストを飛ばす。
// `make build-check` が `make build` の後に環境変数を指定してこのテストを実行する。
// 検証の方法は docs/basic-design.md の 9 章に書く。
//
// このパッケージは他のパッケージから import しない。
package distcheck
