package editor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/piplupflipper/vi-imitation/ext"
	"github.com/piplupflipper/vi-imitation/internal/config"
	"github.com/piplupflipper/vi-imitation/internal/option"
	"github.com/piplupflipper/vi-imitation/internal/ui"
	"github.com/piplupflipper/vi-imitation/internal/version"
)

// 起動の案内（Vim と Neovim の :intro の画面。FR-205〜FR-207。基本設計 7.2 の「起動の案内」）。
//
// 起動の案内は、描くたびに（bufferFrame の uiIntro）次のすべてを満たすかを確かめ、満たす間だけ編集の client の画面に出す（FR-205）。
// 条件は、Neovim 0.12.5 の may_show_intro（src/nvim/version.c）を本体の言葉に写したものである。
//
//   - 設定 intro.show が真である（Neovim の 'shortmess' に I が無い。FR-206）
//   - 繋いでいる編集の client が 1 つである（Neovim の one_window。2 つ目の client のペインは、窓を分けたのに当たる）
//   - その client が、端末多重化ソフトのペインに部品を開いておらず（pane/open。FR-115）、出力の欄も開いていない
//     （どちらも Neovim では窓を分けることになる。出力の欄は :copen の窓に当たる）
//   - カーソルのあるバッファ（フォーカスのあるポップアップを開いていれば、そのバッファ）が、エディタが最初に作ったバッファである
//     （Neovim の curbuf->handle == 1 と、curwin が最初の窓であること）
//   - そのバッファに名前が無く（Neovim の b_fname == NULL）、中身が空である（行が 1 つで、その行が空。Neovim の buf_is_empty）
//
// 一度でも満たさなかったら、その後は満たしても出さない（introState.off。Neovim の update_screen の still_may_intro）。
// このため、文字を打ってアンドゥで空に戻しても、窓を分けて閉じても、ファイルを開いてから最初のバッファへ戻っても、もう出ない。
// インサートモードに入る、Ex コマンドを打つ、メッセージを出す、端末の大きさを変える、フォーカスの無いポップアップ（ホバー）を出すのは、
// 上の条件を崩さないので消す契機にならない（Neovim 0.12.5 と同じ。Neovim の one_window は浮かべた窓を数えない）。
// 消える契機を Neovim 0.12.5 の nvim --clean で確かめた結果は、基本設計 7.2 の「起動の案内」にある。
//
// :intro（FR-207）は、上の条件によらず、打った client の画面に、表示領域を空白で塗ってから案内を出し、その client の次のキーを 1 つ待つ。
// そのキーは案内を消すだけで、コマンドとしては使わない（Neovim 0.12.5 の ex_intro が plain_vgetc で 1 文字を読んで捨てるのと同じ）。
// 待つのは利用者の端末のキーだけで、マクロの再生の残りのキーは待たない（再生は handleKey を通らない）。

// introExtension は、コマンドラインの表（exCommands）で :intro に入れる拡張機能の ID の代わりの値である（messagesExtension と同じ形）。
const introExtension = " intro"

// introCommandNames は、:intro として受けるコマンドの名前である（Vim と同じく :int まで縮められる）。
var introCommandNames = []string{"int", "intr", "intro"}

// firstBufferID は、エディタが最初に作るバッファの ID である（Editor.nextID の初めの値）。
// 起動の引数のファイルが無ければ、起動したときの名前の無い空のバッファがこの ID になる。
const firstBufferID ext.BufferID = 1

// introState は、起動の案内の状態である（このファイルの doc）。
type introState struct {
	// off は、起動の案内を出す条件が一度崩れたことを表す。立てたら下ろさない。
	off bool
	// forced は、:intro で案内を出している client である。その client の次のキーで nil に戻す（dismissIntro）。
	forced *clientView
	// lines は、案内の文面の覚えである。最初に出すときに作る（設定ファイルがあるかを確かめるのを 1 回にするため）。
	lines []string
}

// addIntroCommand は、コマンドラインの表に :intro を足す。拡張機能が同じ ID のコマンドを宣言していれば、本体の :intro を優先し、誤りを返す。
func addIntroCommand(cmds map[string]string) []error {
	var errs []error
	for _, name := range introCommandNames {
		if prev, ok := cmds[name]; ok {
			errs = append(errs, fmt.Errorf("拡張機能 %s のコマンド %s は本体の :intro と重なるので、コマンドラインからは呼べない", prev, name))
		}
		cmds[name] = introExtension
	}
	return errs
}

// showIntro は、:intro で、操作中の client の画面に起動の案内を出す（FR-207。このファイルの doc）。
// 引数は取らず、付ければ E488 にする（Vim 9.1 と Neovim 0.12.5 の :intro x と同じ）。
//
// req: FR-207
func (e *Editor) showIntro(args string) error {
	if args = strings.TrimSpace(args); args != "" {
		return fmt.Errorf("E488: 余分な文字があります: %s", args)
	}
	e.intro.forced = e.op
	return nil
}

