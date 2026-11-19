package ext

import "fmt"

// ポップアップと表示の部品（基本設計 1.4、5.3 の段階 3 の表。FR-68〜FR-72）。
//
// 拡張機能は、ポップアップに出す中身を表示の部品（テキスト、一覧、木、入力の行）のデータで渡す（FR-72）。
// 描き方は本体（internal/ui）が決め、拡張機能が端末のセルを直接描く手段は持たない（#177 の裁定）。
// 本体は、開いたポップアップに ID（ViewID）を付けて応答し、以後の更新、閉じる、利用者の操作の知らせは、この ID で指す。
//
// メソッドと向き（名前は仮）:
//
//   - popup/open（拡張機能 → 本体、リクエスト）: PopupOpenParams で部品を渡して開く。応答は PopupOpenResult
//   - pane/open（拡張機能 → 本体、リクエスト）: PaneOpenParams で部品を渡し、端末多重化ソフトで分割したペインに開く（FR-115）。
//     応答は PaneOpenResult。ペインもポップアップと同じ ID で指し、以下の通知はペインにも使う
//   - view/update（拡張機能 → 本体、通知）: 開いたポップアップの部品の中身を置き換える（ViewUpdateParams）
//   - view/close（拡張機能 → 本体、通知）: 開いたポップアップを閉じる（ViewCloseParams）。本体は view/closed を返さない
//   - view/closed（本体 → 拡張機能、通知）: 利用者が閉じたか、本体が閉じた（別のポップアップで置き換えた、client が終わった）ことを
//     知らせる（ViewClosedParams）。その ID はもう使えない
//   - view/select（本体 → 拡張機能、通知）: 一覧か木の項目を利用者が選んだ（<CR>）こと、入力の行で <CR> を打ったことを知らせる（ViewSelectParams）
//   - input/change（本体 → 拡張機能、通知）: 入力の行の中身が変わったことを知らせる（InputChangeParams）
//   - view/key（本体 → 拡張機能、通知）: 宣言ファイルの contributes.popupKeys のキーを、その拡張機能のポップアップの中で打ったことを
//     知らせる（ViewKeyParams。FR-69）
//   - view/expand（本体 → 拡張機能、通知）: 木の項目を利用者が開閉したことを知らせる（ViewExpandParams）
//
// 本体は、閉じた後や知らない ID の view/update と view/close を黙って捨てる（閉じた知らせと行き違いうるため）。
const (
	// MethodPopupOpen は、拡張機能から本体へのリクエストである。params は PopupOpenParams、result は PopupOpenResult。
	MethodPopupOpen = "popup/open"
	// MethodPaneOpen は、拡張機能から本体へのリクエストである。params は PaneOpenParams、result は PaneOpenResult。
	MethodPaneOpen = "pane/open"
	// MethodViewUpdate は、拡張機能から本体への通知である。params は ViewUpdateParams。
	MethodViewUpdate = "view/update"
	// MethodViewClose は、拡張機能から本体への通知である。params は ViewCloseParams。
	MethodViewClose = "view/close"
	// MethodViewClosed は、本体から拡張機能への通知である。params は ViewClosedParams。
	MethodViewClosed = "view/closed"
	// MethodViewSelect は、本体から拡張機能への通知である。params は ViewSelectParams。
	MethodViewSelect = "view/select"
	// MethodInputChange は、本体から拡張機能への通知である。params は InputChangeParams。
	MethodInputChange = "input/change"
	// MethodViewKey は、本体から拡張機能への通知である。params は ViewKeyParams。
	MethodViewKey = "view/key"
	// MethodViewExpand は、本体から拡張機能への通知である。params は ViewExpandParams。
	MethodViewExpand = "view/expand"
)

// ViewID は、本体が開いたポップアップに付けた番号である。本体の中で一意で、使い回さない。0 は使わない。
type ViewID uint64

// ViewKind は、表示の部品の種類である（基本設計 1.4）。
type ViewKind string

