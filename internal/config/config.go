// Package config は、設定ファイルと、拡張機能の宣言ファイルを読む（基本設計 2 章、7 章、5.1）。
//
// どちらのファイルも JSONC（コメントと末尾のカンマを許す JSON）で書く（FR-40）。
// JSONC は github.com/tailscale/hujson で標準の JSON に直してから encoding/json で読む（#9 の裁定）。
//
// このパッケージは読むだけで、読んだ値の適用（キーマップの展開、拡張機能の起動）は internal/editor と internal/exthost が行う。
// 基本設計 2 章の層の規則で、同じ層の internal/exthost も、上の層の internal/editor も import しない。
// 読んだ値は、editor.Config と exthost.Spec の項目と同じ型で返すので、呼び出し側（cmd/modal-editor）が項目を写す。
//
// # 誤りの扱い
//
// 設定ファイルの誤りのある項目は使わず（既定値のままにし）、位置（行と列）と内容を Error で返す（基本設計 8 章）。
// 誤りがあっても、他の項目は読む。構文の誤りで全体を読めないときは、すべての項目が既定値になる。
//
// # 置き場所（仮）
//
// 設定ファイルの置き場所と、本体が拡張機能を探す規則は、要件定義 7 章で未決なので、仮に次のとおりにする（基本設計 7 章）。
//
//   - 設定ファイル: $XDG_CONFIG_HOME/modal-editor/config.jsonc。XDG_CONFIG_HOME が空か相対パスなら ~/.config を使う（DefaultPath）
//   - 拡張機能: $XDG_DATA_HOME/modal-editor/extensions/<名前>/（利用者）と、本体の実行ファイルの隣の extensions/<名前>/（同梱）。
//     XDG_DATA_HOME が空か相対パスなら ~/.local/share を使う（ExtensionDirs）。各ディレクトリの manifest.json を宣言ファイルとして読み、
//     同じ ID なら利用者のものを使う
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"math"
	"os"
	"time"

	"github.com/tailscale/hujson"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/internal/editcmd"
)

// Config は、設定ファイルから読んだ値である（基本設計 7 章。項目の名前は仮）。
// ゼロ値は「設定ファイルが無い」状態で、すべての項目が既定値になる。
//
// 設定ファイルの形は次のとおりである。
//
//	{
//	  "keymap": { "normal": { "<Space>w": ":w<CR>" } },
//	  "fixendofline": true,
//	  "fileTypes": { ".tmpl": "html", "Justfile": "make" },
//	  "extensions": {
//	    "syncTimeoutMs": 50,
//	    "memoryLimitMb": 0,
//	    "settings": { "lsp": { "servers": [] } },
//	    "activation": { "lsp": [{ "on": "filetype", "pattern": "python" }] }
//	  }
//	}
type Config struct {
	// Keymap は keymap の値で、editor.Config.Keymap にそのまま渡す。
	// モードの名前（"normal"、"visual"、"insert"、"commandLine"）から、キーの列の表記から展開するキーの列の表記への対応である。
	// 未知のモードと、editcmd.ParseKeys で読めない表記の項目は除いてある。
	Keymap map[string]map[string]string
	// SyncTimeout は extensions.syncTimeoutMs の値で、editor.Config.SyncTimeout にそのまま渡す。
	// 項目が無いか誤りなら nil（editor の既定値）。0 は「待たない」（FR-18）。
	SyncTimeout *time.Duration
	// FixEndOfLine は fixendofline の値で、書き出すときに末尾の改行を補うかどうかである（基本設計 4.1。Vim の 'fixendofline'）。
	// 項目が無いか誤りなら nil で、既定の有効（true）として扱う。
	// editor.Config にはまだ対応する項目が無いので、呼び出し側が buffer.Buffer.SetFixEndOfLine へ写す。
	FixEndOfLine *bool
	// MemoryLimitMB は extensions.memoryLimitMb の値（MB）で、0 は「上限なし」である（FR-20）。
	// 基本設計 7 章のとおり、拡張機能の子プロセスの RLIMIT_AS として設定する値である。
	// exthost.Config にはまだ対応する項目が無いので、読むだけで、適用は別の issue で行う。
	MemoryLimitMB int64
	// FileTypes は fileTypes の値で、editor.Config.FileTypes にそのまま渡す（基本設計 6.7 のファイルの種類の対応表）。
	// キーは拡張子（".md"）かファイル名（"Makefile"）、値はファイルの種類の名前である。
	FileTypes map[string]string
	// ExtensionSettings は extensions.settings の値で、拡張機能の ID から、その拡張機能に向けた設定への対応である。
	// LoadExtensions が Extension.Settings に入れ、本体は initialize の settings でそのまま渡す。
	ExtensionSettings map[string]json.RawMessage
	// ExtensionActivation は extensions.activation の値で、拡張機能の ID から、宣言ファイルの activation に足す契機への対応である
	// （基本設計 6.7 の「拡張機能ごとの activation の追加」）。足せるのは filetype の契機だけである。
	ExtensionActivation map[string][]ext.Activation

	// extPos は、extensions.settings と extensions.activation に書いた拡張機能の ID の位置である。
	// 見つからない拡張機能の ID を LoadExtensions が知らせるのに使う。
	extPos map[string]*Error
}