// dismissIntro は、操作中の client に :intro の案内を出していれば消して true を返す（FR-207）。
// handleKey は、true ならそのキーをコマンドとして使わない。
//
// req: FR-207
func (e *Editor) dismissIntro() bool {
	if e.intro.forced == nil || e.intro.forced != e.op {
		return false
	}
	e.intro.forced = nil
	return true
}

// uiIntro は、読み込んでいる client（e.view）の画面に出す起動の案内（ui.Frame.Intro）を返す。出さなければ nil を返す。
// 描くたびに、起動の案内を出す条件（mayShowIntro）を確かめ、崩れていたら、その後は出さない（このファイルの doc）。
// :intro の案内（FR-207）は、条件によらず、打った client の画面に出す。
// 使うのは bufferFrame だけである（ポップアップの窓の中と部品の client の画面には出さない）。
//
// req: FR-205, FR-206
func (e *Editor) uiIntro() *ui.Intro {
	if !e.intro.off && !e.mayShowIntro() {
		e.intro.off = true
	}
	switch {
	case e.intro.forced != nil && e.intro.forced == e.view:
		return &ui.Intro{Lines: e.introText(), Clear: true}
	case !e.intro.off:
		return &ui.Intro{Lines: e.introText()}
	}
	return nil
}

// mayShowIntro は、起動の案内を出す条件（このファイルの doc の箇条書き）を、読み込んでいる client について確かめる。
func (e *Editor) mayShowIntro() bool {
	bs := e.cur
	return e.options.Get(option.IntroShow).Bool() &&
		len(e.clients) == 1 && e.clients[0] == e.view && len(e.panes) == 0 && e.outView == nil &&
		bs != nil && bs.id == firstBufferID && bs.path == "" && !bs.popup &&
		bs.buf.LineCount() == 1 && bs.buf.Line(0) == ""
}

// introText は、起動の案内の文面を返す。最初に呼んだときに作って覚える（introState.lines）。
func (e *Editor) introText() []string {
	if e.intro.lines == nil {
		e.intro.lines = introLines(version.String(), e.introConfig())
	}
	return e.intro.lines
}

// introConfigFile は、起動の案内に出す設定ファイルの場所である（introLines の入力）。
type introConfigFile struct {
	// path は、設定ファイルのパスである。ホームディレクトリの下なら "~/" で始まる形にしてある（shortHome）。
	// 場所を決められなかったら空である。
	path string
	// exists は、path にファイルがあることを表す。
	exists bool
}

// introConfig は、起動のときに読む設定ファイルの場所（cmd/vi-imitation の loadConfig と同じく config.DefaultPath。
// :ext sync と同じく、Config.ExtManager.ConfigPath があればそれ）と、そこにファイルがあるかを返す。
func (e *Editor) introConfig() introConfigFile {
	path := e.cfg.ExtManager.ConfigPath
	if path == "" {
		p, err := config.DefaultPath()
		if err != nil {
			return introConfigFile{}
		}
		path = p
	}
	_, err := os.Stat(path)
	return introConfigFile{path: shortHome(path, os.Getenv("HOME")), exists: err == nil || !errors.Is(err, os.ErrNotExist)}
}

// shortHome は、path がホームディレクトリ home の下なら、home の部分を "~" にした形を返す（Neovim の fnamemodify の :~ と同じ）。
// home が空か、下でなければ path をそのまま返す。案内に、手元のユーザー名を含むパスをそのまま出さないためでもある。
func shortHome(path, home string) string {
	if home == "" {
		return path
	}
	home = filepath.Clean(home)
	if rel, err := filepath.Rel(home, path); err == nil && rel != ".." && !strings.HasPrefix(rel, "../") && !filepath.IsAbs(rel) {
		if rel == "." {
			return "~"
		}
		return "~/" + rel
	}
	return path
}

// introLines は、起動の案内の文面である（仮。オーナーが後で決める。#355）。文面を変えるときは、この関数だけを変える。
// 中身は、名前と版、:q で終わる案内、:help の案内、設定ファイルの場所の 4 つである（#355 の本文）。
// ui の drawIntro が行ごとに表示領域の中央へ寄せて描くので、列をそろえたい行（コマンドの 2 行）は、空白で表示幅をそろえる。
// 案内の全体が表示領域に余白付きで収まらなければ出さない（drawIntro）ので、行を増やしたり広げたりすると、狭い端末で出なくなる。
// 設定ファイルのパスを見出しと別の行にするのは、パスの分だけ広い 1 行にして、狭い端末で案内の全体が出なくなるのを避けるためである
// （既定の置き場所の ~/.config/vi-imitation/config.jsonc は 35 列で、案内は 37 列の端末から出る）。
func introLines(ver string, cfg introConfigFile) []string {
	conf := []string{"設定ファイルの場所を決められない", "（XDG_CONFIG_HOME と HOME が無い）"}
	switch {
	case cfg.path != "" && cfg.exists:
		conf = []string{"設定ファイル", cfg.path}
	case cfg.path != "":
		conf = []string{"設定ファイル（まだ無い）", cfg.path}
	}
	return append([]string{
		"vi-imitation " + ver,
		"",
		":q<Enter>     で終わる      ",
		":help<Enter>  でヘルプを開く",
		"",
	}, conf...)
}
