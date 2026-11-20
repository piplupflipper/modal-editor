package input

import (
	"errors"
	"fmt"
	"slices"

	"github.com/piplupflipper/vi-imitation/internal/editcmd"
)

// BindingKind は、キーマップに登録する操作の種類を表す。
type BindingKind int

// 操作の種類。
const (
	// BindMotion は、モーションである。ノーマルモードでは移動、オペレーターの後では対象になる。
	BindMotion BindingKind = iota + 1
	// BindOperator は、対象を取るオペレーターである（d、c、y など）。
	BindOperator
	// BindTextObject は、オペレーターの後（とビジュアルモード）で使うテキストオブジェクトである。
	BindTextObject
	// BindAction は、対象を取らずに完結する操作である（x、p、i など）。
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

// MapMode は、登録が有効なモードの集合を表すビットの集合である。
// 基本設計 7 章の設定の keymap がモードごとであることに合わせた軸である。
//
// Parser.Feed が引くのは MapNormal、MapVisual（Parser.SetVisual の後）、MapOperatorPending である。
// MapInsert は、インサートモードのカーソルの移動と削除のキー（1 つのキー）を引く Parser.InsertAction が使う（FR-127）。
// インサートモードの文字、<CR>、<Tab>、<Esc> は、1 文字の入力の速さに触れないために表を通さないので（要件定義 8.1 の計測の対象）、
// MapInsert には登録できない（Bind）。
type MapMode uint8

// 登録が有効なモード。
const (
	MapNormal          MapMode = 1 << iota // ノーマルモード
	MapVisual                              // ビジュアルモード
	MapOperatorPending                     // オペレーターの後（対象を待つ間）
	MapInsert                              // インサートモード
)

// allowedModes は、操作の種類ごとに登録できるモードである。
var allowedModes = map[BindingKind]MapMode{
	BindMotion:     MapNormal | MapVisual | MapOperatorPending,
	BindOperator:   MapNormal | MapVisual,
	BindTextObject: MapVisual | MapOperatorPending,
	BindAction:     MapNormal | MapVisual | MapInsert,
}

// defaultModes は、Binding.Modes を省略したときのモードである。
var defaultModes = map[BindingKind]MapMode{
	BindMotion:     MapNormal | MapVisual | MapOperatorPending,
	BindOperator:   MapNormal | MapVisual,
	BindTextObject: MapVisual | MapOperatorPending,
	BindAction:     MapNormal,
}

// DefaultModes は、Binding.Modes を省いたときの、操作の種類 kind の有効なモード（defaultModes）を返す。
func DefaultModes(kind BindingKind) MapMode { return defaultModes[kind] }

// Binding は、キーの列と操作の対応の 1 件である。
type Binding struct {
	Kind BindingKind
	// Modes は、登録が有効なモードの集合である。0 なら種類ごとの既定（defaultModes）にする。
	Modes MapMode
	Keys  []editcmd.Key
	Ref   editcmd.Ref
	// TakesChar は、文字の引数を 1 つ取るかを表す。
	// モーション（f、t）と操作（r）ではキーの列の直後に、オペレーターでは対象の後に取る
	// （拡張機能の surround の ysiw) の ')'）。テキストオブジェクトではキーの列の直後に取る。
	TakesChar bool
	// TakesSecondChar は、操作（BindAction）が TakesChar の文字に続けて、2 つ目の文字の引数を取るかを表す
	// （本体の sr{前の文字}{後の文字}）。2 つ目の文字は Command.Char2 に入る。
	// TakesChar が false の登録と、操作でない登録では使わない（Bind が拒む）。
	TakesSecondChar bool
	// NextMode は、実行の後に移るモードである（i や c ならインサートモード）。
	NextMode editcmd.Mode
	// Description は、キーの一覧（FR-90。基本設計 7.2）に出す説明文である。
	// 本体の操作は表の説明文、拡張機能の操作は宣言ファイルの contributes の description（基本設計 5.1）を入れる。空でもよい。
	// キーの解釈には使わない。
	Description string
	// User は、利用者の設定の keymap に操作の ID で書いた割り当て（UserBinding）であるかを表す。
	// キーの一覧（FR-90）で出どころを「設定」にするのに使う。キーの解釈には使わない。
	User bool
}

// Keymap は、キーの列から操作を引く対応表である。
// 本体の操作（DefaultKeymap）と、拡張機能が宣言した操作（基本設計 5.1 の contributes）と、
// 利用者の設定の keymap に操作の ID で書いた割り当て（基本設計 7 章。UserBinding と Override。FR-126）を同じ表に登録する。
//
// 利用者の設定の keymap のうち、右辺をキーの列で書いたもの（キーの列を別のキーの列に展開する）は、この表ではなく、
// Parser の前段の別の層（internal/editor の remapper）が扱う。
//
// 並行に呼ばれる前提は置かない（基本設計 2.1）。
//
// req: FR-11
type Keymap struct {
	bindings []Binding
	// overridden は、Override が取り除いた登録である（取り除いたモードだけを Modes に持つ）。キーの一覧（FR-90）に出すために残す。
	overridden []Binding
}

// NewKeymap は、空のキーマップを返す。
func NewKeymap() *Keymap { return &Keymap{} }

// ErrConflict は、同じキーの列が、同じモードで有効な登録に使われていることを表す。
var ErrConflict = errors.New("キーの列が同じモードの登録に使われている")

// Bind は、操作を登録する。既存の登録は置き換えない。
// 置き換えたい場合は、先に Unbind で取り除く。
//
// 次の場合はエラーを返し、何も登録しない。
//   - Keys か Ref.ID が空である。Modes に、その種類で登録できないモードが含まれる
//   - Keys が、Parser が先に読むキーで始まる。ノーマル、ビジュアル、オペレーターの後での 1 から 9 の数字（回数）、
//     ノーマルとビジュアルでの "（レジスター）、ノーマルでの q、@、.（マクロとドットリピート）
//   - インサートモード（MapInsert）の登録で、Keys が 1 つのキーでないか、修飾キーの付かない文字、<Esc>、<CR>、<Tab> である
//     （checkInsertKeys。インサートモードの解釈はこれらのキーで表を引かない）
//   - 同じキーの列が、モードの重なる登録に使われている（ErrConflict）。
//     本体の操作と拡張機能の操作の衝突も、拡張機能どうしの衝突も同じく拒む
func (m *Keymap) Bind(b Binding) error {
	if err := normalizeBinding(&b); err != nil {
		return err
	}
	for _, old := range m.bindings {
		if old.Modes&b.Modes != 0 && slices.Equal(old.Keys, b.Keys) {
			return fmt.Errorf("%w: %q は %v の %s（拡張機能 %q）", ErrConflict,
				editcmd.KeysString(b.Keys), old.Kind, old.Ref.ID, old.Ref.Extension)
		}
	}
	b.Keys = slices.Clone(b.Keys)
	m.bindings = append(m.bindings, b)
	return nil
}

// Override は、b を登録する。b.Modes のどれかで有効な、キーの列が同じ登録（本体の既定のキー、拡張機能のキー）があれば、
// そのモードから取り除いてから登録する（利用者の設定が既定に勝つ。FR-126）。取り除いた登録は Overridden で読める。
// b そのものが Bind の規則（衝突を除く）に反するときは、何も取り除かずにエラーを返す。
//
// req: FR-126
func (m *Keymap) Override(b Binding) error {
	if err := normalizeBinding(&b); err != nil {
		return err
	}
	for _, old := range m.bindings {
		if mode := old.Modes & b.Modes; mode != 0 && slices.Equal(old.Keys, b.Keys) {
			old.Modes = mode
			old.Keys = slices.Clone(old.Keys)
			m.overridden = append(m.overridden, old)
		}
	}
	m.Unbind(b.Modes, b.Keys)
	return m.Bind(b)
}

// Overridden は、Override が取り除いた登録を、取り除いた順に返す。各登録の Modes は取り除いたモードである。
// キーの一覧（FR-90）で、効かなくなった既定のキーに印を付けて出すのに使う。返す値は写しである。
func (m *Keymap) Overridden() []Binding {
	out := make([]Binding, len(m.overridden))
	for i, b := range m.overridden {
		b.Keys = slices.Clone(b.Keys)
		out[i] = b
	}
	return out
}

// normalizeBinding は、b の Modes を既定で埋め、Bind の規則（衝突を除く）に反していればエラーを返す。
func normalizeBinding(b *Binding) error {
	allowed, ok := allowedModes[b.Kind]
	if !ok {
		return fmt.Errorf("登録できない操作の種類である: %v", b.Kind)
	}
	if b.Modes == 0 {
		b.Modes = defaultModes[b.Kind]
	}
	if b.Modes&^allowed != 0 {
		return fmt.Errorf("%v は、指定したモード（%04b）に登録できない", b.Kind, b.Modes)
	}
	if len(b.Keys) == 0 || b.Ref.ID == "" {
		return fmt.Errorf("キーの列と ID は空にできない: %q %q", editcmd.KeysString(b.Keys), b.Ref.ID)
	}
	if b.TakesSecondChar && (b.Kind != BindAction || !b.TakesChar) {
		return fmt.Errorf("2 つ目の文字の引数は、文字の引数を取る操作にだけ付けられる: %q", editcmd.KeysString(b.Keys))
	}
	if reservedFirstKey(b.Keys[0], b.Modes) {
		return fmt.Errorf("%q は、回数、レジスター、マクロ、ドットリピートとして先に読まれるキーで始まる", editcmd.KeysString(b.Keys))
	}
	if b.Modes&MapInsert != 0 {
		if err := checkInsertKeys(b.Keys); err != nil {
			return err
		}
	}
	return nil
}

// checkInsertKeys は、keys をインサートモードの登録の左辺に使えるかを確かめる。
// インサートモードの解釈（internal/command の State.insertKey）は、1 つのキーごとに表を引き、
// 修飾キーの付かない文字、<Esc>、<CR>、<Tab> では表を引かない（1 文字の入力の速さに触れないため。要件定義 8.1）。
// このため、2 つ以上のキーの列と、表を引かないキーは登録できない。
// jk のようなキーの列は、設定の keymap の右辺をキーの列で書く（internal/editor の remapper）。
//
// req: FR-127
func checkInsertKeys(keys []editcmd.Key) error {
	if len(keys) != 1 {
		return fmt.Errorf("インサートモードの左辺は 1 つのキーにする: %q", editcmd.KeysString(keys))
	}
	if !InsertTableKey(keys[0]) {
		return fmt.Errorf("インサートモードの %q は文字の入力かモードの切り替えのキーなので、操作を割り当てられない", editcmd.KeysString(keys))
	}
	return nil
}

// InsertTableKey は、インサートモードで k が操作の表を引くキーかを返す。
// 修飾キーの付かない文字、<Esc>、<CR>、<Tab> は表を引かない（State.insertKey の分岐と同じ規則）。
func InsertTableKey(k editcmd.Key) bool {
	if k.Mod != 0 {
		return true
	}
	switch k.Code {
	case editcmd.CodeRune, editcmd.CodeEsc, editcmd.CodeEnter, editcmd.CodeTab:
		return false
	case editcmd.CodeBackspace, editcmd.CodeDelete, editcmd.CodeUp, editcmd.CodeDown, editcmd.CodeLeft, editcmd.CodeRight,
		editcmd.CodeHome, editcmd.CodeEnd, editcmd.CodePageUp, editcmd.CodePageDown, editcmd.CodeInsert, editcmd.CodeF1:
	}
	return true
}

// reservedFirstKey は、k が modes のどれかで Parser の先に読むキーかを返す。
// 先に読むキーの表は editcmd.LeadRoleOf にあり、設定の leader の検査（editcmd.ParseLeader）と共有する。
func reservedFirstKey(k editcmd.Key, modes MapMode) bool {
	switch editcmd.LeadRoleOf(k) {
	case editcmd.LeadCount:
		return modes&(MapNormal|MapVisual|MapOperatorPending) != 0
	case editcmd.LeadRegister:
		return modes&(MapNormal|MapVisual) != 0
	case editcmd.LeadMacro:
		return modes&MapNormal != 0
	case editcmd.LeadNone:
	}
	return false
}

// Unbind は、mode（1 つ以上のモード）で有効な、キーの列が keys の登録を、そのモードから取り除く。
// 有効なモードが無くなった登録は表から消す。取り除いたかを返す。
func (m *Keymap) Unbind(mode MapMode, keys []editcmd.Key) bool {
	removed := false
	for i := range m.bindings {
		b := &m.bindings[i]
		if b.Modes&mode != 0 && slices.Equal(b.Keys, keys) {
			b.Modes &^= mode
			removed = true
		}
	}
	m.bindings = slices.DeleteFunc(m.bindings, func(b Binding) bool { return b.Modes == 0 })
	return removed
}

// Lookup は、mode で有効な、キーの列が keys の登録を返す。
// 同じモードで同じキーの列の登録は Bind が拒むので、高々 1 件である。
func (m *Keymap) Lookup(mode MapMode, keys []editcmd.Key) (Binding, bool) {
	for _, b := range m.bindings {
		if b.Modes&mode != 0 && slices.Equal(b.Keys, keys) {
			return b, true
		}
	}
	return Binding{}, false
}

// Bindings は、登録をすべて、登録した順に返す。キーの一覧（FR-90）を作るのに使う。
// 返す値は写しで、変えても Keymap に影響しない。
func (m *Keymap) Bindings() []Binding {
	out := make([]Binding, len(m.bindings))
	for i, b := range m.bindings {
		b.Keys = slices.Clone(b.Keys)
		out[i] = b
	}
	return out
}

// match は、mode で有効な登録のうち、キーの列が seq と一致するものと、
// seq を真の接頭辞とするより長い登録があるかを返す。
func (m *Keymap) match(mode MapMode, seq []editcmd.Key) (exact *Binding, longer bool) {
	for i := range m.bindings {
		b := &m.bindings[i]
		if b.Modes&mode == 0 {
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
