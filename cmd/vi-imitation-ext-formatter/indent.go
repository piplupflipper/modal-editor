package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/tailscale/hujson"
)

// 整形ツールの設定ファイルからのインデント（基本設計 6.10。#349。FR-193）。
//
// バッファのファイルの種類に設定した整形ツール（formatters の fileTypes）のうち、biome、prettier、ruff について、
// その整形ツールが使う設定ファイルを、ファイルのディレクトリから上へ探して読み、インデントの文字（タブかスペースか）と幅を、
// 本体の設定の項目（indent.useSpaces、indent.width、tab.width）にして option/set で渡す。
// 本体はそれを .editorconfig の後の段に入れるので、整形ツールの設定は .editorconfig に勝ち、利用者の :set には負ける（基本設計 7.3）。
//
// 設定ファイルの探し方は、それぞれの整形ツールに合わせた。どれも Git の根で止めず、ファイルシステムの根まで探す
// （2026-11-22 に biome 2.4.15 と ruff 0.15.16 で、Git の根より上の設定ファイルを使うことを確かめた）。
//
// 小さな読み手で扱えない書き方（biome と prettier の overrides の字下げ、biome の "//" でない extends、ruff の extend、
// prettier の JavaScript の設定ファイルなど）は、整形ツールと食い違う値を送らないように、読めない設定ファイル（configError）にする
// （PR #352 のレビューの推奨 2 へのオーケストレータの決め）。呼び出し側は何も送らず、1 回だけ知らせる。

// indentNames は、この拡張機能が option/set で入れる項目の名前である。値の無い項目は null で取り下げる。
var indentNames = []string{"indent.useSpaces", "indent.width", "tab.width"}

// indentConfig は、1 つの整形ツールの設定ファイルから読んだインデントである。書いていない項目は nil。
type indentConfig struct {
	useTabs *bool
	width   *int
}

// merge は、c に o の書いた項目を重ねる（o が勝つ）。
func (c indentConfig) merge(o indentConfig) indentConfig {
	if o.useTabs != nil {
		c.useTabs = o.useTabs
	}
	if o.width != nil {
		c.width = o.width
	}
	return c
}

// options は、c を本体の設定の項目の値にする。.editorconfig の写し方（基本設計 7.3。Neovim 0.12.5 の editorconfig.lua）と同じにした。
// タブなら indent.useSpaces を false に、幅が無ければ indent.width を 0（tab.width に従う）にする。スペースなら indent.useSpaces を true にする。
// 幅は indent.width と tab.width の両方にする（biome の indentWidth、prettier の tabWidth、ruff の indent-width は、
// どれもタブ文字の幅にも使われる）。書いていない項目は null（取り下げ）にする。
func (c indentConfig) options() map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for _, n := range indentNames {
		out[n] = json.RawMessage("null")
	}
	if c.useTabs != nil {
		out["indent.useSpaces"] = json.RawMessage(fmt.Sprint(!*c.useTabs))
		if *c.useTabs && c.width == nil {
			out["indent.width"] = json.RawMessage("0")
		}
	}
	if c.width != nil {
		w := json.RawMessage(fmt.Sprint(*c.width))
		out["indent.width"], out["tab.width"] = w, w
	}
	return out
}

// launchers は、パッケージの実行ファイルを取ってきて起動するコマンド（npx prettier、uvx ruff など）である。
// args の始めの、これらの下位コマンド（launcherSubcommands）と - で始まる引数を除いた最初の引数を、整形ツールの名前とみなす。
var launchers = []string{"npx", "pnpx", "bunx", "uvx", "pnpm", "yarn", "bun", "uv", "pipx"}

// launcherSubcommands は、launchers の後に書く下位コマンド（pnpm dlx、uv tool run、pipx run など）である。
var launcherSubcommands = []string{"dlx", "exec", "x", "run", "tool"}

// toolKind は、整形ツールの設定 f が、設定ファイルを読める整形ツール（biome、prettier、ruff）のどれかを返す。どれでもなければ空である。
//
// 次の順に見て、最初に当たったもので決める。prettierd は prettier の設定ファイルを読むので prettier にする。
//
//  1. name（省けば command のファイル名）
//  2. command のファイル名
//  3. command が launchers（npx、uvx など）なら、args の最初の整形ツールの名前（版の @3.6.2 と、@biomejs/biome のような scope を除く）
//
// npx などで起動して、引数からも分からない書き方では、name に biome、prettier、ruff のどれかを書く（docs/usage.md。PR #352 のレビューの推奨 8）。
func toolKind(f formatterConfig) string {
	names := []string{f.Name, filepath.Base(f.Command)}
	if slices.Contains(launchers, filepath.Base(f.Command)) {
		for _, a := range f.Args {
			if strings.HasPrefix(a, "-") || slices.Contains(launcherSubcommands, a) {
				continue
			}
			names = append(names, packageName(a))
			break
		}
	}
	for _, n := range names {
		switch strings.ToLower(n) {
		case "biome":
			return "biome"
		case "prettier", "prettierd":
			return "prettier"
		case "ruff":
			return "ruff"
		}
	}
	return ""
}