// 表示の部品の種類。
const (
	// ViewText は、テキストのバッファである。本体は、編集を禁止したファイルでないバッファとして出し、通常のノーマルモードのキー
	// （移動、検索、ヤンク）が効く（FR-71）。
	ViewText ViewKind = "text"
	// ViewList は、項目の一覧である。1 項目を 1 行に出し、<CR> で選ぶと view/select で項目の ID を知らせる。
	ViewList ViewKind = "list"
	// ViewTree は、項目の木である。子のある項目（HasChildren を含む）の <CR> は開閉で view/expand を送り、
	// 子の無い項目の <CR> は view/select で ID を知らせる。view/update では、項目の ID で開閉とカーソルの項目を保つ。
	ViewTree ViewKind = "tree"
	// ViewInput は、1 行の入力である。中身が変わるたびに input/change で知らせ、<CR> で view/select（Item は中身）を送る。
	ViewInput ViewKind = "input"
	// ViewFinder は、入力の行と一覧を組にした部品である（ファジーファインダー。FR-106。API 0.9.0 で足した）。
	// Text は入力の行の最初の中身、Items は一覧の項目（子を持たない）である。
	// フォーカスは入力の行に置き、開いた時点でインサートモードにする。入力の行が変わるたびに input/change（Seq 付き）で知らせ、
	// 拡張機能は絞り込んだ一覧を view/update（同じ Seq 付き）で返す。本体は、最後に知らせた Seq より古い Seq の一覧を出さない。
	// view/update は一覧（Items）と見出し（Title）だけを置き換え、入力の行の中身（Text）は読まない（利用者が打ち続けているため）。
	// 一覧の中のカーソルは <C-n>、<C-p>、<C-j>、<C-k>、<Down>、<Up> で動かし、<CR> で view/select（Item は一覧の項目の ID、
	// Input は入力の行の中身）を送る。<Esc> はインサートモードでも閉じる。
	ViewFinder ViewKind = "finder"
)

// Known は、k がこの版の API の知っている部品の種類かを返す。
func (k ViewKind) Known() bool {
	switch k {
	case ViewText, ViewList, ViewTree, ViewInput, ViewFinder:
		return true
	}
	return false
}

// ViewComponent は、ポップアップに出す表示の部品 1 つである。Kind によって使う項目が違う。
type ViewComponent struct {
	Kind ViewKind `json:"kind"`
	// Title は、ポップアップの枠の上に出す見出しである。空なら出さない。
	Title string `json:"title,omitempty"`
	// Text は、ViewText の本文と、ViewInput と ViewFinder の入力の行の最初の中身である。ViewInput と ViewFinder では改行を含めない。
	Text string `json:"text,omitempty"`
	// Format は、ViewText の本文の形式である。PopupMarkdown なら、本体はコードの囲みの行を除いて出す（popup/show と同じ）。
	Format PopupFormat `json:"format,omitempty"`
	// Highlights は、ViewText の本文に色を付ける範囲である。位置は Format の整形の前の Text を指す（4.1 の位置）。
	// Markdown で本体が除くコードの囲みの行の中の範囲（始まりか終わりがその行にあるもの）は捨てる。
	// Style は 7.1 の色の名前（"keyword"、"diagnostic.error" など）で、知らない名前には色を付けない。
	Highlights []Decoration `json:"highlights,omitempty"`
	// Items は、ViewList、ViewTree、ViewFinder の項目である。ViewTree では Children で木にする。
	Items []ViewItem `json:"items,omitempty"`
}

