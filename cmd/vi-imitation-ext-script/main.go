// Command vi-imitation-ext-script は、スクリプトの拡張機能を 1 つ実行する実行ファイルである（FR-10、FR-36、基本設計 6.6）。
//
// 使い方:
//
//	vi-imitation-ext-script <スクリプトのパス>
//
// スクリプトは、利用者が置く Go のソースファイル 1 つである。
// Go のインタープリター yaegi（github.com/traefik/yaegi）で実行するので、コンパイルは要らない。
// yaegi は本体には入れず、この実行ファイルだけに入れる（要件定義 6 章）。
//
// # 宣言ファイル
//
// 本体は、宣言ファイルの command にこの実行ファイルを、args にスクリプトのパスを書いた拡張機能を、
// コンパイルする拡張機能と同じく子プロセスとして起動する（基本設計 6.7 の args）。
// 宣言ファイルの例は testdata/line/manifest.json にある。
// 1 つのプロセスは 1 つのスクリプトだけを実行する。
// スクリプトの無限ループや panic で、他のスクリプトの拡張機能を止めないためである（NFR-03）。
//
// args の相対パスは、本体が環境変数 VI_IMITATION_EXTENSION_DIR（ext.EnvExtensionDir）で渡す宣言ファイルのディレクトリから解決する。
// 子プロセスの作業ディレクトリは本体のものなので、作業ディレクトリからは解決しない。
// 環境変数が無いか空なら、作業ディレクトリからの相対パスとして扱う。
//
// この実行ファイルは、複数のスクリプトで 1 つを共有できる。
// ただし本体は command を PATH から探さず、相対パスを常に宣言ファイルのディレクトリから解決する（基本設計 5.1）。
// 共有するには、command に共有の実行ファイルの絶対パスか、宣言ファイルのディレクトリからの相対パス（../bin/vi-imitation-ext-script など）を書く。
// 例の宣言ファイルの ./vi-imitation-ext-script は、実行ファイルを宣言ファイルと同じディレクトリに置く（コピーかシンボリックリンク）書き方である。
// 本体が拡張機能を探す規則は、仮に決まっている（要件定義 6 章、基本設計 7 章）。
// 利用者のスクリプトの宣言ファイルが、配布物のこの実行ファイルを、配布物を置いた場所を書かずに指す方法は未決である（基本設計 10 章、6.6）。
//
// # スクリプトの書き方
//
// スクリプトは package main で、func main を持つ。
// コンパイルする拡張機能と同じく ext と ext/server を import し、main で server.New のハンドラーを登録して Run を呼ぶ。
// 例は testdata/line/line.go にある。
//
// スクリプトが import できるのは、標準ライブラリ（yaegi の stdlib.Symbols と unrestricted.Symbols にあるもの）と、ext と ext/server だけである。
// yaegi は Go のモジュールを読まないので、外部のモジュールは import できない（基本設計 6.6）。
// GOPATH の形のソースを読む機能も、この実行ファイルでは使わない（Options.GoPath を空にしている）。
//
// yaegi は制限なしのモード（Options.Unrestricted）で動かす。
// スクリプトは、コンパイルする拡張機能と同じく利用者の権限で動く別のプロセスであり、
// 悪意のある拡張機能から守ることは要件の対象外である（要件定義 3.2、NFR-03 の注）。
// 制限のモードは守りにならず、環境変数が読めない、os/exec が使えない、os.Exit と log.Fatal が panic になる、という違いだけが残るので使わない。
// このため os.Getenv、os/exec、os.Exit、log.Fatal は、コンパイルした Go と同じく動く。
//
// # yaegi の制限
//
// 同じソースは go build でも通るが、yaegi v0.16.3 では Go と違う点がある（組み込み関数の max が無い、など）。
// 利用者が当たる違いの一覧は、docs/usage.md の「yaegi の制限」に 1 つだけ置き、本体の vi-imitation --docs usage 'yaegi の制限' で読める。
// 一覧を変えるときは、そちらを直す。
// ハンドラーの引数に名前を付けないと panic になる違いは、testdata/faulty/faulty.go のテストが確かめる。
// 使える標準ライブラリの記号の一覧は、cmd/docgen が下の newInterpreter と同じ記号の表から生成する（vi-imitation --docs script-symbols）。
//
// yaegi が読み込みで返す誤りには、一覧を読むコマンドを添えて標準エラー出力に書く。
//
// # 標準入出力
//
// この実行ファイルの標準入出力は本体とのメッセージに使うので、スクリプトからは次のとおりに見える。
//
//   - os.Stdin は os.DevNull である。読むとすぐ EOF になり、本体とのメッセージを読むことは無い
//   - os.Stdout と fmt.Print などへの出力は、標準エラー出力へ書く。本体はログとして記録する（基本設計 5.3）
//
// server.Server の Run は、コンパイルされた側のコードなので、このプロセスの本当の標準入出力でメッセージをやり取りする。
// スクリプトから Serve に os.Stdin と os.Stdout を渡すと、上の置き換えのためにメッセージが届かないので、Run を使う。
//
// # 異常の扱い
//
// スクリプトを読み込めない（ファイルが無い、構文の誤り、package main でない、func main が無い、import できない）ときは、
// 理由を標準エラー出力に書き、終了コード 1 で終わる。
// 構文の誤りは「パス:行:列: 内容」の形で書く。
// 本体は子プロセスの終了を異常として扱い、再起動し、失敗が続けば無効にする（基本設計 5.2）。
//
// スクリプトの main の panic は、yaegi が回復して EvalPath の誤りとして返すので、同じく終了コード 1 で終わる。
// os.Exit と log.Fatal は、そのままプロセスを終わらせる。
// ハンドラーの panic は、ext/server が回復してそのリクエストの誤りの応答にし、プロセスは動き続ける。コンパイルする拡張機能と同じである。
// スクリプトが起動した goroutine の panic は、プロセスを終わらせる。
// ハンドラーの無限ループは、メッセージを処理する goroutine を止めるので、本体の生存確認（ping）が期限を超え、本体が強制終了する。
// スクリプトが起動した別の goroutine の無限ループは、ping に答え続けるので検出しない（コンパイルする拡張機能でも同じである）。
package main

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"

	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"
	"github.com/traefik/yaegi/stdlib/unrestricted"

	"github.com/piplupflipper/vi-imitation/ext"
)