// packageName は、パッケージの指定（prettier@3.6.2、@biomejs/biome@2.4.15、ruff==0.15.16）から、実行ファイルに当たる名前を取り出す。
func packageName(spec string) string {
	scoped := strings.HasPrefix(spec, "@")
	s := strings.TrimPrefix(spec, "@")
	if i := strings.IndexAny(s, "@=<>~"); i >= 0 {
		s = s[:i]
	}
	if scoped {
		if _, after, ok := strings.Cut(s, "/"); ok {
			s = after
		}
	}
	return s
}

// projectIndent は、ファイル path（ファイルの種類 ft）のインデントを、ft に設定した整形ツールの設定ファイルから読む。
// 設定の順に整形ツールを見て、後の整形ツールの値を重ねる（後の整形ツールの出力が最後に残るため）。
// 読んだ設定ファイルのパスを sources に返す。どれかの設定ファイルを読めなければ、その誤り（*configError）を返す。
//
// req: FR-193
func (c *config) projectIndent(path, ft string) (out indentConfig, sources []string, err error) {
	seen := map[string]bool{}
	dir := filepath.Dir(path)
	for _, f := range c.forFileType(ft) {
		kind := toolKind(f)
		if kind == "" || seen[kind] {
			continue
		}
		seen[kind] = true
		var ic indentConfig
		var srcs []string
		switch kind {
		case "biome":
			ic, srcs, err = biomeIndent(path, ft)
		case "prettier":
			ic, srcs, err = prettierIndent(path)
		case "ruff":
			ic, srcs, err = ruffIndent(dir)
		}
		if err != nil {
			return indentConfig{}, nil, err
		}
		out = out.merge(ic)
		sources = append(sources, srcs...)
	}
	return out, sources, nil
}

// configError は、整形ツールの設定ファイルを読めないことを表す。
type configError struct {
	path string
	err  error
}

func (e *configError) Error() string {
	return fmt.Sprintf("整形ツールの設定 %s を読めないので、インデントに使わない: %v", e.path, e.err)
}

func (e *configError) Unwrap() error { return e.err }

// isFile は、path が通常のファイル（シンボリックリンクの先を含む）かを返す。
func isFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}

// readConfigFile は、設定ファイル path を読む。
func readConfigFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, &configError{path: path, err: err}
	}
	return data, nil
}

// parseJSONC は、注釈と末尾のカンマを許す JSON（biome.jsonc など）を読む。
func parseJSONC(data []byte, v any) error {
	std, err := hujson.Standardize(bytes.TrimPrefix(data, []byte(bom)))
	if err != nil {
		return err
	}
	return json.Unmarshal(std, v)
}

// widthOf は、設定ファイルの幅の値 v（JSON の数か、TOML と YAML の整数）を、本体の tab.width の範囲（1 から 9999）の整数にする。
func widthOf(name string, v any) (*int, error) {
	var n int
	switch x := v.(type) {
	case float64:
		if x != float64(int(x)) {
			return nil, fmt.Errorf("%s は整数にする: %v", name, x)
		}
		n = int(x)
	case int64:
		n = int(x)
	default:
		return nil, fmt.Errorf("%s は整数にする", name)
	}
	if n < 1 || n > 9999 {
		return nil, fmt.Errorf("%s は 1 から 9999 の整数にする: %d", name, n)
	}
	return &n, nil
}

// styleOf は、インデントの文字の値 v を読む。tabWord ならタブ、spaceWord ならスペースである。
func styleOf(name string, v any, tabWord, spaceWord string) (*bool, error) {
	switch v {
	case tabWord:
		return boolPtr(true), nil
	case spaceWord:
		return boolPtr(false), nil
	}
	return nil, fmt.Errorf("%s は %q か %q にする: %v", name, tabWord, spaceWord, v)
}

// boolPtr は、b のポインターを返す。
func boolPtr(b bool) *bool { return &b }

