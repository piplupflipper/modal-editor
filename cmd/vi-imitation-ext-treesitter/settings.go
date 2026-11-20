package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/piplupflipper/vi-imitation/internal/extmgr"
)

// parserSpec は、利用者の設定に並べた 1 つのパーサである（FR-142。設定の extensions.settings.treesitter.parsers の 1 項目）。
//
// 設定の形は次のとおりである（項目の名前は仮）。
//
//	"extensions": {
//	  "settings": {
//	    "treesitter": {
//	      // パーサの C のソースをビルドする C コンパイラ。既定は PATH の cc
//	      "compiler": "cc",
//	      "parsers": {
//	        // 名前は、取得したソースとビルドしたものを置くディレクトリの名前と、ロックファイルの記録の名前になる
//	        "python": {
//	          "repo": "https://github.com/tree-sitter/tree-sitter-python",
//	          "version": "v0.25.0",
//	          "fileTypes": ["python"],
//	          // 省略できる。src/ のあるディレクトリ（リポジトリの中の相対パス）。既定はリポジトリの最上位
//	          "path": "",
//	          // 省略できる。ハイライトのクエリのファイル（リポジトリの中の相対パス）。複数あれば順につなぐ
//	          "queries": ["queries/highlights.scm"]
//	        }
//	      }
//	    }
//	  }
//	}
type parserSpec struct {
	// name は、parsers の項目の名前である。
	name string
	// repo、version は、取得元の git のリポジトリと版（タグ、ブランチ、コミットのハッシュ）である（extmgr.Checkout に渡す）。
	repo, version string
	// fileTypes は、このパーサを使うファイルの種類である。利用者の設定の中で重なった種類は除いてある。
	fileTypes []string
	// path は、src/parser.c のある src/ の親のディレクトリの、リポジトリの中の相対パスである。空ならリポジトリの最上位である。
	// 1 つのリポジトリに複数の文法を置くもの（typescript/ と tsx/ を持つ tree-sitter-typescript など）に使う。
	path string
	// queries は、ハイライトのクエリのファイルの、リポジトリの中の相対パスの列である。
	// 空なら、path の下の queries/highlights.scm、無ければリポジトリの最上位の queries/highlights.scm を使う。
	queries []string
}

// treesitterSettings は、設定の extensions.settings.treesitter の値である（parserSpec の doc の形）。
type treesitterSettings struct {
	// compiler は、パーサの C のソースをビルドする C コンパイラの名前かパスである。空なら "cc" である。
	compiler string
	// parsers は、名前の順に並べた、使えるパーサの一覧である。誤りのある項目は含めない。
	parsers []parserSpec
	// listed は、parsers の項目の名前の集合で、誤りのある項目の名前も含む。
	// ロックファイルから、設定に並べなくなったパーサの記録を消すのに使う（誤りのある項目の記録は残す）。
	listed map[string]bool
}

// defaultCompiler は、設定に compiler が無いときの C コンパイラである。
const defaultCompiler = "cc"