const name = "vi-imitation-ext-script"

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

// run は、引数のスクリプトを実行し、終了コードを返す。
// スクリプトの main が戻れば（本体が標準入力を閉じて Run が戻れば）0 を、読み込みに失敗するか main が panic すれば 1 を、
// 引数の誤りは 2 を返す。誤りは stderr に書く。
// スクリプトが os.Exit を呼べば、run は戻らずにその終了コードでプロセスが終わる。
func run(args []string, stderr io.Writer) int {
	if len(args) != 1 {
		_, _ = fmt.Fprintf(stderr, "usage: %s <script.go>\n", name)
		return 2
	}
	path := scriptPath(args[0], os.Getenv(ext.EnvExtensionDir))
	if err := runScript(path, stderr); err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return 1
	}
	return 0
}

// scriptPath は、引数のスクリプトのパスを返す。
// 相対パスは、本体が渡す宣言ファイルのディレクトリ dir（環境変数 ext.EnvExtensionDir）から解決する。
// dir が空なら、作業ディレクトリ（本体のもの）からの相対パスのまま返す。
func scriptPath(arg, dir string) string {
	if filepath.IsAbs(arg) || dir == "" {
		return arg
	}
	return filepath.Join(dir, arg)
}

// runScript は、path のスクリプトを検査してから yaegi で実行する。
// スクリプトの main が戻るまで戻らない。
//
// req: FR-10, FR-36
func runScript(path string, stderr io.Writer) error {
	if err := checkScript(path); err != nil {
		return err
	}
	stdin, err := os.Open(os.DevNull)
	if err != nil {
		return err
	}
	defer func() { _ = stdin.Close() }()
	i, err := newInterpreter(path, stdin, stderr)
	if err != nil {
		return err
	}
	if _, err := i.EvalPath(path); err != nil {
		var p interp.Panic
		if errors.As(err, &p) {
			return fmt.Errorf("%s: panic: %v\n%s", path, p.Value, p.Stack)
		}
		// yaegi の誤りは、先頭にパスと行と列を含む。
		// undefined: max のように、Go では正しいのに yaegi では誤りになるものがあるので、制限の一覧の場所を添える。
		return fmt.Errorf("%w\n%s", err, limitsHint)
	}
	return nil
}

// limitsHint は、yaegi の読み込みの誤りに添える、制限の一覧を読むコマンドの案内である。
// 配布物だけを持つ利用者が読めるよう、本体に埋め込んだ文書（vi-imitation --docs）を指す。
const limitsHint = "(yaegi の制限は vi-imitation --docs usage 'yaegi の制限' を、使える記号は vi-imitation --docs script-symbols <import のパス> を参照)"

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
// 制限なしのモード（Unrestricted）で動かし、使わせる記号の表は、標準ライブラリ（os/exec と os.Exit を含む）と、ext と ext/server（Symbols）である。
// スクリプトの os.Stdin は stdin（os.DevNull）にし、os.Stdout は stderr へ向ける（パッケージの doc の「標準入出力」）。
// yaegi が os.Stdin と os.Stdout を置き換えるのは、渡した値が *os.File のときだけである。
func newInterpreter(path string, stdin *os.File, stderr io.Writer) (*interp.Interpreter, error) {
	i := interp.New(interp.Options{
		GoPath:       "",
		Stdin:        stdin,
		Stdout:       stderr,
		Stderr:       stderr,
		Args:         []string{path},
		Unrestricted: true,
	})
	if err := i.Use(stdlib.Symbols); err != nil {
		return nil, fmt.Errorf("load stdlib symbols: %w", err)
	}
	if err := i.Use(unrestricted.Symbols); err != nil {
		return nil, fmt.Errorf("load unrestricted symbols: %w", err)
	}
	if err := i.Use(Symbols); err != nil {
		return nil, fmt.Errorf("load ext symbols: %w", err)
	}
	return i, nil
}
