// Package docs は、本体の実行ファイルに埋め込む文書を持つ（FR-89、FR-172。基本設計 7.2）。
//
// 埋め込み（embed）はパッケージのディレクトリの外のファイルを読めないので、文書と同じディレクトリにこのパッケージを置く。
// 他のパッケージを import しない。
//
// agent/ の文書の一覧の部分（印 <!-- generated:<名前>:begin --> と end の間）は、cmd/docgen がコードの表から生成する。
// 表を変えたら go generate ./docs（make docs）で生成し直す。生成し直し忘れると make check が失敗する。
package docs

import (
	_ "embed" // 埋め込みのため
	"regexp"
	"strings"
)

//go:generate go run ../cmd/docgen -root ..

// Usage は、docs/usage.md の中身である。:help が、ファイルを読まずに出す（internal/editor の help.go）。
//
//go:embed usage.md
var Usage string

var (
	//go:embed agent/config.md
	agentConfig string
	//go:embed agent/keymap.md
	agentKeymap string
	//go:embed agent/commands.md
	agentCommands string
	//go:embed agent/extensions.md
	agentExtensions string
	//go:embed agent/extension-types.md
	agentExtensionTypes string
	//go:embed agent/script.md
	agentScript string
	//go:embed agent/script-symbols.md
	agentScriptSymbols string
	//go:embed debugging.md
	debugging string
)

// Topic は、vi-imitation --docs で出す文書の 1 つの話題である。
type Topic struct {
	// Name は、話題の名前（vi-imitation --docs の引数）である。
	Name string
	// File は、文書のファイルの、リポジトリの docs/ からの相対パスである。
	File string
	// Summary は、話題の一覧に出す 1 行の説明である。
	Summary string
	// Text は、文書の中身である。1 行だけの HTML のコメント（生成の印など）の行は除いてある。
	Text string
}

// Topics は、話題を一覧の順に返す。
// AI エージェントが要る話題だけを読めるように、設定、キーの割り当て、Ex コマンド、拡張機能、拡張機能の型に分け、
// デバッグの案内（docs/debugging.md）と :help の文書（docs/usage.md）を並べる。
//
// req: FR-172
func Topics() []Topic {
	return []Topic{
		{"config", "agent/config.md", "設定ファイルの置き場所と書き方、最上位の項目と設定の項目（型、範囲、既定値）の一覧", strip(agentConfig)},
		{"keymap", "agent/keymap.md", "keymap の書き方と、操作の ID（種類、モード、既定のキー、説明）の一覧", strip(agentKeymap)},
		{"commands", "agent/commands.md", "Ex コマンドの名前と省略形の一覧と、keymap と commandAliases から Ex コマンドを呼ぶ書き方", strip(agentCommands)},
		{"extensions", "agent/extensions.md", "拡張機能の作り方、宣言ファイルの項目、起動の契機、環境変数、メソッドの一覧", strip(agentExtensions)},
		{"extension-types", "agent/extension-types.md", "拡張機能 API のメソッドの params と result の型の項目（型の名前を節に書いて 1 つずつ読む）", strip(agentExtensionTypes)},
		{"script", "agent/script.md", "スクリプトの拡張機能の例、import できるパッケージ、ext/server の API", strip(agentScript)},
		{"script-symbols", "agent/script-symbols.md", "スクリプトから使える標準ライブラリの記号（import のパスを節に書いて 1 つずつ読む）", strip(agentScriptSymbols)},
		{"debugging", "debugging.md", "動かないとき、画面が崩れるときに調べる手順と、不具合の報告に添えるもの", strip(debugging)},
		{"usage", "usage.md", ":help の文書の全体（大きいので、節の名前を書いて 1 節ずつ読む）", strip(Usage)},
	}
}

// LookupTopic は、名前が name の話題を返す。無ければ ok は false である。
func LookupTopic(name string) (Topic, bool) {
	for _, t := range Topics() {
		if t.Name == name {
			return t, true
		}
	}
	return Topic{}, false
}

// commentLineRE は、1 行だけの HTML のコメントの行である（生成の印 <!-- generated:options:begin --> など）。
var commentLineRE = regexp.MustCompile(`(?m)^<!--.*-->\n`)

// strip は、文書から 1 行だけの HTML のコメントの行を除く。端末に出す文書のトークンを減らすためである。
func strip(text string) string {
	return commentLineRE.ReplaceAllString(text, "")
}

// Heading は、Markdown の文書の見出しの 1 つである。
type Heading struct {
	// Level は、見出しの段（# の数）である。
	Level int
	// Title は、# と空白の後の見出しの文字列である。
	Title string
	// Line は、見出しの行の番号（0 始まり）である。
	Line int
}

// Headings は、Markdown の文書 text の見出しを、出てくる順に返す。コードの囲み（```）の中の # で始まる行は見出しにしない。
func Headings(text string) []Heading {
	var out []Heading
	fence := false
	for i, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(l, "```") {
			fence = !fence
			continue
		}
		if fence {
			continue
		}
		level := len(l) - len(strings.TrimLeft(l, "#"))
		if level == 0 || level > 6 || !strings.HasPrefix(l[level:], " ") {
			continue
		}
		out = append(out, Heading{Level: level, Title: strings.TrimSpace(l[level:]), Line: i})
	}
	return out
}

// Section は、Markdown の文書 text の、見出しが title の節を返す。
// 節は、見出しの行から、同じかより上の段の次の見出しの手前までである（下の段の見出しの節を含む）。
// 見出しは、そのままの文字列か、` を除いた文字列で比べる（`<Leader>` と `leader` の節は <Leader> と leader でも引ける）。
// 同じ見出しが複数あれば、最初のものを返す。見つからなければ ok は false である。
func Section(text, title string) (string, bool) {
	hs := Headings(text)
	plain := strings.ReplaceAll(title, "`", "")
	for i, h := range hs {
		if h.Title != title && strings.ReplaceAll(h.Title, "`", "") != plain {
			continue
		}
		lines := strings.Split(text, "\n")
		end := len(lines)
		for _, n := range hs[i+1:] {
			if n.Level <= h.Level {
				end = n.Line
				break
			}
		}
		return strings.TrimRight(strings.Join(lines[h.Line:end], "\n"), "\n") + "\n", true
	}
	return "", false
}