// biomeLanguage は、ファイルの種類から、biome の言語ごとの設定の項目の名前（"javascript" の "javascript.formatter"）を引く。
var biomeLanguage = map[string]string{
	"javascript": "javascript", "javascriptreact": "javascript", "typescript": "javascript", "typescriptreact": "javascript",
	"json": "json", "jsonc": "json", "css": "css", "graphql": "graphql", "html": "html",
}

// biomeFiles は、biome が 1 つのディレクトリで設定を探す順である。
// 2026-11-22 に biome 2.4.15 で、biome.json、biome.jsonc、.biome.json、.biome.jsonc の順に勝つことを確かめた（PR #352 のレビューの推奨 1）。
var biomeFiles = []string{"biome.json", "biome.jsonc", ".biome.json", ".biome.jsonc"}

// biomeIndent は、ファイル file（ファイルの種類 ft）について、biome の設定ファイルを読む。
//
// ファイルのディレクトリから上へ、各ディレクトリで biomeFiles の順に探し、最初に見つけたもの（近い設定）を使う。
// formatter.indentStyle（tab か space）と formatter.indentWidth を読み、ファイルの種類の言語の <言語>.formatter に書いた項目を重ねる。
// その上に、file に当たる overrides を順に重ねる（overrideMatch で当たるかを決められないものは configError）。
// extends（biomeExtends）は、近い設定より先に当てる。
// "extends" の無い近い設定は、"root": false でも上の設定を引き継がない。
// 2026-11-22 に biome 2.4.15 の format --stdin-file-path=<絶対パス>（この拡張機能の設定の例と同じ渡し方）で、これらを確かめた
// （PR #352 のレビューの推奨 2 と、再レビューの推奨 2、3、4）。
func biomeIndent(file, ft string) (indentConfig, []string, error) {
	path, ok := findUp(filepath.Dir(file), biomeFiles, isFile, nil)
	if !ok {
		return indentConfig{}, nil, nil
	}
	doc, err := readBiome(path)
	if err != nil {
		return indentConfig{}, nil, err
	}
	own, err := biomeDocIndent(path, doc, file, ft, true)
	if err != nil {
		return indentConfig{}, nil, err
	}
	base, srcs, err := biomeExtends(path, doc, file, ft)
	if err != nil {
		return indentConfig{}, nil, err
	}
	return base.merge(own), append([]string{path}, srcs...), nil
}

