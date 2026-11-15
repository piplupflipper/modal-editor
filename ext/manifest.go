package ext

import (
	"errors"
	"fmt"
	"strings"
)

// Manifest は、拡張機能の宣言ファイルの内容である（基本設計 5.1）。
// 本体は、拡張機能を起動せずに宣言ファイルだけを読み、追加する操作と起動の契機を知る。
// 項目の名前は仮である。
//
// 宣言ファイルは JSONC で書く。
// JSONC を読む方法は未決なので、このパッケージは読み込んだ後の値の型と検査（Validate）だけを持つ。
//
// req: FR-13
type Manifest struct {
	// ID は、拡張機能の ID である。ドットリピートの記録と設定で使う。
	ID string `json:"id"`
	// Command は、拡張機能の実行ファイルのパスである。
	// 相対パスは、宣言ファイルのあるディレクトリから解決する。
	Command string `json:"command"`
	// Args は、Command に渡す引数の列である（基本設計 6.7 の宣言ファイルに足す項目）。
	// スクリプトの拡張機能が、共通の実行ファイルにスクリプトのパスを渡すのに使う。無ければ省く。
	Args []string `json:"args,omitempty"`
	// SaveHook は、true なら、稼働中のときに保存の直前の buffer/willSave を受け取る（基本設計 6.7 の保存の流れ）。
	// 保存の前に整形する LSP の拡張機能が使う。
	SaveHook bool `json:"saveHook,omitempty"`
	// Activation は、起動の契機の一覧である。どれか 1 つが成り立った時点で起動する。
	Activation []Activation `json:"activation"`
	// Contributes は、拡張機能が追加する操作である。
	Contributes Contributes `json:"contributes"`
}

// ActivationKind は、起動の契機の種類である。
type ActivationKind string

// 起動の契機の種類。
//
// ActivationOperator、ActivationMotion、ActivationTextObject、ActivationCommand は、
// 同じ拡張機能が追加した、その種類の操作が呼ばれたときに成り立つ。
// ActivationFileType は、Pattern と一致する種類のファイルを開いたときに成り立つ。
const (
	ActivationOperator   ActivationKind = "operator"
	ActivationMotion     ActivationKind = "motion"
	ActivationTextObject ActivationKind = "textObject"
	ActivationCommand    ActivationKind = "command"
	ActivationFileType   ActivationKind = "filetype"
)

// Known は、k がこの版の API の知っている契機の種類かどうかを返す。
func (k ActivationKind) Known() bool {
	switch k {
	case ActivationOperator, ActivationMotion, ActivationTextObject, ActivationCommand, ActivationFileType:
		return true
	}
	return false
}

// Activation は、1 つの起動の契機である。
type Activation struct {
	// On は、契機の種類である。
	On ActivationKind `json:"on"`
	// ID は、操作が呼ばれたときの契機で、その操作の ID である。それ以外の契機では空。
	ID string `json:"id,omitempty"`
	// Pattern は、ActivationFileType の契機で、ファイルの種類の名前である。それ以外の契機では空。
	Pattern string `json:"pattern,omitempty"`
}

// Contributes は、拡張機能が追加する操作の一覧である。
type Contributes struct {
	Operators   []Contribution `json:"operators,omitempty"`
	Motions     []Contribution `json:"motions,omitempty"`
	TextObjects []Contribution `json:"textObjects,omitempty"`
	Commands    []Contribution `json:"commands,omitempty"`
}

// Contribution は、拡張機能が追加する 1 つの操作である。
type Contribution struct {
	// ID は、操作の ID である。拡張機能の中で一意にする。
	// 本体はドットリピートで、拡張機能の ID とこの ID を記録する（FR-12）。
	ID string `json:"id"`
	// Keys は、既定のキーの列（"ys" など）である。利用者の設定で上書きできる。
	// 既定のキーを持たない操作では空。
	Keys string `json:"keys,omitempty"`
}

// KnownActivation は、Activation のうち種類が既知の契機だけを返す。
// 本体は、未知の契機を無視し、この一覧だけで起動を判断する。
func (m *Manifest) KnownActivation() []Activation {
	var known []Activation
	for _, a := range m.Activation {
		if a.On.Known() {
			known = append(known, a)
		}
	}
	return known
}

