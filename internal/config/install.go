package config

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tailscale/hujson"
)

// ExtensionInstall は、設定ファイルの extensions.install の 1 項目で、取得する拡張機能の 1 つである（基本設計 5.6。項目の名前は仮）。
// 項目の名前と型と並びは extmgr.Spec と同じにしてあり、呼び出し側は extmgr.Spec(e) で変換できる。
//
//	{ "name": "surround", "repo": "https://github.com/example/modal-editor-surround", "version": "v0.1.0" }
type ExtensionInstall struct {
	// Name は、拡張機能を置くディレクトリの名前（$XDG_DATA_HOME/modal-editor/extensions/<Name>/）である。
	// 英数字で始まり、英数字と "."、"_"、"-" だけからなる。
	Name string
	// Repo は、取得元の git のリポジトリ（git clone に渡す URL か絶対パス）である。"-" で始まらず、相対パスでない。
	Repo string
	// Version は、取り出す版（タグ、ブランチ、コミットのハッシュ）である。"-" で始まらない。
	Version string
}

// installName は、extensions.install の name に使える名前である。
// ディレクトリの名前にするので、パスの区切り、"." と ".."、隠しファイルになる "." で始まる名前を除く。
var installName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// extensionInstall は、extensions.install の値を読む（基本設計 5.6）。
// 各項目は name、repo、version の 3 つの文字列を持つオブジェクトで、どれも省けない。
// 誤りのある項目（型の違い、項目の欠け、未知の項目、使えない名前、相対パスの repo、先の項目と重なる名前）は使わず、位置と内容を記録する。
// valid は、誤りが 1 つも無かったことである（Config.ExtensionInstallValid）。
func (s *source) extensionInstall(v *hujson.Value) (out []ExtensionInstall, valid bool) {
	before := len(s.problems)
	defer func() { valid = len(s.problems) == before }()
	const path = "extensions.install"
	arr, ok := s.array(v, path)
	if !ok {
		return nil, false
	}
	out = []ExtensionInstall{}
	seen := map[string]bool{}
	for i := range arr.Elements {
		el := &arr.Elements[i]
		obj, ok := s.object(el, path+" の項目")
		if !ok {
			continue
		}
		if e, ok := s.installEntry(el, obj); ok {
			if seen[e.Name] {
				s.report(el.StartOffset, "%s の名前 %q が先の項目と重なるので、この項目を使わない", path, e.Name)
				continue
			}
			seen[e.Name] = true
			out = append(out, e)
		}
	}
	return out, true
}

// isRelativeRepo は、repo が git の相対パスの取得元かを返す（extmgr.IsRelativeRepo と同じ規則。同じ層なので import しない）。
// "://" を含むものを URL、最初の "/" より前に ":" があるものを scp の形（user@host:path）として扱う。
// 相対パスは、本体の作業ディレクトリによって指す先が変わるので使えない。
func isRelativeRepo(repo string) bool {
	if strings.Contains(repo, "://") || filepath.IsAbs(repo) {
		return false
	}
	colon := strings.IndexByte(repo, ':')
	slash := strings.IndexByte(repo, '/')
	return colon < 0 || (slash >= 0 && slash < colon)
}

// installEntry は、extensions.install の 1 項目を読む。誤りがあれば記録して false を返す。
func (s *source) installEntry(el *hujson.Value, obj *hujson.Object) (ExtensionInstall, bool) {
	const path = "extensions.install"
	var e ExtensionInstall
	ok := true
	for _, m := range s.members(obj, path) {
		var dst *string
		switch m.name {
		case "name":
			dst = &e.Name
		case "repo":
			dst = &e.Repo
		case "version":
			dst = &e.Version
		default:
			s.report(m.nameOff, "未知の項目 %s を無視する", join(path, m.name))
			continue
		}
		str, isStr := s.str(m.value, join(path, m.name))
		if !isStr {
			ok = false
			continue
		}
		switch {
		case m.name == "name" && !installName.MatchString(str):
			s.report(m.value.StartOffset, "%s.name %q は英数字で始め、英数字と . _ - だけにする", path, str)
			ok = false
		case m.name != "name" && (str == "" || strings.HasPrefix(str, "-")):
			s.report(m.value.StartOffset, "%s は空にせず、- で始めない", join(path, m.name))
			ok = false
		case m.name == "repo" && isRelativeRepo(str):
			s.report(m.value.StartOffset, "%s %q は相対パスにしない（URL か絶対パスにする）", join(path, m.name), str)
			ok = false
		}
		*dst = str
	}
	if !ok {
		return ExtensionInstall{}, false
	}
	if e.Name == "" || e.Repo == "" || e.Version == "" {
		s.report(el.StartOffset, "%s の項目には name、repo、version を書く", path)
		return ExtensionInstall{}, false
	}
	return e, true
}
