package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// settings は、利用者の設定のうち、この拡張機能に向けた部分（extensions.settings.formatter）である（基本設計 6.10）。
// 項目の名前は仮である。知らない項目は誤りにする（綴りの誤りで整形ツールが黙って走らないのを避ける）。
type settings struct {
	// Formatters は、整形ツールの列である。保存の前と format では、バッファのファイルの種類を FileTypes に含むものを、
	// 列の順にすべて走らせ、前の整形ツールの出力を次の整形ツールの入力にする（conform.nvim の、1 つのファイルの種類に並べた整形ツールと同じ）。
	Formatters []formatterConfig `json:"formatters"`
	// FormatOnSave は、保存の前に整形するかどうかである。省くと true である。false でも format（gq と :format）は使える。
	FormatOnSave *bool `json:"formatOnSave,omitempty"`
	// LocalBinDirs は、整形ツールの実行ファイルを PATH より先に探す、プロジェクトの依存の置き場所である。
	// ファイルのディレクトリから Git の根（Git の作業ツリーの外ならファイルシステムの根）へ向かって、
	// 各ディレクトリの下のこれらの相対パスを、列の順に探す（findCommand）。
	// 省くと defaultLocalBinDirs で、空の列を書くとプロジェクトの依存を探さない。
	LocalBinDirs []string `json:"localBinDirs,omitempty"`
	// TimeoutMs は、1 つの整形ツールの実行を待つ時間の上限のミリ秒である。省くか 0 なら defaultTimeout である。
	// 保存の前の整形は、本体の buffer/willSave の期限（extensions.willSaveTimeoutMs）でも打ち切られる。
	TimeoutMs int `json:"timeoutMs,omitempty"`
}

// formatterConfig は、1 つの整形ツールの設定である。
type formatterConfig struct {
	// Name は、知らせに使う名前である。省くと Command のファイル名である。
	Name string `json:"name,omitempty"`
	// Command は、整形ツールの実行ファイルの名前か絶対パスである。~/ で始めるとホームのディレクトリからのパスになる。
	// 名前なら、プロジェクトの依存（LocalBinDirs）、PATH の順に探す（findCommand）。
	Command string `json:"command"`
	// Args は、Command に渡す引数の列である。各引数の中の {path} は、バッファのファイルの絶対パスに置き換える。
	Args []string `json:"args,omitempty"`
	// FileTypes は、この整形ツールを使うファイルの種類の名前の列である。宣言ファイルの起動の契機もここから作る。
	FileTypes []string `json:"fileTypes"`
}

// defaultLocalBinDirs は、LocalBinDirs を省いたときの、プロジェクトの依存の置き場所である（#339 の本文）。
var defaultLocalBinDirs = []string{"node_modules/.bin", ".venv/bin"}

// defaultTimeout は、TimeoutMs を省いたときの、1 つの整形ツールの実行を待つ時間の上限（仮）である。
const defaultTimeout = 10 * time.Second

// pathPlaceholder は、Args の中で、バッファのファイルの絶対パスに置き換える文字列である。
const pathPlaceholder = "{path}"

// config は、settings を検査して既定値を埋めたものである。作った後は変えないので、goroutine の間で共有してよい。
type config struct {
	formatters   []formatterConfig
	formatOnSave bool
	localBinDirs []string
	timeout      time.Duration
}

// parseSettings は、initialize の settings を読んで検査する。settings が無ければ、整形ツールの無い設定を返す。
// 誤りはすべて 1 つの誤りにまとめて返す。
func parseSettings(raw json.RawMessage) (*config, error) {
	c := &config{formatOnSave: true, localBinDirs: defaultLocalBinDirs, timeout: defaultTimeout}
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return c, nil
	}
	var s settings
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("extensions.settings.formatter を読めない: %w", err)
	}
	var errs []error
	for i, f := range s.Formatters {
		where := fmt.Sprintf("extensions.settings.formatter.formatters[%d]", i)
		switch {
		case f.Command == "":
			errs = append(errs, fmt.Errorf("%s の command が空", where))
		case strings.ContainsRune(f.Command, '/') && !filepath.IsAbs(f.Command) && !strings.HasPrefix(f.Command, "~/"):
			errs = append(errs, fmt.Errorf("%s の command %q は、名前か絶対パス（~/ で始めてよい）にする", where, f.Command))
		}
		if len(f.FileTypes) == 0 || slices.Contains(f.FileTypes, "") {
			errs = append(errs, fmt.Errorf("%s の fileTypes に、空でないファイルの種類の名前を 1 つ以上書く", where))
		}
		if f.Name == "" {
			s.Formatters[i].Name = filepath.Base(f.Command)
		}
	}
	for _, d := range s.LocalBinDirs {
		if d == "" || filepath.IsAbs(d) {
			errs = append(errs, fmt.Errorf("extensions.settings.formatter.localBinDirs の %q は、空でない相対パスにする", d))
		}
	}
	if s.TimeoutMs < 0 {
		errs = append(errs, errors.New("extensions.settings.formatter.timeoutMs は 0 以上の整数にする"))
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	c.formatters = s.Formatters
	if s.FormatOnSave != nil {
		c.formatOnSave = *s.FormatOnSave
	}
	if s.LocalBinDirs != nil {
		c.localBinDirs = s.LocalBinDirs
	}
	if s.TimeoutMs > 0 {
		c.timeout = time.Duration(s.TimeoutMs) * time.Millisecond
	}
	return c, nil
}

// forFileType は、ファイルの種類 ft のバッファで走らせる整形ツールを、設定の順に返す。
func (c *config) forFileType(ft string) []formatterConfig {
	var out []formatterConfig
	for _, f := range c.formatters {
		if slices.Contains(f.FileTypes, ft) {
			out = append(out, f)
		}
	}
	return out
}