// ViewItem は、一覧か木の項目 1 つである。
type ViewItem struct {
	// ID は、選んだときに view/select で返す値である。部品の中で一意にする。
	ID string `json:"id"`
	// Label は、表示の文字列である。改行を含めない。
	Label string `json:"label"`
	// Highlights は、Label の中の色を付ける範囲である。
	Highlights []ViewSpan `json:"highlights,omitempty"`
	// Children は、ViewTree の子の項目である。ViewList では使わない。
	Children []ViewItem `json:"children,omitempty"`
	// HasChildren は、ViewTree で、Children が空でも子を持つ項目（子を後から読む項目）であることを表す。
	// 本体は開閉の印を付け、<CR> で開くと view/expand で知らせる。拡張機能は子を読んで view/update で送る
	// （ファイラのディレクトリ、DAP の変数の variablesReference。FR-105、FR-108）。ViewList では使わない。
	HasChildren bool `json:"hasChildren,omitempty"`
	// Expanded は、ViewTree で子を開いて出すかの指定である。開閉は本体が項目の ID ごとに持つ。
	// 省くと、前の木にあった ID の項目は利用者が開閉した状態を保ち、新しく現れた ID の項目は閉じて出す。
	// 書くと、前の木にあった項目もその状態にする（ファイラの「今のファイルの場所まで開く」、DAP の止まった位置のスコープを開く）。
	Expanded *bool `json:"expanded,omitempty"`
}

// ViewExpandParams は、view/expand の params である。
// 木の子を持つ項目（Children か HasChildren のある項目）を、利用者が <CR> で開いたか閉じたかを知らせる。
type ViewExpandParams struct {
	View ViewID `json:"view"`
	// Item は、開閉した項目の ID である。
	Item string `json:"item"`
	// Expanded は、開いたなら true、閉じたなら false である。
	Expanded bool `json:"expanded"`
}

// ViewSpan は、項目の表示の文字列の中の、色を付けるバイトの範囲 [Start, End) である。
type ViewSpan struct {
	Start int `json:"start"`
	End   int `json:"end"`
	// Style は、7.1 の色の名前である。
	Style string `json:"style"`
}

// Validate は、部品の種類が既知であることと、項目の ID が空でなく部品の中で重複しないこと、
// 表示の文字列と入力の行の中身が改行を含まないこと、範囲が表示の文字列の中にあることを検査する。
// 本体は、誤りのある popup/open に JSON-RPC のエラーで応答し、誤りのある view/update は捨てる。
func (c *ViewComponent) Validate() error {
	if !c.Kind.Known() {
		return fmt.Errorf("kind %q は知らない部品の種類である", c.Kind)
	}
	if (c.Kind == ViewInput || c.Kind == ViewFinder) && containsNewline(c.Text) {
		return fmt.Errorf("%s の text が改行を含む", c.Kind)
	}
	seen := map[string]bool{}
	var check func(items []ViewItem, path string) error
	check = func(items []ViewItem, path string) error {
		for i, it := range items {
			p := fmt.Sprintf("%s[%d]", path, i)
			switch {
			case it.ID == "":
				return fmt.Errorf("%s.id が空である", p)
			case seen[it.ID]:
				return fmt.Errorf("%s.id %q が重複する", p, it.ID)
			case containsNewline(it.Label):
				return fmt.Errorf("%s.label が改行を含む", p)
			case c.Kind != ViewTree && (len(it.Children) > 0 || it.HasChildren):
				return fmt.Errorf("%s.children と hasChildren は tree でだけ使える", p)
			}
			seen[it.ID] = true
			for j, s := range it.Highlights {
				if s.Start < 0 || s.End < s.Start || s.End > len(it.Label) {
					return fmt.Errorf("%s.highlights[%d] が label の外を指す", p, j)
				}
			}
			if err := check(it.Children, p+".children"); err != nil {
				return err
			}
		}
		return nil
	}
	return check(c.Items, "items")
}

func containsNewline(s string) bool {
	for i := range len(s) {
		if s[i] == '\n' || s[i] == '\r' {
			return true
		}
	}
	return false
}

// PopupPlacement は、ポップアップの置き場所である（基本設計 1.4）。
type PopupPlacement string

// ポップアップの置き場所。
const (
	// PopupAtCursor は、編集の client の画面の中の、カーソルの近くに浮かべる窓である（既定）。
	// Focus が false なら、ホバーと同じく次のキーかカーソルの移動で閉じ、<C-w>w でフォーカスを入れられる。
	PopupAtCursor PopupPlacement = "cursor"
	// PopupCenter は、中央のポップアップである。開いている間はそれだけを操作する（FR-68）。
	// tmux と herdr の中では、端末多重化ソフトのポップアップの中の部品の client が描き（FR-66、FR-67）、
	// 外と、端末多重化ソフトのポップアップを開けないときは、編集の client の画面の中央に描く。
	PopupCenter PopupPlacement = "center"
)