// biomeExtends は、biome の設定ファイル path（中身 doc）の extends の設定を、近い設定より先に当てる値にする。
//
//   - "//": 上へ探した根の設定（"root": false でない設定）。近い設定と根の設定の間に、別の "root": false の設定があれば
//     （3 段の入れ子）、biome の決め方を確かめていないので configError にする（PR #352 の再レビューの推奨 3。biome 2.4.15 は、
//     その間の設定を使うように見える）
//   - 配列: 書いた順に当てる設定ファイル（後のものが勝つ）。相対パスか絶対パスの .json と .jsonc だけを読み、
//     共有の設定（パッケージの名前）は configError にする。空の配列は extends が無いのと同じである
//
// extends の先の設定がさらに extends を持つもの、extends の先の overrides でインデントを変えているものは、当てる順を確かめていないので
// configError にする。
func biomeExtends(path string, doc map[string]json.RawMessage, file, ft string) (indentConfig, []string, error) {
	raw, ok := doc["extends"]
	if !ok {
		return indentConfig{}, nil, nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if s != "//" {
			return indentConfig{}, nil, &configError{path: path, err: fmt.Errorf("extends の %q を読めない", s)}
		}
		var rootErr error
		var rootDoc map[string]json.RawMessage
		var middle string
		rootPath, found := findUp(filepath.Dir(filepath.Dir(path)), biomeFiles, func(p string) bool {
			if !isFile(p) {
				return false
			}
			d, rerr := readBiome(p)
			if rerr != nil {
				rootErr = rerr
				return true
			}
			if r, ok := d["root"]; ok && string(bytes.TrimSpace(r)) == "false" {
				middle = p
				return false
			}
			rootDoc = d
			return true
		}, nil)
		switch {
		case rootErr != nil:
			return indentConfig{}, nil, rootErr
		case middle != "":
			return indentConfig{}, nil, &configError{path: path, err: fmt.Errorf("根の設定との間に、ほかの入れ子の設定 %s がある（3 段の入れ子は読めない）", middle)}
		case !found:
			return indentConfig{}, nil, nil
		}
		root, err := biomeBase(rootPath, rootDoc, file, ft)
		return root, []string{rootPath}, err
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err != nil {
		return indentConfig{}, nil, &configError{path: path, err: fmt.Errorf("extends を読めない: %w", err)}
	}
	var out indentConfig
	var srcs []string
	for _, e := range list {
		local := strings.HasPrefix(e, "./") || strings.HasPrefix(e, "../") || filepath.IsAbs(e)
		if ext := filepath.Ext(e); !local || ext != ".json" && ext != ".jsonc" {
			return indentConfig{}, nil, &configError{path: path, err: fmt.Errorf("extends の %q（共有の設定）は読めない", e)}
		}
		target := e
		if !filepath.IsAbs(e) {
			target = filepath.Join(filepath.Dir(path), e)
		}
		d, err := readBiome(target)
		if err != nil {
			return indentConfig{}, nil, err
		}
		ic, err := biomeBase(target, d, file, ft)
		if err != nil {
			return indentConfig{}, nil, err
		}
		out = out.merge(ic)
		srcs = append(srcs, target)
	}
	return out, srcs, nil
}

// biomeBase は、extends の先の設定ファイル path（中身 doc）からインデントを読む。さらに extends を持つものは configError にする。
func biomeBase(path string, doc map[string]json.RawMessage, file, ft string) (indentConfig, error) {
	if _, ok := doc["extends"]; ok {
		return indentConfig{}, &configError{path: path, err: errors.New("extends の先の設定の extends は読めない")}
	}
	return biomeDocIndent(path, doc, file, ft, false)
}

// readBiome は、biome の設定ファイル path を、最上位の項目から JSON への対応にする。
func readBiome(path string) (map[string]json.RawMessage, error) {
	data, err := readConfigFile(path)
	if err != nil {
		return nil, err
	}
	var doc map[string]json.RawMessage
	if err := parseJSONC(data, &doc); err != nil {
		return nil, &configError{path: path, err: err}
	}
	return doc, nil
}

// biomeFormatter は、biome の formatter の項目の JSON（where はその位置の名前）からインデントを読む。
func biomeFormatter(where string, raw json.RawMessage) (indentConfig, error) {
	var f map[string]any
	if err := json.Unmarshal(raw, &f); err != nil {
		return indentConfig{}, fmt.Errorf("%s: %w", where, err)
	}
	var ic indentConfig
	if v, ok := f["indentStyle"]; ok {
		t, err := styleOf(where+".indentStyle", v, "tab", "space")
		if err != nil {
			return indentConfig{}, err
		}
		ic.useTabs = t
	}
	if v, ok := f["indentWidth"]; ok {
		w, err := widthOf(where+".indentWidth", v)
		if err != nil {
			return indentConfig{}, err
		}
		ic.width = w
	}
	return ic, nil
}

// biomeSection は、biome の設定の部分（最上位か、overrides の 1 つ）doc の、formatter と ft の言語の formatter を読む。
// hasIndent は、どちらかにインデントの項目（indentStyle か indentWidth）を書いたかである。
func biomeSection(where string, doc map[string]json.RawMessage, ft string) (ic indentConfig, hasIndent bool, err error) {
	read := func(w string, raw json.RawMessage) error {
		x, err := biomeFormatter(w, raw)
		if err != nil {
			return err
		}
		hasIndent = hasIndent || x != (indentConfig{})
		ic = ic.merge(x)
		return nil
	}
	if raw, ok := doc["formatter"]; ok {
		if err := read(where+"formatter", raw); err != nil {
			return indentConfig{}, false, err
		}
	}
	for _, lang := range []string{"javascript", "json", "css", "graphql", "html"} {
		raw, ok := doc[lang]
		if !ok {
			continue
		}
		var section struct {
			Formatter json.RawMessage `json:"formatter"`
		}
		if err := json.Unmarshal(raw, &section); err != nil {
			return indentConfig{}, false, fmt.Errorf("%s%s: %w", where, lang, err)
		}
		if section.Formatter == nil {
			continue
		}
		x, err := biomeFormatter(where+lang+".formatter", section.Formatter)
		if err != nil {
			return indentConfig{}, false, err
		}
		hasIndent = hasIndent || x != (indentConfig{})
		if lang == biomeLanguage[ft] {
			ic = ic.merge(x)
		}
	}
	return ic, hasIndent, nil
}

// biomeDocIndent は、biome の設定ファイル path の中身 doc から、ファイル file のインデントを読む。
// apply が true なら、file に当たる overrides のインデントを順に重ねる（後のものが勝つ。2026-11-22 に biome 2.4.15 で確かめた）。
// overrides の includes は path のディレクトリからの相対パスに当て（overrideMatch。biome の *.js はそのディレクトリの直下のファイルにだけ当たる）、
// 当たるかを決められないもので、インデントを変えているものは configError にする。当たらないと分かるものは読み飛ばす。
// apply が false（extends の先の設定）なら、インデントを変えている overrides は、当てる順を確かめていないので configError にする。
func biomeDocIndent(path string, doc map[string]json.RawMessage, file, ft string, apply bool) (indentConfig, error) {
	ic, _, err := biomeSection("", doc, ft)
	if err != nil {
		return indentConfig{}, &configError{path: path, err: err}
	}
	raw, ok := doc["overrides"]
	if !ok {
		return ic, nil
	}
	var overrides []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &overrides); err != nil {
		return indentConfig{}, &configError{path: path, err: fmt.Errorf("overrides: %w", err)}
	}
	rel := relSlash(filepath.Dir(path), file)
	for i, o := range overrides {
		x, has, err := biomeSection(fmt.Sprintf("overrides[%d].", i), o, ft)
		if err != nil {
			return indentConfig{}, &configError{path: path, err: err}
		}
		if !has {
			continue
		}
		if !apply {
			return indentConfig{}, &configError{path: path, err: fmt.Errorf("extends の先の overrides[%d] でインデントを変えている（読めない）", i)}
		}
		var includes []string
		_, legacy := o["include"]
		if r, ok := o["includes"]; ok && !legacy {
			if err := json.Unmarshal(r, &includes); err != nil {
				return indentConfig{}, &configError{path: path, err: fmt.Errorf("overrides[%d].includes: %w", i, err)}
			}
		}
		match, known := overrideMatch(includes, nil, rel, false)
		switch {
		case !known:
			return indentConfig{}, &configError{path: path, err: fmt.Errorf("overrides[%d] がこのファイルに当たるかを決められない（読めない）", i)}
		case match:
			ic = ic.merge(x)
		}
	}
	return ic, nil
}

