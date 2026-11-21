package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
			ic, srcs, err = biomeIndent(dir, ft)
		case "prettier":
			ic, srcs, err = prettierIndent(dir)
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

// biomeIndent は、biome の設定ファイルを読む。
//
// ファイルのディレクトリから上へ、各ディレクトリで biomeFiles の順に探し、最初に見つけたもの（近い設定）を使う。
// formatter.indentStyle（tab か space）と formatter.indentWidth を読み、ファイルの種類の言語の <言語>.formatter に書いた項目を重ねる。
// 近い設定が "extends": "//" なら、その上へ探した根の設定（"root": false でない設定）を先に当て、近い設定を重ねる。
// "extends" の無い近い設定は、"root": false でも上の設定を引き継がない。
// 2026-11-22 に biome 2.4.15 の format --stdin-file-path=<絶対パス>（この拡張機能の設定の例と同じ渡し方）で、この 3 つを確かめた
// （PR #352 のレビューの推奨 2）。
// "//" でない extends（ほかの設定ファイルや共有の設定）と、overrides の中のインデントの項目は読めないので、configError にする。
func biomeIndent(dir, ft string) (indentConfig, []string, error) {
	path, ok := findUp(dir, biomeFiles, isFile, nil)
	if !ok {
		return indentConfig{}, nil, nil
	}
	doc, err := readBiome(path)
	if err != nil {
		return indentConfig{}, nil, err
	}
	nested, err := biomeDocIndent(path, doc, ft)
	if err != nil {
		return indentConfig{}, nil, err
	}
	raw, extends := doc["extends"]
	if !extends {
		return nested, []string{path}, nil
	}
	var s string
	if json.Unmarshal(raw, &s) != nil || s != "//" {
		return indentConfig{}, nil, &configError{path: path, err: errors.New(`extends は "//" のほかを読めない`)}
	}
	var rootErr error
	var rootDoc map[string]json.RawMessage
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
			return false
		}
		rootDoc = d
		return true
	}, nil)
	switch {
	case rootErr != nil:
		return indentConfig{}, nil, rootErr
	case !found:
		return nested, []string{path}, nil
	}
	if _, ok := rootDoc["extends"]; ok {
		return indentConfig{}, nil, &configError{path: rootPath, err: errors.New("根の設定の extends を読めない")}
	}
	root, err := biomeDocIndent(rootPath, rootDoc, ft)
	if err != nil {
		return indentConfig{}, nil, err
	}
	return root.merge(nested), []string{path, rootPath}, nil
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

