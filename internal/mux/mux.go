// Package mux は、端末多重化ソフト（tmux と herdr）を操作する層である（基本設計 1.3。FR-66、FR-67）。
//
// server だけがこの層を使う。拡張機能はこの層を直接使わず、本体へのリクエスト（pane/open、popup/open）で頼む（FR-115）。
// このパッケージは、どのペインに何を出すかを知らない。呼び出し側が、起動するコマンドと環境変数を渡す。
//
// # 実装
//
// Detect が環境変数から、tmux 用、herdr 用、どちらでもないときの 3 つの実装のどれかを選ぶ。
// 判定の順は、server のソケットのパスの規則（基本設計 1.2）と同じく、tmux、herdr、どちらでもない、の順である。
//
//   - tmux: tmux のコマンドを、環境変数 TMUX の先頭のソケットのパスを -S に渡して起動する（tmux.go）
//   - herdr: herdr の socket API（protocol 22）へ、改行で区切った JSON のリクエストを送る（herdr.go）
//   - どちらでもない（FR-60）: どの操作も ErrUnsupported を返す
//
// # 並行性
//
// どの実装も、作った後に状態を変えない。メソッドは複数の goroutine から同時に呼んでよい。
// どの操作も外部のコマンドかソケットを使うので時間がかかる。
// イベントループ（基本設計 2.1）から同期で呼ばず、別の goroutine で呼んで結果をチャネルで受ける前提である。
package mux

import (
	"context"
	"errors"
	"strings"
)

// Kind は、端末多重化ソフトの種類である。
type Kind int

// 端末多重化ソフトの種類。
const (
	// KindNone は、端末多重化ソフトの外である（FR-60）。
	KindNone Kind = iota
	// KindTmux は、tmux の中である。
	KindTmux
	// KindHerdr は、herdr の中である。
	KindHerdr
)

// String は、種類の名前を返す。
func (k Kind) String() string {
	switch k {
	case KindNone:
		return "none"
	case KindTmux:
		return "tmux"
	case KindHerdr:
		return "herdr"
	}
	return "unknown"
}

// ErrUnsupported は、端末多重化ソフトの外（FR-60）で、操作ができないことを表す。
// 呼び出し側は、ポップアップを編集の client の画面の中央に描くなど、代わりの手段を取る（基本設計 1.2、1.4）。
var ErrUnsupported = errors.New("mux: no terminal multiplexer")

// Direction は、ペインを分割する向きである。
type Direction int

// 分割の向き。新しいペインは、元のペインの右か下に開く。
const (
	// Right は、元のペインの右に開く（tmux の split-window -h、herdr の right）。
	Right Direction = iota
	// Down は、元のペインの下に開く（tmux の split-window -v、herdr の down）。
	Down
)

// Command は、ペインかポップアップの中で起動するコマンドである。
type Command struct {
	// Args は、起動するプログラムとその引数である。空にしない。
	// 端末多重化ソフトにはシェルのコマンドの文字列として渡すので、各要素はシェルの引用で囲んで渡す。
	Args []string
	// Env は、足す環境変数である（server のソケットのパス MODAL_EDITOR_SOCKET など。基本設計 1.3）。
	Env map[string]string
	// Dir は、起動するディレクトリである。空なら端末多重化ソフトの既定に任せる。
	Dir string
}

// SplitOptions は、ペインを分割して開くときの指定である。
type SplitOptions struct {
	// Target は、分割する元のペインの識別子である（元の client のペイン）。空なら Mux.CurrentPane のペインを使う。
	Target string
	// Direction は、分割の向きである。
	Direction Direction
	// Size は、新しいペインの大きさを、分割する向きの元のペインの大きさに対する百分率（1〜100）で表す。
	// 0 なら端末多重化ソフトの既定に任せる。
	// herdr 0.9.0 は、プラグインのペインを分割で開くときの大きさを受け付けないので、herdr では無視する。
	Size int
	// Command は、新しいペインで起動するコマンドである。
	Command Command
	// Focus は、新しいペインへフォーカスを移すかである。
	Focus bool
}

// PopupOptions は、画面の中央のポップアップを開くときの指定である（FR-67）。
type PopupOptions struct {
	// Target は、ポップアップを開いた元のペイン（元の client のペイン）の識別子である。空なら Mux.CurrentPane のペインを使う。
	// tmux では、このペインを表示している tmux の client の上に開く。
	// herdr 0.9.0 は、ポップアップをアクティブなペインの上にしか開けないので、herdr では使わない。
	Target string
	// Width と Height は、端末全体に対する百分率（1〜100）である。0 なら端末多重化ソフトの既定に任せる。
	Width, Height int
	// Command は、ポップアップの中で起動するコマンドである。コマンドが終わるとポップアップは閉じる。
	Command Command
}