// relSlash は、ディレクトリ dir からファイル file への相対パスを、/ で区切って返す。求められなければ file のファイル名を返す。
func relSlash(dir, file string) string {
	rel, err := filepath.Rel(dir, file)
	if err != nil {
		return filepath.Base(file)
	}
	return filepath.ToSlash(rel)
}

// overrideMatch は、overrides の当てるパターン includes（! で始まるものは除くパターン）と除くパターン excludes が、
// 設定ファイルのディレクトリからの相対パス rel に当たるかを返す。決められなければ known が false である。
//
// 決められるのは、*SUF と **/*SUF（SUF は * ? [ ] { } / を含まない）のパターンだけである。**/*SUF は深さを問わずに、
// *SUF は anyDepth（prettier の、/ を含まないパターンはファイル名に当てる決め）なら深さを問わずに、そうでなければ
// rel が / を含まないとき（biome の、設定ファイルのディレクトリの直下）だけ、ファイル名が SUF で終われば当たる。
// 2026-11-22 に biome 2.4.15 と prettier 3.6.2 で、src/a.js に *.js が biome では当たらず、prettier では当たることを確かめた。
// ほかのパターンが 1 つでもあれば、決められないとする。
func overrideMatch(includes, excludes []string, rel string, anyDepth bool) (match, known bool) {
	var pos, neg []string
	for _, p := range includes {
		if n, ok := strings.CutPrefix(p, "!"); ok {
			neg = append(neg, strings.TrimPrefix(n, "!"))
		} else {
			pos = append(pos, p)
		}
	}
	neg = append(neg, excludes...)
	if len(pos) == 0 {
		return false, false
	}
	for _, p := range pos {
		m, k := simpleGlob(p, rel, anyDepth)
		if !k {
			return false, false
		}
		match = match || m
	}
	if !match {
		return false, true
	}
	for _, p := range neg {
		m, k := simpleGlob(p, rel, anyDepth)
		if !k {
			return false, false
		}
		if m {
			return false, true
		}
	}
	return true, true
}

// simpleGlob は、*SUF か **/*SUF のパターン p が rel に当たるかを返す（overrideMatch の doc）。ほかのパターンは known が false である。
func simpleGlob(p, rel string, anyDepth bool) (match, known bool) {
	deep := false
	if rest, ok := strings.CutPrefix(p, "**/"); ok {
		p, deep = rest, true
	}
	suf, ok := strings.CutPrefix(p, "*")
	if !ok || strings.ContainsAny(suf, "*?[]{}/\\!") {
		return false, false
	}
	if !strings.HasSuffix(path.Base(rel), suf) {
		return false, true
	}
	return deep || anyDepth || !strings.Contains(rel, "/"), true
}

