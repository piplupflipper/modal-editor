// Command modal-editor-ext-script は、スクリプトの拡張機能を 1 つ実行する実行ファイルである（FR-10、FR-36、基本設計 6.6）。
//
// 使い方:
//
//	modal-editor-ext-script <スクリプトのパス>
//
// スクリプトは、利用者が置く Go のソースファイル 1 つである。
// Go のインタープリター yaegi（github.com/traefik/yaegi）で実行するので、コンパイルは要らない。
// yaegi は本体には入れず、この実行ファイルだけに入れる（要件定義 6 章）。
//
// 本体は、宣言ファイルの command にこの実行ファイルを、args にスクリプトのパスを書いた拡張機能を、
// コンパイルする拡張機能と同じく子プロセスとして起動する（基本設計 6.7 の args）。
// 宣言ファイルの例は testdata/line/manifest.json にある。
// 1 つのプロセスは 1 つのスクリプトだけを実行する。
// スクリプトの無限ループや panic で、他のスクリプトの拡張機能を止めないためである（NFR-03）。
//
// # スクリプトの書き方
//
// スクリプトは package main で、func main を持つ。
// コンパイルする拡張機能と同じく ext と ext/server を import し、main で server.New のハンドラーを登録して Run を呼ぶ。
// 例は testdata/line/line.go にある。
// 同じソースは、go build でコンパイルする拡張機能としても動く。
//
// スクリプトが import できるのは、標準ライブラリ（yaegi の stdlib.Symbols にあるもの）と、ext と ext/server だけである。
// yaegi は Go のモジュールを読まないので、外部のモジュールは import できない（基本設計 6.6）。
// GOPATH の形のソースを読む機能も、この実行ファイルでは使わない（Options.GoPath を空にしている）。
//
// yaegi v0.16.3 で、Go の言語仕様と違う点のうち、スクリプトの作者が踏みやすいものを書く（2026-11-16 に確かめた）。
//
//   - 組み込み関数の max、min、clear が無い（undefined の誤りになる）
//   - ハンドラーに渡す関数リテラルの引数に名前を付けず、本体で変数を宣言すると、呼ばれたときに
//     「reflect.Set: value of type context.Context is not assignable to type int」のような panic になる。
//     使わない引数も _ と名前を付ければ起きない（testdata/faulty/faulty.go）
//
// スクリプトの標準入出力は、本体とのメッセージに使うので、スクリプトからは次のとおりに見える。
//
//   - os.Stdin は空である。読むとすぐ EOF になる
//   - os.Stdout と fmt.Print などへの出力は、標準エラー出力へ書く。本体はログとして記録する（基本設計 5.3）
//
// server.Server の Run は、このプロセスの本当の標準入出力でメッセージをやり取りする。
// Serve に os.Stdin と os.Stdout を渡すと、上の置き換えのためにメッセージが届かないので、Run を使う。
//
// # 異常の扱い
//
// スクリプトを読み込めない（ファイルが無い、構文の誤り、package main でない、func main が無い、import できない）ときは、
// 理由を標準エラー出力に書き、終了コード 1 で終わる。
// 構文の誤りは「パス:行:列: 内容」の形で書く。
// 本体は子プロセスの終了を異常として扱い、再起動し、失敗が続けば無効にする（基本設計 5.2）。
//
// スクリプトの main の panic は、yaegi が回復して EvalPath の誤りとして返すので、同じく終了コード 1 で終わる。
// ハンドラーの panic は、ext/server が回復してそのリクエストの誤りの応答にし、プロセスは動き続ける。コンパイルする拡張機能と同じである。
// スクリプトが起動した goroutine の panic は、プロセスを終わらせる。
// ハンドラーの無限ループは、メッセージを処理する goroutine を止めるので、本体の生存確認（ping）が期限を超え、本体が強制終了する。
package main

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"strings"

	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"
)

const name = "modal-editor-ext-script"

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

// run は、引数のスクリプトを実行し、終了コードを返す。
// スクリプトの main が戻れば（本体が標準入力を閉じて Run が戻れば）0 を、読み込みに失敗するか main が panic すれば 1 を、
// 引数の誤りは 2 を返す。誤りは stderr に書く。
func run(args []string, stderr io.Writer) int {
	if len(args) != 1 {
		_, _ = fmt.Fprintf(stderr, "usage: %s <script.go>\n", name)
		return 2
	}
	path := args[0]
	if err := runScript(path, stderr); err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return 1
	}
	return 0
}

// runScript は、path のスクリプトを検査してから yaegi で実行する。
// スクリプトの main が戻るまで戻らない。
//
// req: FR-10, FR-36
func runScript(path string, stderr io.Writer) error {
	if err := checkScript(path); err != nil {
		return err
	}
	i, err := newInterpreter(path, stderr)
	if err != nil {
		return err
	}
	if _, err := i.EvalPath(path); err != nil {
		var p interp.Panic
		if errors.As(err, &p) {
			return fmt.Errorf("%s: panic: %v\n%s", path, p.Value, p.Stack)
		}
		// yaegi の誤りは、先頭にパスと行と列を含む。
		return err
	}
	return nil
}

// checkScript は、path のスクリプトを構文解析し、package main で func main を持つことを確かめる。
//
// yaegi も構文の誤りを返すが、func main が無いスクリプトは何もせずに戻る（EvalPath が誤りを返さない）。
// そのまま終了コード 0 で終わると、本体のログからは理由が分からないので、実行する前に確かめる。
// 構文の誤りは go/scanner の形（パス:行:列: 内容）で返す。
func checkScript(path string) error {
	src, err := os.ReadFile(path) //nolint:gosec // 宣言ファイルの args に書かれたスクリプトを読むのが、この実行ファイルの役目である。
	if err != nil {
		return err
	}
	f, err := parser.ParseFile(token.NewFileSet(), path, src, parser.SkipObjectResolution)
	if err != nil {
		return fmt.Errorf("syntax error: %w", err)
	}
	if f.Name.Name != "main" {
		return fmt.Errorf("%s: package is %s, want main", path, f.Name.Name)
	}
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil && fd.Name.Name == "main" {
			return nil
		}
	}
	return fmt.Errorf("%s: func main is not declared", path)
}

// newInterpreter は、スクリプトを実行する yaegi のインタープリターを返す。
// 使わせる記号の表は、標準ライブラリと、ext と ext/server（Symbols）である。
// スクリプトの os.Stdin は空にし、os.Stdout は stderr へ向ける（パッケージの doc の「スクリプトの書き方」）。
func newInterpreter(path string, stderr io.Writer) (*interp.Interpreter, error) {
	i := interp.New(interp.Options{
		GoPath: "",
		Stdin:  strings.NewReader(""),
		Stdout: stderr,
		Stderr: stderr,
		Args:   []string{path},
	})
	if err := i.Use(stdlib.Symbols); err != nil {
		return nil, fmt.Errorf("load stdlib symbols: %w", err)
	}
	if err := i.Use(Symbols); err != nil {
		return nil, fmt.Errorf("load ext symbols: %w", err)
	}
	return i, nil
}