// Mux は、端末多重化ソフトを操作する口である（基本設計 1.3）。
//
// ペインの識別子は、tmux では %1 のようなペインの ID、herdr では wW:p7 のようなペインの ID である。
type Mux interface {
	// Kind は、端末多重化ソフトの種類を返す。
	Kind() Kind

	// CurrentPane は、Detect に渡した環境変数のペイン（この層を作ったプロセスがいるペイン）の識別子を返す。
	CurrentPane(ctx context.Context) (string, error)

	// Unit は、server の単位（tmux の window、herdr のタブ）を、ほかの単位と区別できる文字列で返す（基本設計 1.2）。
	// 呼び出し側は、この文字列から server のソケットのパスを作る（FR-59）。
	// 端末多重化ソフトのサーバー（tmux のソケット、herdr のソケット）が異なれば、識別子が同じでも文字列は異なる。
	Unit(ctx context.Context) (string, error)

	// SplitPane は、ペインを分割して開き、中でコマンドを起動し、新しいペインの識別子を返す。
	// コマンドが終わるとペインは閉じる。
	SplitPane(ctx context.Context, opts SplitOptions) (string, error)

	// OpenPopup は、画面の中央のポップアップを開き、中でコマンドを起動する（FR-67）。
	// コマンドが終わるとポップアップは閉じる。開いた後は待たずに戻る。
	OpenPopup(ctx context.Context, opts PopupOptions) error

	// ClosePopup は、OpenPopup で開いたポップアップを外から閉じる。
	// target は、OpenPopup の PopupOptions.Target と同じものを渡す。
	ClosePopup(ctx context.Context, target string) error

	// FocusPane は、ペインへフォーカスを移す（FR-63）。ペインが別の window（タブ）にあれば、その window（タブ）へ移る。
	FocusPane(ctx context.Context, pane string) error

	// ClosePane は、ペインを閉じる。
	ClosePane(ctx context.Context, pane string) error
}

// Detect は、環境変数から端末多重化ソフトを判定し、その実装を返す。
// getenv には os.Getenv を渡す。テストでは偽の環境を渡す。
//
//   - TMUX と TMUX_PANE があれば tmux（tmux のソケットのパスは TMUX の最初のカンマの前）
//   - HERDR_ENV が 1 で、HERDR_PANE_ID と HERDR_SOCKET_PATH があれば herdr
//   - どれでもなければ、どの操作も ErrUnsupported を返す実装
func Detect(getenv func(string) string) Mux {
	if tmux, pane := getenv("TMUX"), getenv("TMUX_PANE"); tmux != "" && pane != "" {
		socket, _, _ := strings.Cut(tmux, ",")
		return &Tmux{Bin: "tmux", Socket: socket, Pane: pane}
	}
	if getenv("HERDR_ENV") == "1" {
		if pane, socket := getenv("HERDR_PANE_ID"), getenv("HERDR_SOCKET_PATH"); pane != "" && socket != "" {
			return &Herdr{Socket: socket, Pane: pane}
		}
	}
	return None{}
}

// None は、端末多重化ソフトの外（FR-60）の実装である。Kind を除くどの操作も ErrUnsupported を返す。
type None struct{}

var _ Mux = None{}

// Kind は KindNone を返す。
func (None) Kind() Kind { return KindNone }

// CurrentPane は ErrUnsupported を返す。
func (None) CurrentPane(context.Context) (string, error) { return "", ErrUnsupported }

// Unit は ErrUnsupported を返す。
func (None) Unit(context.Context) (string, error) { return "", ErrUnsupported }

// SplitPane は ErrUnsupported を返す。
func (None) SplitPane(context.Context, SplitOptions) (string, error) { return "", ErrUnsupported }

// OpenPopup は ErrUnsupported を返す。
func (None) OpenPopup(context.Context, PopupOptions) error { return ErrUnsupported }

// ClosePopup は ErrUnsupported を返す。
func (None) ClosePopup(context.Context, string) error { return ErrUnsupported }

// FocusPane は ErrUnsupported を返す。
func (None) FocusPane(context.Context, string) error { return ErrUnsupported }

// ClosePane は ErrUnsupported を返す。
func (None) ClosePane(context.Context, string) error { return ErrUnsupported }

// shellJoin は、args の各要素を POSIX のシェルの単一引用で囲み、空白でつないだ 1 つのコマンドの文字列にする。
// 引用の要らない文字だけの要素は囲まない。
func shellJoin(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = shellQuote(a)
	}
	return strings.Join(quoted, " ")
}

// shellQuote は、s を POSIX のシェルで 1 つの語として読まれる形にする。
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		if !isShellSafe(r) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// isShellSafe は、r がシェルの引用なしで書いてよい文字かを返す。
// = は含めない。先頭の語の a=b は、シェルが環境変数の代入として読むためである。
func isShellSafe(r rune) bool {
	switch {
	case 'a' <= r && r <= 'z', 'A' <= r && r <= 'Z', '0' <= r && r <= '9':
		return true
	}
	return strings.ContainsRune("-_./:@%+,", r)
}

// checkCommand は、起動するコマンドが空でないことを確かめる。
func checkCommand(c Command) error {
	if len(c.Args) == 0 {
		return errors.New("mux: empty command")
	}
	return nil
}

// checkPercent は、百分率の指定が 0〜100 であることを確かめる。
func checkPercent(name string, v int) error {
	if v < 0 || v > 100 {
		return errors.New("mux: " + name + " must be between 0 and 100")
	}
	return nil
}