// prettierFiles は、prettier が 1 つのディレクトリで設定を探す順である。
// 2026-11-22 に prettier 3.6.2 の --find-config-path で、package.json（prettier の鍵があるときだけ）、package.yaml、.prettierrc、
// .prettierrc.json、.prettierrc.yaml、.prettierrc.yml、.prettierrc.json5、.prettierrc.js、.prettierrc.mjs、.prettierrc.cjs、
// prettier.config.js、prettier.config.mjs、prettier.config.cjs、.prettierrc.toml の順に勝つことを確かめた（PR #352 のレビューの推奨 6）。
// TypeScript の形（.ts、.mts、.cts）は手元の node で読めないので確かめておらず、prettier の文書の並びに入れた。
var prettierFiles = []string{
	"package.json", "package.yaml", ".prettierrc", ".prettierrc.json", ".prettierrc.yaml", ".prettierrc.yml", ".prettierrc.json5",
	".prettierrc.js", ".prettierrc.ts", ".prettierrc.mjs", ".prettierrc.mts", ".prettierrc.cjs", ".prettierrc.cts",
	"prettier.config.js", "prettier.config.ts", "prettier.config.mjs", "prettier.config.mts", "prettier.config.cjs", "prettier.config.cts",
	".prettierrc.toml",
}

// prettierIndent は、prettier の設定ファイルを読む。
//
// ファイルのディレクトリから上へ、各ディレクトリで prettierFiles の順に探し、最初に見つけたものだけを使う
// （prettier の文書の「searching up the file tree until a config file is (or isn't) found」）。
// useTabs と tabWidth を読む。.prettierrc と YAML の形は、ブロックの形と流れの形（{ useTabs: true }）を読む。
// ファイル file に当たる overrides は重ねる（prettierOverrides）。
// 読めない形（JavaScript、TypeScript、JSON5、package.yaml、package.json の共有の設定の名前）と、読めない overrides は configError にする。
func prettierIndent(file string) (indentConfig, []string, error) {
	var readErr error
	path, ok := findUp(filepath.Dir(file), prettierFiles, func(p string) bool {
		if !isFile(p) {
			return false
		}
		if filepath.Base(p) != "package.json" {
			return true
		}
		data, err := readConfigFile(p)
		if err != nil {
			readErr = err
			return true
		}
		var pkg map[string]json.RawMessage
		if err := json.Unmarshal(data, &pkg); err != nil {
			readErr = &configError{path: p, err: err}
			return true
		}
		_, has := pkg["prettier"]
		return has
	}, nil)
	if !ok || readErr != nil {
		return indentConfig{}, nil, readErr
	}
	data, err := readConfigFile(path)
	if err != nil {
		return indentConfig{}, nil, err
	}
	var doc map[string]any
	jsonForm := false
	switch base := filepath.Base(path); base {
	case "package.json":
		var pkg struct {
			Prettier json.RawMessage `json:"prettier"`
		}
		if err = json.Unmarshal(data, &pkg); err == nil {
			if !bytes.HasPrefix(bytes.TrimSpace(pkg.Prettier), []byte("{")) {
				return indentConfig{}, nil, &configError{path: path, err: errors.New("prettier の共有の設定の名前は読めない")}
			}
			err = json.Unmarshal(pkg.Prettier, &doc)
			jsonForm = true
		}
	case ".prettierrc.json":
		err = parseJSONC(data, &doc)
		jsonForm = true
	case ".prettierrc", ".prettierrc.yaml", ".prettierrc.yml":
		// prettier は .prettierrc を YAML として読む（JSON は YAML の流れの形の一部）。JSON で読めれば overrides の中まで見る。
		if err = parseJSONC(data, &doc); err == nil {
			jsonForm = true
		} else {
			doc, err = parseYAMLTop(string(data))
		}
	case ".prettierrc.toml":
		doc, err = parseTOML(string(data))
	default:
		return indentConfig{}, nil, &configError{path: path, err: errors.New("JavaScript、TypeScript、JSON5、package.yaml の形は読めない")}
	}
	if err != nil {
		return indentConfig{}, nil, &configError{path: path, err: err}
	}
	ic, err := prettierOptions(doc)
	if err != nil {
		return indentConfig{}, nil, &configError{path: path, err: err}
	}
	over, err := prettierOverrides(doc, string(data), jsonForm, relSlash(filepath.Dir(path), file))
	if err != nil {
		return indentConfig{}, nil, &configError{path: path, err: err}
	}
	return ic.merge(over), []string{path}, nil
}

