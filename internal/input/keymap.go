package input

import (
	"errors"
	"fmt"
	"slices"
)

// BindingKind は、キーマップに登録する操作の種類を表す。
type BindingKind int

// 操作の種類。
const (
	// BindMotion は、モーションである。ノーマルモードでは移動、オペレーターの後では対象になる。
	BindMotion BindingKind = iota + 1
	// BindOperator は、対象を取るオペレーターである（d、c、y など）。
	BindOperator
	// BindTextObject は、オペレーターの後だけで使えるテキストオブジェクトである。
	BindTextObject
	// BindAction は、ノーマルモードで対象を取らずに完結する操作である（x、p、i など）。
	BindAction

	// bindLine は、オペレーターの重ね押し（dd）を表す、Parser の内部だけで使う種類である。
	bindLine
)

func (k BindingKind) String() string {
	switch k {
	case BindMotion:
		return "motion"
	case BindOperator:
		return "operator"
	case BindTextObject:
		return "text object"
	case BindAction:
		return "action"
	case bindLine:
		return "line"
	}
	return fmt.Sprintf("BindingKind(%d)", int(k))
}

// normalKinds と pendingKinds は、Parser の状態ごとに引く操作の種類である。
var (
	normalKinds  = []BindingKind{BindMotion, BindOperator, BindAction}
	pendingKinds = []BindingKind{BindMotion, BindTextObject}
)

// Binding は、キーの列と操作の対応の 1 件である。
type Binding struct {
	Kind BindingKind
	Keys []Key
	Ref  Ref
	// TakesChar は、キーの列の後に文字の引数を 1 つ取るかを表す（f、t、r など）。
	TakesChar bool
	// Mode は、実行の後に移るモードである（i や c ならインサートモード）。
	Mode Mode
}

// Keymap は、キーの列から操作を引く対応表である。
// 本体の操作（DefaultKeymap）と、拡張機能が宣言した操作（基本設計 5.1 の contributes）を同じ表に登録する。
// 並行に呼ばれる前提は置かない（基本設計 2.1）。
//
// req: FR-11
type Keymap struct {
	bindings []Binding
}

// NewKeymap は、空のキーマップを返す。
func NewKeymap() *Keymap { return &Keymap{} }

// ErrConflict は、同じキーの列が、同じ状態で引かれる別の種類の操作に登録済みであることを表す。
var ErrConflict = errors.New("キーの列が別の種類の操作に登録済みである")

// Bind は、操作を登録する。
// 同じ種類で同じキーの列の登録があれば置き換える（利用者の設定による上書きのため）。
//
// 次の場合はエラーを返し、何も登録しない。
//   - Keys か Ref.ID が空である
//   - Keys が 1 から 9 の数字で始まる（回数として読まれるため、引けない）
//   - 同じ状態で引かれる別の種類の操作に、同じキーの列が登録済みである（ErrConflict）。
//     例えばモーションの x は、ノーマルモードで操作の x と区別できない
func (m *Keymap) Bind(b Binding) error {
	switch b.Kind {
	case BindMotion, BindOperator, BindTextObject, BindAction:
	case bindLine:
		return fmt.Errorf("登録できない操作の種類である: %v", b.Kind)
	default:
		return fmt.Errorf("登録できない操作の種類である: %v", b.Kind)
	}
	if len(b.Keys) == 0 || b.Ref.ID == "" {
		return fmt.Errorf("キーの列と ID は空にできない: %q %q", KeysString(b.Keys), b.Ref.ID)
	}
	if first := b.Keys[0]; first.isPlain() && first.Rune >= '1' && first.Rune <= '9' {
		return fmt.Errorf("数字で始まるキーの列は回数として読まれる: %q", KeysString(b.Keys))
	}
	if b.Kind != BindTextObject && b.Keys[0] == Rune('"') {
		return fmt.Errorf("\" で始まるキーの列はレジスターの指定として読まれる: %q", KeysString(b.Keys))
	}
	b.Keys = slices.Clone(b.Keys)
	for i, old := range m.bindings {
		if !slices.Equal(old.Keys, b.Keys) {
			continue
		}
		if old.Kind == b.Kind {
			m.bindings[i] = b
			return nil
		}
		if sharesState(old.Kind, b.Kind) {
			return fmt.Errorf("%w: %q は %v の %s", ErrConflict, KeysString(b.Keys), old.Kind, old.Ref.ID)
		}
	}
	m.bindings = append(m.bindings, b)
	return nil
}

// Unbind は、指定した種類とキーの列の登録を取り除く。取り除いたかを返す。
func (m *Keymap) Unbind(kind BindingKind, keys []Key) bool {
	n := len(m.bindings)
	m.bindings = slices.DeleteFunc(m.bindings, func(b Binding) bool {
		return b.Kind == kind && slices.Equal(b.Keys, keys)
	})
	return len(m.bindings) != n
}

// Lookup は、指定した種類とキーの列の登録を返す。
func (m *Keymap) Lookup(kind BindingKind, keys []Key) (Binding, bool) {
	for _, b := range m.bindings {
		if b.Kind == kind && slices.Equal(b.Keys, keys) {
			return b, true
		}
	}
	return Binding{}, false
}

// match は、kinds の操作のうち、キーの列が seq と一致するものと、
// seq を真の接頭辞とするより長い登録があるかを返す。
func (m *Keymap) match(kinds []BindingKind, seq []Key) (exact *Binding, longer bool) {
	for i := range m.bindings {
		b := &m.bindings[i]
		if !slices.Contains(kinds, b.Kind) {
			continue
		}
		switch {
		case slices.Equal(b.Keys, seq):
			exact = b
		case len(b.Keys) > len(seq) && slices.Equal(b.Keys[:len(seq)], seq):
			longer = true
		}
	}
	return exact, longer
}

func sharesState(a, b BindingKind) bool {
	return (slices.Contains(normalKinds, a) && slices.Contains(normalKinds, b)) ||
		(slices.Contains(pendingKinds, a) && slices.Contains(pendingKinds, b))
}