// biomeDocIndent は、biome の設定ファイル path の中身 doc からインデントを読む。overrides の中のインデントの項目は configError にする。
func biomeDocIndent(path string, doc map[string]json.RawMessage, ft string) (indentConfig, error) {
	ic, _, err := biomeSection("", doc, ft)
	if err != nil {
		return indentConfig{}, &configError{path: path, err: err}
	}
	if raw, ok := doc["overrides"]; ok {
		var overrides []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &overrides); err != nil {
			return indentConfig{}, &configError{path: path, err: fmt.Errorf("overrides: %w", err)}
		}
		for i, o := range overrides {
			_, has, err := biomeSection(fmt.Sprintf("overrides[%d].", i), o, ft)
			if err != nil {
				return indentConfig{}, &configError{path: path, err: err}
			}
			if has {
				return indentConfig{}, &configError{path: path, err: fmt.Errorf("overrides[%d] でインデントを変えている（overrides は読めない）", i)}
			}
		}
	}
	return ic, nil
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
// 読めない形（JavaScript、TypeScript、JSON5、package.yaml、package.json の共有の設定の名前）と、overrides（読めないので、
// JSON の形でインデントの項目を書いたものと、YAML と TOML の形のものすべて）は configError にする。
func prettierIndent(dir string) (indentConfig, []string, error) {
	var readErr error
	path, ok := findUp(dir, prettierFiles, func(p string) bool {
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
	if err := prettierOverrides(doc, jsonForm); err != nil {
		return indentConfig{}, nil, &configError{path: path, err: err}
	}
	var ic indentConfig
	if v, ok := doc["useTabs"]; ok {
		b, isBool := v.(bool)
		if !isBool {
			return indentConfig{}, nil, &configError{path: path, err: errors.New("useTabs は true か false にする")}
		}
		ic.useTabs = &b
	}
	if v, ok := doc["tabWidth"]; ok {
		w, err := widthOf("tabWidth", v)
		if err != nil {
			return indentConfig{}, nil, &configError{path: path, err: err}
		}
		ic.width = w
	}
	return ic, []string{path}, nil
}

// prettierOverrides は、prettier の設定 doc の overrides を確かめる。JSON の形（jsonForm）なら、options に useTabs か tabWidth を
// 書いたものを誤りにする。YAML と TOML の形は中身を読めないので、overrides があれば誤りにする。
func prettierOverrides(doc map[string]any, jsonForm bool) error {
	v, ok := doc["overrides"]
	if !ok {
		return nil
	}
	if !jsonForm {
		return errors.New("overrides は JSON の形でないと読めない")
	}
	list, _ := v.([]any)
	for i, o := range list {
		m, _ := o.(map[string]any)
		opts, _ := m["options"].(map[string]any)
		_, tabs := opts["useTabs"]
		_, width := opts["tabWidth"]
		if tabs || width {
			return fmt.Errorf("overrides[%d] でインデントを変えている（overrides は読めない）", i)
		}
	}
	return nil
}

// ruffIndent は、ruff の設定ファイルを読む。
//
// ファイルのディレクトリから上へ、各ディレクトリで .ruff.toml、ruff.toml、pyproject.toml の順に探し、最初に見つけたものだけを使う。
// pyproject.toml は [tool.ruff] の表（鍵の無い見出しだけでもよい）があるときだけ当たる（2026-11-22 に ruff 0.15.16 の
// check --show-settings で、この順と、[tool.ruff] の無い pyproject.toml を飛ばすこと、鍵の無い [tool.ruff] の pyproject.toml を使うことを確かめた）。
// indent-width と format.indent-style（space か tab）を読む。pyproject.toml では tool.ruff の下を読む。
// extend（ほかの設定ファイルを引き継ぐ）は読めないので configError にする。
func ruffIndent(dir string) (indentConfig, []string, error) {
	var readErr error
	var doc map[string]any
	path, ok := findUp(dir, []string{".ruff.toml", "ruff.toml", "pyproject.toml"}, func(p string) bool {
		if !isFile(p) {
			return false
		}
		data, err := readConfigFile(p)
		if err != nil {
			readErr = err
			return true
		}
		d, err := parseTOML(string(data))
		if err != nil {
			readErr = &configError{path: p, err: err}
			return true
		}
		if filepath.Base(p) == "pyproject.toml" {
			if !hasTable(d, "tool.ruff") {
				return false
			}
			d = subTable(d, "tool.ruff")
		}
		doc = d
		return true
	}, nil)
	if !ok || readErr != nil {
		return indentConfig{}, nil, readErr
	}
	if _, ok := doc["extend"]; ok {
		return indentConfig{}, nil, &configError{path: path, err: errors.New("extend は読めない")}
	}
	var ic indentConfig
	if v, ok := doc["format.indent-style"]; ok {
		t, err := styleOf("format.indent-style", v, "tab", "space")
		if err != nil {
			return indentConfig{}, nil, &configError{path: path, err: err}
		}
		ic.useTabs = t
	}
	if v, ok := doc["indent-width"]; ok {
		w, err := widthOf("indent-width", v)
		if err != nil {
			return indentConfig{}, nil, &configError{path: path, err: err}
		}
		ic.width = w
	}
	return ic, []string{path}, nil
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