// Validate は、宣言ファイルを読み込んだ後の値を検査する。
// 見つけた誤りをすべて errors.Join でまとめて err に返し、誤りが無ければ err は nil である。
// err が nil でない宣言ファイルは、拡張機能として使わない。
//
// 検査するのは次の項目である。
//
//   - ID と Command が空でないこと。ID が空白を含まないこと
//   - Activation が 1 つ以上あること
//   - 操作の契機は、同じ種類の操作として Contributes にある ID を指すこと
//   - ファイルの種類の契機は、Pattern が空でないこと
//   - Contributes の各操作の ID が空でなく、種類をまたいで重複しないこと
//
// 未知の種類の契機は誤りにせず、warnings に 1 件ずつ返す。
// API の後の版で契機の種類を足しても、古い本体が新しい拡張機能の宣言ファイルを丸ごと拒まないようにするためである。
// 本体は未知の契機だけを無視する（KnownActivation）。
// 契機がすべて未知なら、その拡張機能は起動しないが、誤りにはしない。
//
// Command のファイルが存在するかどうかは検査しない。
//
// req: FR-13
func (m *Manifest) Validate() (warnings []error, err error) {
	var errs []error
	if m.ID == "" {
		errs = append(errs, errors.New("id が空である"))
	} else if strings.ContainsFunc(m.ID, isSpace) {
		errs = append(errs, fmt.Errorf("id %q が空白を含む", m.ID))
	}
	if m.Command == "" {
		errs = append(errs, errors.New("command が空である"))
	}

	// 種類ごとの操作の ID の集合を作りながら、ID の空と重複を検査する。
	ids := map[ActivationKind]map[string]bool{}
	seen := map[string]string{}
	for _, group := range []struct {
		kind  ActivationKind
		field string
		items []Contribution
	}{
		{ActivationOperator, "operators", m.Contributes.Operators},
		{ActivationMotion, "motions", m.Contributes.Motions},
		{ActivationTextObject, "textObjects", m.Contributes.TextObjects},
		{ActivationCommand, "commands", m.Contributes.Commands},
	} {
		ids[group.kind] = map[string]bool{}
		for i, c := range group.items {
			path := fmt.Sprintf("contributes.%s[%d]", group.field, i)
			if c.ID == "" {
				errs = append(errs, fmt.Errorf("%s.id が空である", path))
				continue
			}
			if prev, ok := seen[c.ID]; ok {
				errs = append(errs, fmt.Errorf("%s.id %q が %s と重複する", path, c.ID, prev))
				continue
			}
			seen[c.ID] = path
			ids[group.kind][c.ID] = true
		}
	}

	if len(m.Activation) == 0 {
		errs = append(errs, errors.New("activation が空である"))
	}
	for i, a := range m.Activation {
		path := fmt.Sprintf("activation[%d]", i)
		switch a.On {
		case ActivationOperator, ActivationMotion, ActivationTextObject, ActivationCommand:
			if a.ID == "" {
				errs = append(errs, fmt.Errorf("%s.id が空である", path))
			} else if !ids[a.On][a.ID] {
				errs = append(errs, fmt.Errorf("%s.id %q が contributes の %s に無い", path, a.ID, a.On))
			}
		case ActivationFileType:
			if a.Pattern == "" {
				errs = append(errs, fmt.Errorf("%s.pattern が空である", path))
			}
		default:
			warnings = append(warnings, fmt.Errorf("%s.on %q は未知の契機なので無視する", path, a.On))
		}
	}
	return warnings, errors.Join(errs...)
}

// ValidateManifests は、複数の宣言ファイルを検査する。
// 各宣言ファイルの Validate に加えて、拡張機能の ID が重複しないことを検査する。
// 誤りと警告には、どの宣言ファイルのものかを前に付ける。
// 誤りはすべて errors.Join でまとめて err に返し、誤りが無ければ err は nil である。
//
// req: FR-13
func ValidateManifests(ms []Manifest) (warnings []error, err error) {
	var errs []error
	seen := map[string]int{}
	for i := range ms {
		m := &ms[i]
		ws, verr := m.Validate()
		for _, w := range ws {
			warnings = append(warnings, fmt.Errorf("宣言ファイル[%d]（id %q）: %w", i, m.ID, w))
		}
		if verr != nil {
			errs = append(errs, fmt.Errorf("宣言ファイル[%d]（id %q）: %w", i, m.ID, verr))
		}
		if m.ID == "" {
			continue
		}
		if prev, ok := seen[m.ID]; ok {
			errs = append(errs, fmt.Errorf("宣言ファイル[%d] の id %q が宣言ファイル[%d] と重複する", i, m.ID, prev))
			continue
		}
		seen[m.ID] = i
	}
	return warnings, errors.Join(errs...)
}

func isSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\v' || r == '\f'
}