// PopupOpenParams は、popup/open の params である。
// 本体は、その拡張機能へ最後に問い合わせを始めた client（基本設計 1.2）の画面に出す。
// 1 つの client に出すポップアップは 1 つで、新しいポップアップは前のものを置き換える（前のものの持ち主へ view/closed を送る）。
type PopupOpenParams struct {
	Component ViewComponent `json:"component"`
	// Placement は、置き場所である。省くと PopupAtCursor である。
	Placement PopupPlacement `json:"placement,omitempty"`
	// Focus は、PopupAtCursor のポップアップに、開いた時点でフォーカスを入れるかである。PopupCenter では常に入れる。
	Focus bool `json:"focus,omitempty"`
}

// Validate は、Component と Placement を検査する。
func (p *PopupOpenParams) Validate() error {
	switch p.Placement {
	case "", PopupAtCursor, PopupCenter:
	default:
		return fmt.Errorf("placement %q は知らない置き場所である", p.Placement)
	}
	return p.Component.Validate()
}

// PopupOpenResult は、popup/open の result である。
type PopupOpenResult struct {
	// View は、開いたポップアップの ID である。
	View ViewID `json:"view"`
}

// PaneDirection は、pane/open でペインを分割する向きである。
type PaneDirection string

// ペインを分割する向き。新しいペインは、元の client のペインの右か下に開く。
const (
	// PaneRight は、元の client のペインの右に開く（既定）。
	PaneRight PaneDirection = "right"
	// PaneDown は、元の client のペインの下に開く。
	PaneDown PaneDirection = "down"
)

// PaneOpenParams は、pane/open の params である（基本設計 1.2、1.3、5.3。FR-115、FR-123）。
//
// 本体は、その拡張機能へ最後に問い合わせを始めた client（元の client）のペインを、端末多重化ソフト（tmux、herdr）で分割し、
// 新しいペインの中で部品を描く client（ペインの部品の client）を起動する。拡張機能は端末多重化ソフトを直接操作しない。
// ペインの中で打ったキーと選んだ項目は、元の client の操作として扱い、view/select などで知らせる。
// ペインの中の <Esc> はペインを閉じない（ポップアップと違う）。利用者が端末多重化ソフトでペインを閉じるか、ペインの中で :q を打つと閉じ、
// view/closed を送る。
//
// 端末多重化ソフトの外（FR-60）と、ペインを開けなかったとき（herdr のプラグインを登録していないなど）は、ペインに代えて
// 元の client の画面の中央のポップアップに出す（FR-123）。このときは PopupCenter のポップアップと同じく <Esc> で閉じる。
// どちらでも応答の View は同じ扱いで、拡張機能は違いを知らずに view/update と view/close を送れる。
type PaneOpenParams struct {
	Component ViewComponent `json:"component"`
	// Direction は、分割の向きである。省くと PaneRight である。
	Direction PaneDirection `json:"direction,omitempty"`
	// Size は、新しいペインの大きさを、元のペインの分割の向きの大きさに対する百分率（1〜99）で表す。0 なら端末多重化ソフトの既定である。
	// herdr 0.9.0 は分割の大きさを受け付けないので、herdr では使わない（基本設計 1.3）。
	Size int `json:"size,omitempty"`
	// Focus は、開いた新しいペインへフォーカスを移すかである。
	Focus bool `json:"focus,omitempty"`
}

// Validate は、Component、Direction、Size を検査する。
func (p *PaneOpenParams) Validate() error {
	switch p.Direction {
	case "", PaneRight, PaneDown:
	default:
		return fmt.Errorf("direction %q は知らない向きである", p.Direction)
	}
	if p.Size < 0 || p.Size > 99 {
		return fmt.Errorf("size %d は 0〜99 でない", p.Size)
	}
	return p.Component.Validate()
}