// parserName は、パーサの名前に使える文字列である（extmgr の拡張機能の名前と同じ規則）。ディレクトリの名前になる。
var parserName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// parseSettings は、extensions.settings.treesitter の値を読む（FR-142）。
//
// 誤りは problems に 1 件ずつ日本語で返し、誤りのある項目だけを使わない。
//
//   - 値が JSON のオブジェクトでなければ、すべてを使わない。parsers がオブジェクトでないときも、パーサを 1 つも使わない
//   - 知らない項目（最上位と各パーサ）は誤りにする。最上位の知らない項目は無視し、パーサの知らない項目はそのパーサを使わない
//   - repo が空か "-" で始まるか相対パス、version が空か "-" で始まる、fileTypes が空か空の名前を含む、
//     path か queries がリポジトリの外を指す（絶対パスや ".." を含む）パーサは使わない
//   - 2 つ以上のパーサが同じファイルの種類を持てば、その種類にはどのパーサも使わない（ほかの種類には使う）
//
// settings が nil（設定が無い）なら、誤りの無い空の設定を返す。
//
// req: FR-142
func parseSettings(settings json.RawMessage) (treesitterSettings, []string) {
	out := treesitterSettings{compiler: defaultCompiler, listed: map[string]bool{}}
	if len(bytes.TrimSpace(settings)) == 0 || bytes.Equal(bytes.TrimSpace(settings), []byte("null")) {
		return out, nil
	}
	var problems []string
	var top map[string]json.RawMessage
	if err := json.Unmarshal(settings, &top); err != nil {
		return out, []string{fmt.Sprintf("設定の extensions.settings.treesitter を読めない: %v", err)}
	}
	keys := sortedKeys(top)
	for _, k := range keys {
		switch k {
		case "compiler":
			var c string
			if err := json.Unmarshal(top[k], &c); err != nil || c == "" {
				problems = append(problems, "設定の extensions.settings.treesitter.compiler は空でない文字列にする")
				continue
			}
			out.compiler = c
		case "parsers":
		default:
			problems = append(problems, fmt.Sprintf("設定の extensions.settings.treesitter に知らない項目 %s がある", k))
		}
	}
	raw, ok := top["parsers"]
	if !ok {
		return out, problems
	}
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return out, append(problems, fmt.Sprintf("設定の extensions.settings.treesitter.parsers を読めない: %v", err))
	}
	for _, name := range sortedKeys(entries) {
		out.listed[name] = true
		spec, err := parseParser(name, entries[name])
		if err != nil {
			problems = append(problems, fmt.Sprintf("パーサ %s を使わない: %v", name, err))
			continue
		}
		out.parsers = append(out.parsers, spec)
	}

	// 利用者の設定の中で重なったファイルの種類は、どのパーサにも使わない。
	owners := map[string][]string{}
	for _, p := range out.parsers {
		for _, ft := range p.fileTypes {
			if !slices.Contains(owners[ft], p.name) {
				owners[ft] = append(owners[ft], p.name)
			}
		}
	}
	for _, ft := range sortedKeys(owners) {
		if names := owners[ft]; len(names) > 1 {
			problems = append(problems, fmt.Sprintf("ファイルの種類 %s を複数のパーサ（%s）が持つので、%s にはどれも使わない", ft, strings.Join(names, "、"), ft))
		}
	}
	for i := range out.parsers {
		p := &out.parsers[i]
		p.fileTypes = slices.DeleteFunc(p.fileTypes, func(ft string) bool { return len(owners[ft]) > 1 })
	}
	return out, problems
}

// parserJSON は、parsers の 1 項目の JSON の形である。
type parserJSON struct {
	Repo      string   `json:"repo"`
	Version   string   `json:"version"`
	FileTypes []string `json:"fileTypes"`
	Path      string   `json:"path"`
	Queries   []string `json:"queries"`
}

// parseParser は、parsers の 1 項目を読んで検査する。
func parseParser(name string, raw json.RawMessage) (parserSpec, error) {
	if !parserName.MatchString(name) {
		return parserSpec{}, fmt.Errorf("名前は英数字で始め、英数字と . _ - だけにする")
	}
	var j parserJSON
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&j); err != nil {
		return parserSpec{}, fmt.Errorf("読めない: %w", err)
	}
	switch {
	case j.Repo == "" || strings.HasPrefix(j.Repo, "-"):
		return parserSpec{}, fmt.Errorf("repo %q は空にせず、- で始めない", j.Repo)
	case extmgr.IsRelativeRepo(j.Repo):
		return parserSpec{}, fmt.Errorf("repo %q は相対パスにしない（URL か絶対パスにする）", j.Repo)
	case j.Version == "" || strings.HasPrefix(j.Version, "-"):
		return parserSpec{}, fmt.Errorf("version %q は空にせず、- で始めない", j.Version)
	case len(j.FileTypes) == 0:
		return parserSpec{}, fmt.Errorf("fileTypes が空である")
	case slices.Contains(j.FileTypes, ""):
		return parserSpec{}, fmt.Errorf("fileTypes に空の名前がある")
	case j.Path != "" && !filepath.IsLocal(j.Path):
		return parserSpec{}, fmt.Errorf("path %q はリポジトリの中の相対パスにする", j.Path)
	}
	for _, q := range j.Queries {
		if !filepath.IsLocal(q) {
			return parserSpec{}, fmt.Errorf("queries の %q はリポジトリの中の相対パスにする", q)
		}
	}
	fileTypes := slices.Clone(j.FileTypes)
	slices.Sort(fileTypes)
	return parserSpec{
		name:      name,
		repo:      j.Repo,
		version:   j.Version,
		fileTypes: slices.Compact(fileTypes),
		path:      filepath.Clean(j.Path),
		queries:   j.Queries,
	}, nil
}

// sortedKeys は、m のキーを名前の順に返す。
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