// FileName は、設定ファイルの名前である（仮）。
const FileName = "config.jsonc"

// appName は、XDG のディレクトリの下に置くディレクトリの名前である。
const appName = "modal-editor"

// mapModes は、keymap のモードの名前である。internal/editor の mapModeNames と同じにする。
var mapModes = map[string]bool{"normal": true, "visual": true, "insert": true, "commandLine": true}

// maxSyncTimeoutMs は、syncTimeoutMs の上限である。time.Duration に直しても溢れない値にする。
const maxSyncTimeoutMs = math.MaxInt64 / int64(time.Millisecond)

// Load は、path の設定ファイルを読む。
// ファイルが無ければ、ゼロ値の Config と nil を返す（設定ファイルは無くてよい）。
// ファイルを読めないときは、ゼロ値の Config と、位置の無い Error を返す。
// それ以外は Parse と同じである。
func Load(path string) (Config, []error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, []error{&Error{Path: path, Msg: "設定ファイルを読めない: " + err.Error()}}
	}
	return Parse(path, data)
}

// Parse は、data を JSONC の設定ファイルとして読む。path は誤りの文言に出すパスである。
//
// 誤りのある項目（型の違い、範囲外の値、読めないキーの表記、未知の項目）は Config に入れず、
// 位置と内容を *Error で返す。誤りのある項目があっても、他の項目は読む。
// 未知の項目は、綴りの誤りに気づけるように誤りとして知らせ、値は無視する。
// 空のファイルと、空白とコメントだけのファイル（設定を全部コメントにしたもの）は、{} と同じに扱う。
// 先頭の UTF-8 の BOM は読み飛ばし、位置は BOM の後から数える。
//
// req: FR-40
func Parse(path string, data []byte) (Config, []error) {
	var cfg Config
	s := newSource(path, data)
	if s.onlyExtra() {
		return cfg, nil
	}
	root, err := s.parseJSONC()
	if err != nil {
		return cfg, []error{err}
	}
	obj, ok := root.Value.(*hujson.Object)
	if !ok {
		s.report(root.StartOffset, "設定ファイルの最上位はオブジェクトにする")
		return cfg, s.problems
	}
	for _, m := range s.members(obj, "") {
		switch m.name {
		case "keymap":
			cfg.Keymap = s.keymap(m.value)
		case "fixendofline":
			if b, ok := s.boolean(m.value, "fixendofline"); ok {
				cfg.FixEndOfLine = &b
			}
		case "fileTypes":
			cfg.FileTypes = s.fileTypes(m.value)
		case "extensions":
			s.extensions(m.value, &cfg)
		default:
			s.report(m.nameOff, "未知の項目 %s を無視する", join("", m.name))
		}
	}
	return cfg, s.problems
}

// keymap は、keymap の値を読む。
func (s *source) keymap(v *hujson.Value) map[string]map[string]string {
	obj, ok := s.object(v, "keymap")
	if !ok {
		return nil
	}
	out := map[string]map[string]string{}
	for _, mode := range s.members(obj, "keymap") {
		path := join("keymap", mode.name)
		if !mapModes[mode.name] {
			s.report(mode.nameOff, "未知のモード %s を無視する", path)
			continue
		}
		mobj, ok := s.object(mode.value, path)
		if !ok {
			continue
		}
		maps := map[string]string{}
		for _, e := range s.members(mobj, path) {
			if keys, err := editcmd.ParseKeys(e.name); err != nil || len(keys) == 0 {
				s.report(e.nameOff, "%s: キーの列 %q を読めない%s", path, e.name, reason(err))
				continue
			}
			rhs, ok := s.str(e.value, join(path, e.name))
			if !ok {
				continue
			}
			if _, err := editcmd.ParseKeys(rhs); err != nil {
				s.report(e.value.StartOffset, "%s: 展開する列 %q を読めない: %v", join(path, e.name), rhs, err)
				continue
			}
			maps[e.name] = rhs
		}
		out[mode.name] = maps
	}
	return out
}