// PaneOpenResult は、pane/open の result である。
type PaneOpenResult struct {
	// View は、開いたペイン（端末多重化ソフトの外では、代わりのポップアップ）の ID である。
	View ViewID `json:"view"`
}

// ViewUpdateParams は、view/update の params である。
// 本体は、部品の種類が開いたときと同じ場合だけ置き換える。
// 一覧と木では、カーソルのあった項目の ID が新しい中身にもあれば、その項目の行へカーソルを置く。無ければ、行の番号を行数に収まる範囲で保つ。
// テキストと入力の行では、行の番号を保つ。ただしテキストでカーソルが最後の行にあれば、新しい中身の最後の行へ置く
// （書き足した出力の末尾を追う。#244）。木の開閉の状態は、項目の ID ごとに保つ（ViewItem.Expanded）。
//
// ViewFinder では、一覧（Items）と見出し（Title）だけを置き換え、Component.Text は読まない。
// Seq は、この一覧がどの入力に答えたものかを、input/change の Seq（開いたときの中身なら 0）で表す。
// 本体は、最後に送った input/change の Seq より小さい Seq の view/update を捨てる（古い入力への一覧を出さない。基本設計 6.9）。
// 同じ Seq の view/update は何度送ってもよい（候補を読みながら一覧を送り直すとき）。
// 前の view/update より大きい Seq の一覧では、カーソルを先頭の項目へ置き、同じ Seq なら項目の ID で保つ。
// ViewFinder 以外では Seq を読まない。
type ViewUpdateParams struct {
	View      ViewID        `json:"view"`
	Component ViewComponent `json:"component"`
	Seq       uint64        `json:"seq,omitempty"`
}

// ViewCloseParams は、view/close の params である。
type ViewCloseParams struct {
	View ViewID `json:"view"`
}

// ViewClosedParams は、view/closed の params である。
type ViewClosedParams struct {
	View ViewID `json:"view"`
}

// ViewSelectParams は、view/select の params である。
type ViewSelectParams struct {
	View ViewID `json:"view"`
	// Item は、選んだ項目の ID（ViewList、ViewTree、ViewFinder）か、入力の行の中身（ViewInput）である。
	// ViewFinder で一覧が空なら空である。
	Item string `json:"item"`
	// Input は、ViewFinder の入力の行の中身である。他の部品では省く。
	Input string `json:"input,omitempty"`
	// Selection は、ViewList、ViewTree、ViewFinder の項目の選択に本体が付ける番号である（1 から。API 0.9.0 で足した）。
	// 拡張機能は、この選択への応答として、同じ番号を付けた editor/execute（EditorExecuteParams.Selection）を 1 回だけ送れる。
	// ViewInput では省く。
	Selection uint64 `json:"selection,omitempty"`
}

// InputChangeParams は、input/change の params である。
type InputChangeParams struct {
	View ViewID `json:"view"`
	// Text は、変わった後の入力の行の中身である。
	Text string `json:"text"`
	// Seq は、そのポップアップの入力の行が変わるたびに 1 から 1 ずつ増やす番号である（API 0.9.0 で足した）。
	// ViewFinder の拡張機能は、この入力への一覧を view/update の Seq に同じ値を入れて返す。
	// 本体は、前の入力への一覧が届いていなくても次の input/change を送るので、拡張機能は古い入力への計算を捨ててよい。
	Seq uint64 `json:"seq,omitempty"`
}

// ViewKeyParams は、view/key の params である（FR-69）。
type ViewKeyParams struct {
	View ViewID `json:"view"`
	// ID は、打ったキーの contributes.popupKeys の項目の ID である。
	ID string `json:"id"`
	// Item は、一覧、木、ViewFinder ではカーソルの行の項目の ID、入力の行では中身、テキストでは空である。
	Item string `json:"item,omitempty"`
	// Input は、ViewFinder の入力の行の中身である。他の部品では省く。
	Input string `json:"input,omitempty"`
}
