package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

// toolKind は、整形ツールの設定 f が、設定ファイルを読める整形ツール（biome、prettier、ruff）のどれかを返す。どれでもなければ空である。
// 名前（name。省けば command のファイル名）か command のファイル名で決める。prettierd は prettier の設定ファイルを読むので prettier にする。
func toolKind(f formatterConfig) string {
	for _, n := range []string{f.Name, filepath.Base(f.Command)} {
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

// projectIndent は、ファイル path（ファイルの種類 ft）のインデントを、ft に設定した整形ツールの設定ファイルから読む。
// 設定の順に整形ツールを見て、後の整形ツールの値を重ねる（後の整形ツールの出力が最後に残るため）。
// どれかの設定ファイルを読めなければ、その誤りを返す（呼び出し側は本体の設定を変えない）。
//
// req: FR-193
func (c *config) projectIndent(path, ft string) (indentConfig, error) {
	var out indentConfig
	seen := map[string]bool{}
	dir := filepath.Dir(path)
	for _, f := range c.forFileType(ft) {
		kind := toolKind(f)
		if kind == "" || seen[kind] {
			continue
		}
		seen[kind] = true
		var ic indentConfig
		var err error
		switch kind {
		case "biome":
			ic, err = biomeIndent(dir, ft)
		case "prettier":
			ic, err = prettierIndent(dir)
		case "ruff":
			ic, err = ruffIndent(dir)
		}
		if err != nil {
			return indentConfig{}, err
		}
		out = out.merge(ic)
	}
	return out, nil
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

// boolPtr は、b のポインターを返す。
func boolPtr(b bool) *bool { return &b }

// biomeLanguage は、ファイルの種類から、biome の言語ごとの設定の項目の名前（"javascript" の "javascript.formatter"）を引く。
var biomeLanguage = map[string]string{
	"javascript": "javascript", "javascriptreact": "javascript", "typescript": "javascript", "typescriptreact": "javascript",
	"json": "json", "jsonc": "json", "css": "css", "graphql": "graphql", "html": "html",
}

// biomeIndent は、biome の設定ファイルを読む。
//
// ファイルのディレクトリから上へ、各ディレクトリで biome.json、biome.jsonc の順に探し、最初に見つけたものだけを使う
// （入れ子の設定は、上の設定を引き継がない。2026-11-22 に biome 2.4.15 で、同じディレクトリでは biome.json が勝つこと、
// 近い biome.jsonc が上の biome.json の項目を引き継がないことを確かめた）。
// formatter.indentStyle（tab か space）と formatter.indentWidth を読み、ファイルの種類の言語の <言語>.formatter に書いた項目を重ねる。
// overrides（glob ごとの上書き）と extends は読まない。
func biomeIndent(dir, ft string) (indentConfig, error) {
	path, ok := findUp(dir, []string{"biome.json", "biome.jsonc"}, isFile, nil)
	if !ok {
		return indentConfig{}, nil
	}
	data, err := readConfigFile(path)
	if err != nil {
		return indentConfig{}, err
	}
	var doc map[string]json.RawMessage
	if err := parseJSONC(data, &doc); err != nil {
		return indentConfig{}, &configError{path: path, err: err}
	}
	// read は、formatter の項目の JSON（where はその位置の名前）からインデントを読む。
	read := func(where string, raw json.RawMessage) (indentConfig, error) {
		var f map[string]any
		if err := json.Unmarshal(raw, &f); err != nil {
			return indentConfig{}, fmt.Errorf("%s: %w", where, err)
		}
		var ic indentConfig
		if v, ok := f["indentStyle"]; ok {
			switch v {
			case "tab":
				ic.useTabs = boolPtr(true)
			case "space":
				ic.useTabs = boolPtr(false)
			default:
				return indentConfig{}, fmt.Errorf("%s.indentStyle は \"tab\" か \"space\" にする: %v", where, v)
			}
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
	var out indentConfig
	if raw, ok := doc["formatter"]; ok {
		ic, err := read("formatter", raw)
		if err != nil {
			return indentConfig{}, &configError{path: path, err: err}
		}
		out = ic
	}
	if lang := biomeLanguage[ft]; lang != "" {
		if raw, ok := doc[lang]; ok {
			var section struct {
				Formatter json.RawMessage `json:"formatter"`
			}
			if err := json.Unmarshal(raw, &section); err != nil {
				return indentConfig{}, &configError{path: path, err: fmt.Errorf("%s: %w", lang, err)}
			}
			if section.Formatter != nil {
				ic, err := read(lang+".formatter", section.Formatter)
				if err != nil {
					return indentConfig{}, &configError{path: path, err: err}
				}
				out = out.merge(ic)
			}
		}
	}
	return out, nil
}

// prettierFiles は、prettier が 1 つのディレクトリで設定を探す順である（https://prettier.io/docs/configuration の並び。
// 2026-11-22 に読んだ）。package.json は prettier の鍵があるときだけ当たる。
// 読めない形（JavaScript、TypeScript、JSON5、package.yaml）も、prettier が使うので探す順に並べ、当たったらそこで止めて値を使わない。
var prettierFiles = []string{
	"package.json", "package.yaml", ".prettierrc", ".prettierrc.json", ".prettierrc.yml", ".prettierrc.yaml", ".prettierrc.json5",
	".prettierrc.js", "prettier.config.js", ".prettierrc.ts", "prettier.config.ts",
	".prettierrc.mjs", "prettier.config.mjs", ".prettierrc.mts", "prettier.config.mts",
	".prettierrc.cjs", "prettier.config.cjs", ".prettierrc.cts", "prettier.config.cts",
	".prettierrc.toml",
}

// prettierIndent は、prettier の設定ファイルを読む。
//
// ファイルのディレクトリから上へ、各ディレクトリで prettierFiles の順に探し、最初に見つけたものだけを使う
// （prettier の文書の「searching up the file tree until a config file is (or isn't) found」）。
// useTabs と tabWidth を読む。overrides（glob ごとの上書き）は読まない。
// package.json の prettier が文字列（共有の設定の名前）なら、読めない形と同じく値を使わない。
func prettierIndent(dir string) (indentConfig, error) {
	var readErr error
	path, ok := findUp(dir, prettierFiles, func(p string) bool {
		if !isFile(p) {
			return false
		}
		switch filepath.Base(p) {
		case "package.json":
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
		case "package.yaml":
			data, err := readConfigFile(p)
			if err != nil {
				readErr = err
				return true
			}
			doc, err := parseYAMLTop(string(data))
			if err != nil {
				readErr = &configError{path: p, err: err}
				return true
			}
			_, has := doc["prettier"]
			return has
		}
		return true
	}, nil)
	if !ok || readErr != nil {
		return indentConfig{}, readErr
	}
	data, err := readConfigFile(path)
	if err != nil {
		return indentConfig{}, err
	}
	var doc map[string]any
	switch base := filepath.Base(path); base {
	case "package.json":
		var pkg struct {
			Prettier json.RawMessage `json:"prettier"`
		}
		if err = json.Unmarshal(data, &pkg); err == nil {
			if !bytes.HasPrefix(bytes.TrimSpace(pkg.Prettier), []byte("{")) {
				return indentConfig{}, nil // 共有の設定の名前（文字列）
			}
			err = json.Unmarshal(pkg.Prettier, &doc)
		}
	case ".prettierrc", ".prettierrc.json":
		if t := bytes.TrimSpace(bytes.TrimPrefix(data, []byte(bom))); base == ".prettierrc.json" || bytes.HasPrefix(t, []byte("{")) {
			err = parseJSONC(data, &doc)
		} else {
			doc, err = parseYAMLTop(string(data))
		}
	case ".prettierrc.yml", ".prettierrc.yaml":
		doc, err = parseYAMLTop(string(data))
	case ".prettierrc.toml":
		doc, err = parseTOML(string(data))
	default:
		return indentConfig{}, nil // 読めない形（JavaScript、TypeScript、JSON5、package.yaml）
	}
	if err != nil {
		return indentConfig{}, &configError{path: path, err: err}
	}
	var ic indentConfig
	if v, ok := doc["useTabs"]; ok {
		b, isBool := v.(bool)
		if !isBool {
			return indentConfig{}, &configError{path: path, err: errors.New("useTabs は true か false にする")}
		}
		ic.useTabs = &b
	}
	if v, ok := doc["tabWidth"]; ok {
		w, err := widthOf("tabWidth", v)
		if err != nil {
			return indentConfig{}, &configError{path: path, err: err}
		}
		ic.width = w
	}
	return ic, nil
}

// ruffIndent は、ruff の設定ファイルを読む。
//
// ファイルのディレクトリから上へ、各ディレクトリで .ruff.toml、ruff.toml、pyproject.toml の順に探し、最初に見つけたものだけを使う。
// pyproject.toml は [tool.ruff] の表があるときだけ当たる（2026-11-22 に ruff 0.15.16 の check --show-settings で、この順と、
// [tool.ruff] の無い pyproject.toml を飛ばすことを確かめた）。
// indent-width と format.indent-style（space か tab）を読む。pyproject.toml では tool.ruff の下を読む。extend は読まない。
func ruffIndent(dir string) (indentConfig, error) {
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
		return indentConfig{}, readErr
	}
	var ic indentConfig
	if v, ok := doc["format.indent-style"]; ok {
		switch v {
		case "tab":
			ic.useTabs = boolPtr(true)
		case "space":
			ic.useTabs = boolPtr(false)
		default:
			return indentConfig{}, &configError{path: path, err: fmt.Errorf("format.indent-style は \"space\" か \"tab\" にする: %v", v)}
		}
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

// hasTable は、parseTOML の結果 d に、表 name（その下の鍵か、空の見出し）があるかを返す。
func hasTable(d map[string]any, name string) bool {
	for k := range d {
		if strings.HasPrefix(k, name+".") {
			return true
		}
	}
	return false
}

// subTable は、parseTOML の結果 d のうち、表 name の下の鍵を、name. を除いた鍵で返す。
func subTable(d map[string]any, name string) map[string]any {
	out := map[string]any{}
	for k, v := range d {
		if rest, ok := strings.CutPrefix(k, name+"."); ok {
			out[rest] = v
		}
	}
	return out
}