// reason は、err があれば ": <err>" を返す。
func reason(err error) string {
	if err == nil {
		return "（空の列）"
	}
	return ": " + err.Error()
}

// fileTypes は、fileTypes の値を読む。
func (s *source) fileTypes(v *hujson.Value) map[string]string {
	obj, ok := s.object(v, "fileTypes")
	if !ok {
		return nil
	}
	out := map[string]string{}
	for _, e := range s.members(obj, "fileTypes") {
		path := join("fileTypes", e.name)
		if e.name == "" {
			s.report(e.nameOff, "fileTypes の拡張子かファイル名が空")
			continue
		}
		name, ok := s.str(e.value, path)
		if !ok {
			continue
		}
		if name == "" {
			s.report(e.value.StartOffset, "%s のファイルの種類の名前が空", path)
			continue
		}
		out[e.name] = name
	}
	return out
}

// extensions は、extensions の値を読んで cfg に入れる。
func (s *source) extensions(v *hujson.Value, cfg *Config) {
	obj, ok := s.object(v, "extensions")
	if !ok {
		return
	}
	for _, m := range s.members(obj, "extensions") {
		path := join("extensions", m.name)
		switch m.name {
		case "syncTimeoutMs":
			if n, ok := s.nonNegInt(m.value, path, maxSyncTimeoutMs); ok {
				d := time.Duration(n) * time.Millisecond
				cfg.SyncTimeout = &d
			}
		case "memoryLimitMb":
			if n, ok := s.nonNegInt(m.value, path, math.MaxInt64>>20); ok {
				cfg.MemoryLimitMB = n
			}
		case "settings":
			cfg.ExtensionSettings = s.extensionSettings(m.value, cfg)
		case "activation":
			cfg.ExtensionActivation = s.extensionActivation(m.value, cfg)
		default:
			s.report(m.nameOff, "未知の項目 %s を無視する", path)
		}
	}
}

// notePos は、拡張機能の ID を書いた位置を cfg に記録する。
func (s *source) notePos(cfg *Config, id string, off int, path string) {
	if cfg.extPos == nil {
		cfg.extPos = map[string]*Error{}
	}
	if _, ok := cfg.extPos[id]; !ok {
		cfg.extPos[id] = s.at(off, "%s の拡張機能 %s が見つからない", path, id)
	}
}

// extensionSettings は、extensions.settings の値を読む。値の中身は拡張機能が解釈するので、形を問わない。
func (s *source) extensionSettings(v *hujson.Value, cfg *Config) map[string]json.RawMessage {
	obj, ok := s.object(v, "extensions.settings")
	if !ok {
		return nil
	}
	out := map[string]json.RawMessage{}
	for _, e := range s.members(obj, "extensions.settings") {
		var buf bytes.Buffer
		if err := json.Compact(&buf, s.raw(e.value)); err != nil {
			// 構文木から切り出した標準の JSON なので、ここには来ない。
			s.report(e.value.StartOffset, "%s を読めない: %v", join("extensions.settings", e.name), err)
			continue
		}
		out[e.name] = buf.Bytes()
		s.notePos(cfg, e.name, e.nameOff, "extensions.settings")
	}
	return out
}

// extensionActivation は、extensions.activation の値を読む。
func (s *source) extensionActivation(v *hujson.Value, cfg *Config) map[string][]ext.Activation {
	obj, ok := s.object(v, "extensions.activation")
	if !ok {
		return nil
	}
	out := map[string][]ext.Activation{}
	for _, e := range s.members(obj, "extensions.activation") {
		path := join("extensions.activation", e.name)
		arr, ok := s.array(e.value, path)
		if !ok {
			continue
		}
		var acts []ext.Activation
		for i := range arr.Elements {
			el := &arr.Elements[i]
			var a ext.Activation
			dec := json.NewDecoder(bytes.NewReader(s.raw(el)))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&a); err != nil {
				s.report(el.StartOffset, "%s の契機を読めない: %v", path, err)
				continue
			}
			if a.On != ext.ActivationFileType || a.Pattern == "" || a.ID != "" {
				s.report(el.StartOffset, `%s に足せるのは {"on": "filetype", "pattern": "<ファイルの種類>"} の契機だけ`, path)
				continue
			}
			acts = append(acts, a)
		}
		out[e.name] = acts
		s.notePos(cfg, e.name, e.nameOff, "extensions.activation")
	}
	return out
}