// prettierOptions は、prettier の設定（か overrides の options）doc の useTabs と tabWidth を読む。
func prettierOptions(doc map[string]any) (indentConfig, error) {
	var ic indentConfig
	if v, ok := doc["useTabs"]; ok {
		b, isBool := v.(bool)
		if !isBool {
			return indentConfig{}, errors.New("useTabs は true か false にする")
		}
		ic.useTabs = &b
	}
	if v, ok := doc["tabWidth"]; ok {
		w, err := widthOf("tabWidth", v)
		if err != nil {
			return indentConfig{}, err
		}
		ic.width = w
	}
	return ic, nil
}

// indentKey は、YAML と TOML の本文の中の、prettier のインデントの項目の鍵（useTabs: と tabWidth:、TOML の useTabs = と tabWidth =）である。
var indentKey = regexp.MustCompile(`(?m)(^|[\s{,'"])(useTabs|tabWidth)['"]?\s*[:=]`)

// prettierOverrides は、prettier の設定 doc の overrides のうち、ファイル（設定ファイルのディレクトリからの相対パス rel）に当たるものの
// useTabs と tabWidth を、順に重ねて返す（後のものが勝つ。PR #352 の再レビューの推奨 2）。
//
// JSON の形（jsonForm）では、options に useTabs か tabWidth を書いたものの files と excludeFiles を overrideMatch で当て
// （prettier は / を含まないパターンをファイル名に当てる）、当たるかを決められないものは誤りにする。当たらないと分かるものは読み飛ばす。
// YAML と TOML の形（本文 src）は overrides の中を読めないので、本文が最上位のほかに useTabs か tabWidth の鍵を含むときだけ誤りにする
// （インデントを変えない overrides は読み飛ばせる）。
func prettierOverrides(doc map[string]any, src string, jsonForm bool, rel string) (indentConfig, error) {
	if !jsonForm {
		top := 0
		for _, k := range []string{"useTabs", "tabWidth"} {
			if _, ok := doc[k]; ok {
				top++
			}
		}
		_, hasOverrides := doc["overrides"]
		hasTOMLOverrides := slices.ContainsFunc(slices.Collect(maps.Keys(doc)), func(k string) bool { return strings.HasPrefix(k, "overrides") })
		if (hasOverrides || hasTOMLOverrides) && len(indentKey.FindAllString(src, -1)) > top {
			return indentConfig{}, errors.New("overrides でインデントを変えている（YAML と TOML の形の overrides は読めない）")
		}
		return indentConfig{}, nil
	}
	v, ok := doc["overrides"]
	if !ok {
		return indentConfig{}, nil
	}
	list, _ := v.([]any)
	var out indentConfig
	for i, o := range list {
		m, _ := o.(map[string]any)
		opts, _ := m["options"].(map[string]any)
		x, err := prettierOptions(opts)
		if err != nil {
			return indentConfig{}, fmt.Errorf("overrides[%d]: %w", i, err)
		}
		if x == (indentConfig{}) {
			continue
		}
		match, known := overrideMatch(stringList(m["files"]), stringList(m["excludeFiles"]), rel, true)
		switch {
		case !known:
			return indentConfig{}, fmt.Errorf("overrides[%d] がこのファイルに当たるかを決められない（読めない）", i)
		case match:
			out = out.merge(x)
		}
	}
	return out, nil
}

// stringList は、JSON の文字列か文字列の配列 v を、文字列の列にする。どちらでもなければ空である。
func stringList(v any) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case []any:
		var out []string
		for _, e := range x {
			s, ok := e.(string)
			if !ok {
				return nil
			}
			out = append(out, s)
		}
		return out
	}
	return nil
}

// ruffIndent は、ruff の設定ファイルを読む。
//
// ファイルのディレクトリから上へ、各ディレクトリで .ruff.toml、ruff.toml、pyproject.toml の順に探し、最初に見つけたものだけを使う。
// pyproject.toml は [tool.ruff] の表（鍵の無い見出しだけでもよい）があるときだけ当たる（2026-11-22 に ruff 0.15.16 の
// check --show-settings で、この順と、[tool.ruff] の無い pyproject.toml を飛ばすこと、鍵の無い [tool.ruff] の pyproject.toml を使うことを確かめた）。
// indent-width と format.indent-style（space か tab）を読む。pyproject.toml では tool.ruff の下を読む。
// extend（ほかの設定ファイルを引き継ぐ）は、その設定ファイル（相対パスは extend を書いた設定ファイルのディレクトリから）を先に当てて重ねる
// （ruffExtend。PR #352 の再レビューの推奨 2）。
func ruffIndent(dir string) (indentConfig, []string, error) {
	var readErr error
	var doc map[string]any
	path, ok := findUp(dir, []string{".ruff.toml", "ruff.toml", "pyproject.toml"}, func(p string) bool {
		if !isFile(p) {
			return false
		}
		d, has, err := readRuff(p)
		if err != nil {
			readErr = err
			return true
		}
		doc = d
		return has
	}, nil)
	if !ok || readErr != nil {
		return indentConfig{}, nil, readErr
	}
	doc, srcs, err := ruffExtend(path, doc, 0)
	if err != nil {
		return indentConfig{}, nil, err
	}
	ic, err := ruffDocIndent(path, doc)
	return ic, append([]string{path}, srcs...), err
}

// readRuff は、ruff の設定ファイル p を読む。pyproject.toml なら tool.ruff の下を返し、[tool.ruff] が無ければ has が false である。
func readRuff(p string) (doc map[string]any, has bool, err error) {
	data, err := readConfigFile(p)
	if err != nil {
		return nil, false, err
	}
	d, err := parseTOML(string(data))
	if err != nil {
		return nil, false, &configError{path: p, err: err}
	}
	if filepath.Base(p) == "pyproject.toml" {
		if !hasTable(d, "tool.ruff") {
			return nil, false, nil
		}
		d = subTable(d, "tool.ruff")
	}
	return d, true, nil
}

// maxRuffExtend は、ruff の extend をたどる深さの上限である（輪になった extend で止まらなくならないように）。
const maxRuffExtend = 8

// ruffExtend は、ruff の設定 doc（ファイル path）の extend の先を先に当て、doc を重ねた設定を返す。extend の先がさらに extend を持てば、
// 同じくたどる。2026-11-22 に ruff 0.15.16 で、extend の先の値を使うこと、extend を書いた設定の値が勝つこと、
// [tool.ruff] の無い pyproject.toml を extend の先にすると何も引き継がないことを確かめた。
func ruffExtend(path string, doc map[string]any, depth int) (map[string]any, []string, error) {
	v, ok := doc["extend"]
	if !ok {
		return doc, nil, nil
	}
	s, isString := v.(string)
	if !isString || s == "" {
		return nil, nil, &configError{path: path, err: errors.New("extend は設定ファイルのパスの文字列にする")}
	}
	if depth >= maxRuffExtend {
		return nil, nil, &configError{path: path, err: fmt.Errorf("extend が %d 段より深い", maxRuffExtend)}
	}
	target := s
	if rest, ok := strings.CutPrefix(s, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, nil, &configError{path: path, err: err}
		}
		target = filepath.Join(home, rest)
	} else if !filepath.IsAbs(s) {
		target = filepath.Join(filepath.Dir(path), s)
	}
	base, _, err := readRuff(target)
	if err != nil {
		return nil, nil, err
	}
	base, srcs, err := ruffExtend(target, base, depth+1)
	if err != nil {
		return nil, nil, err
	}
	out := map[string]any{}
	maps.Copy(out, base)
	maps.Copy(out, doc)
	delete(out, "extend")
	return out, append([]string{target}, srcs...), nil
}

// ruffDocIndent は、ruff の設定 doc（ファイル path）の indent-width と format.indent-style を読む。
func ruffDocIndent(path string, doc map[string]any) (indentConfig, error) {
	var ic indentConfig
	if v, ok := doc["format.indent-style"]; ok {
		t, err := styleOf("format.indent-style", v, "tab", "space")
		if err != nil {
			return indentConfig{}, &configError{path: path, err: err}
		}
		ic.useTabs = t
	}
	if v, ok := doc["indent-width"]; ok {
		w, err := widthOf("indent-width", v)
		if err != nil {
			return indentConfig{}, &configError{path: path, err: err}
		}
		ic.width = w
	}
	return ic, nil
}

// hasTable は、parseTOML の結果 d に、表 name（その下の鍵か、鍵の無い見出しの印）があるかを返す。
func hasTable(d map[string]any, name string) bool {
	for k := range d {
		if strings.HasPrefix(k, name+".") {
			return true
		}
	}
	return false
}

// subTable は、parseTOML の結果 d のうち、表 name の下の鍵を、name. を除いた鍵で返す。表の見出しの印（name. そのもの）は除く。
func subTable(d map[string]any, name string) map[string]any {
	out := map[string]any{}
	for k, v := range d {
		if rest, ok := strings.CutPrefix(k, name+"."); ok && rest != "" {
			out[rest] = v
		}
	}
	return out
}
