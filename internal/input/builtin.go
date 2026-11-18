package input

import (
	"fmt"

	"github.com/piplupflipper/modal-editor/internal/editcmd"
)

// spec は、本体の操作の表の 1 行である。
type spec struct {
	keys      string // ParseKeys の表記
	id        string
	takesChar bool
	// takesChar2 は、操作が 2 つ目の文字の引数を取るかである（Binding.TakesSecondChar）。
	takesChar2 bool
	next       editcmd.Mode // 実行の後のモード
	modes      MapMode      // 有効なモード。0 なら種類ごとの既定（defaultModes）
}

// 本体の操作の一覧。
// 一覧は issue #9 の「3.」の案（段階 1）で、裁定待ちである。
// 追加・削除はこの表の行の変更だけで済み、Parser には手を入れなくてよい。
// ID は本体の中の名前で、実行の側（internal/command）がこの ID で実装を引く。
//
// ~ は Vim の既定（tildeop が off）に合わせ、対象を取らない操作にした。大文字と小文字のオペレーターは g~、gu、gU である。
// マクロ（q、@）とドットリピート（.）は、Parser が記録中かどうかで読み方を変えるので、表ではなく Parser が持つ。
// 有効なモードは、modes を書かない行では種類ごとの既定（defaultModes）である。
// ビジュアルモードでは、オペレーター（d、c、y など）は選択を対象にする。ビジュアルモードだけの登録は modes に MapVisual を書く。
//
// req: FR-03
var (
	builtinMotions = []spec{
		{keys: "h", id: "left"},
		{keys: "j", id: "down"},
		{keys: "k", id: "up"},
		{keys: "l", id: "right"},
		{keys: "w", id: "word-next"},
		{keys: "b", id: "word-prev"},
		{keys: "e", id: "word-end"},
		{keys: "W", id: "bigword-next"},
		{keys: "B", id: "bigword-prev"},
		{keys: "E", id: "bigword-end"},
		{keys: "0", id: "line-start"},
		{keys: "^", id: "line-first-nonblank"},
		{keys: "$", id: "line-end"},
		{keys: "gg", id: "file-start"},
		{keys: "G", id: "file-end"},
		{keys: "f", id: "find-forward", takesChar: true},
		{keys: "F", id: "find-backward", takesChar: true},
		{keys: "t", id: "till-forward", takesChar: true},
		{keys: "T", id: "till-backward", takesChar: true},
		{keys: ";", id: "find-repeat"},
		{keys: ",", id: "find-repeat-reverse"},
		{keys: "%", id: "match-pair"},
		{keys: "{", id: "paragraph-prev"},
		{keys: "}", id: "paragraph-next"},
		// 検索。/ と ? は、実行の側（internal/command）がコマンドラインでパターンを受け取ってから移る。
		// n と N は直前の検索を繰り返し、* と # はカーソルの位置の単語を検索する。
		{keys: "/", id: "search-forward"},
		{keys: "?", id: "search-backward"},
		{keys: "n", id: "search-next"},
		{keys: "N", id: "search-prev"},
		{keys: "*", id: "search-word-forward"},
		{keys: "#", id: "search-word-backward"},
		// 矢印キーと Home、End は、Vim 9.1 の既定（'whichwrap' に < > が無い）と同じく h、j、k、l、0、$ と同じ働きにする。
		// 修飾キーの付いたもの（<S-Right> の w、<C-Home> の gg など）は登録しない。
		{keys: "<Left>", id: "left"},
		{keys: "<Down>", id: "down"},
		{keys: "<Up>", id: "up"},
		{keys: "<Right>", id: "right"},
		{keys: "<Home>", id: "line-start"},
		{keys: "<End>", id: "line-end"},
	}

	builtinOperators = []spec{
		{keys: "d", id: "delete"},
		{keys: "c", id: "change", next: editcmd.ModeInsert},
		{keys: "y", id: "yank"},
		{keys: "g~", id: "toggle-case"},
		{keys: "gu", id: "lowercase"},
		{keys: "gU", id: "uppercase"},
		// 囲みを足す（vim-sandwich の sa。FR-73、基本設計 4.8）。対象の後に囲みの文字を 1 つ取る。
		// 囲みのキーはノーマルモードだけに置く。ビジュアルモードの s は、選択を消して入力するオペレーターのまま残す。
		{keys: "sa", id: "surround-add", takesChar: true, modes: MapNormal},
		// > と < は、ビジュアルモードだけのオペレーターである。ノーマルモードの > と < は、モーションを待たずに
		// 行をずらす操作（操作の表の indent-line と dedent-line。FR-76、要件定義 1.1）にする。
		{keys: ">", id: "indent", modes: MapVisual},
		{keys: "<", id: "dedent", modes: MapVisual},
		// ビジュアルモードだけの別名。x は d、s は c に当たる。~、u、U は選択の大文字と小文字を変える。
		{keys: "x", id: "delete", modes: MapVisual},
		{keys: "s", id: "change", next: editcmd.ModeInsert, modes: MapVisual},
		{keys: "~", id: "toggle-case", modes: MapVisual},
		{keys: "u", id: "lowercase", modes: MapVisual},
		{keys: "U", id: "uppercase", modes: MapVisual},
		// ビジュアルモードの D、C、S、Y は、選択を行単位にしてから d、c、c、y を働かせる（Vim の v_D、v_C、v_S、v_Y）。
		// 文字単位の選択でも、選択した行の全体を対象にする。
		{keys: "D", id: "delete-lines", modes: MapVisual},
		{keys: "C", id: "change-lines", next: editcmd.ModeInsert, modes: MapVisual},
		{keys: "S", id: "change-lines", next: editcmd.ModeInsert, modes: MapVisual},
		{keys: "Y", id: "yank-lines", modes: MapVisual},
	}

	builtinTextObjects = []spec{
		{keys: "iw", id: "inner-word"},
		{keys: "aw", id: "a-word"},
		{keys: "iW", id: "inner-bigword"},
		{keys: "aW", id: "a-bigword"},
		{keys: `i"`, id: "inner-double-quote"},
		{keys: `a"`, id: "a-double-quote"},
		{keys: "i'", id: "inner-single-quote"},
		{keys: "a'", id: "a-single-quote"},
		// 括弧のテキストオブジェクトは、Vim と同じく閉じ括弧と b、B の別名を持つ。
		{keys: "i(", id: "inner-paren"},
		{keys: "i)", id: "inner-paren"},
		{keys: "ib", id: "inner-paren"},
		{keys: "a(", id: "a-paren"},
		{keys: "a)", id: "a-paren"},
		{keys: "ab", id: "a-paren"},
		{keys: "i[", id: "inner-bracket"},
		{keys: "i]", id: "inner-bracket"},
		{keys: "a[", id: "a-bracket"},
		{keys: "a]", id: "a-bracket"},
		{keys: "i{", id: "inner-brace"},
		{keys: "i}", id: "inner-brace"},
		{keys: "iB", id: "inner-brace"},
		{keys: "a{", id: "a-brace"},
		{keys: "a}", id: "a-brace"},
		{keys: "aB", id: "a-brace"},
		{keys: "ip", id: "inner-paragraph"},
		{keys: "ap", id: "a-paragraph"},
	}

	builtinActions = []spec{
		{keys: "~", id: "toggle-case"},
		{keys: "p", id: "put-after"},
		{keys: "P", id: "put-before"},
		{keys: "x", id: "delete-char"},
		{keys: "X", id: "delete-char-before"},
		{keys: "r", id: "replace-char", takesChar: true},
		// 省略形のコマンド。D は d$、C は c$、S は cc、Y は yy に当たり、実行の側（internal/command）が
		// オペレーターと対象の組に置き換えて実行する。オペレーターの表ではなく操作の表に置くのは、
		// 対象を待たずに 1 キーで完結するからである。ビジュアルモードの D、C、S、Y は、オペレーターの表のビジュアルモードだけの行である。
		// ノーマルモードの s（cl）は、囲みの操作（sa、sd、sr）に使うので割り当てない（要件定義 1.1）。
		{keys: "D", id: "delete-to-line-end"},
		{keys: "C", id: "change-to-line-end", next: editcmd.ModeInsert},
		{keys: "S", id: "substitute-line", next: editcmd.ModeInsert},
		{keys: "Y", id: "yank-line"},
		// ノーマルモードの > と < は、Vim の >> と << と同じく、回数の行数だけ行をずらす（FR-76）。次のキーを待たないので、
		// >> と打つと 2 回ずれる（#177 のコメントの決定）。
		{keys: ">", id: "indent-line"},
		{keys: "<", id: "dedent-line"},
		// 囲みを消す（sd{文字}）と替える（sr{前の文字}{後の文字}）。vim-sandwich の sd と sr に当たる（FR-74、FR-75、基本設計 4.8）。
		{keys: "sd", id: "surround-delete", takesChar: true},
		{keys: "sr", id: "surround-replace", takesChar: true, takesChar2: true},
		{keys: "J", id: "join-lines"},
		{keys: "o", id: "open-below", next: editcmd.ModeInsert},
		{keys: "O", id: "open-above", next: editcmd.ModeInsert},
		{keys: "i", id: "insert", next: editcmd.ModeInsert},
		{keys: "a", id: "append", next: editcmd.ModeInsert},
		{keys: "I", id: "insert-line-start", next: editcmd.ModeInsert},
		{keys: "A", id: "append-line-end", next: editcmd.ModeInsert},
		{keys: "u", id: "undo"},
		{keys: "<C-r>", id: "redo"},
		// Ex コマンド（:w など）の一覧と解釈はコマンドラインの担当で、ここでは切り替えだけを持つ。
		// ビジュアルモードの : は、選択した行の範囲（'<,'>）を入力した状態で始める。
		{keys: ":", id: "command-line", next: editcmd.ModeCommandLine, modes: MapNormal | MapVisual},
		// ビジュアルモードへの出入り。ビジュアルモードの中では、同じ種類なら抜け、違う種類なら切り替える。
		{keys: "v", id: "visual", modes: MapNormal | MapVisual},
		{keys: "V", id: "visual-line", modes: MapNormal | MapVisual},
		// ビジュアルモードの o は、カーソルを選択の反対の端へ移す。
		{keys: "o", id: "visual-swap", modes: MapVisual},
		// ページ単位のスクロール（Vim の <C-f> と <C-b>）。画面の表示領域を動かすので、モーションではなく操作にする。
		// Vim と同じく、オペレーターの後では使えない（d<PageDown> は誤りになる）。
		{keys: "<PageDown>", id: "page-down", modes: MapNormal | MapVisual},
		{keys: "<PageUp>", id: "page-up", modes: MapNormal | MapVisual},
		// 出力の欄（拡張機能の output/append の行）の操作（名前とキーは仮。基本設計 6.7）。欄にカーソルは入らない。
		// <C-w>P は、閉じていれば開き、開いていれば次の欄へ切り替える（Vim ではプレビューの窓へ移るキー）。
		// <C-w>z は閉じる（Vim でもプレビューの窓を閉じるキー）。<C-w><PageUp> と <C-w><PageDown> は、欄を半分の高さずつスクロールする。
		// スクロールのキーは、Vim 9.1 と Neovim 0.12.5 のヘルプの tags に CTRL-W_<PageUp> と CTRL-W_<PageDown> が無いことを確かめて選んだ。
		// <C-w>d は、Vim では :dsplit、Neovim では診断の浮動窓を開く既定のキーなので使わない（PR #133 のレビュー）。
		{keys: "<C-w>P", id: "output-next", modes: MapNormal},
		{keys: "<C-w>z", id: "output-close", modes: MapNormal},
		{keys: "<C-w><PageUp>", id: "output-scroll-up", modes: MapNormal},
		{keys: "<C-w><PageDown>", id: "output-scroll-down", modes: MapNormal},
		// ジャンプリスト（Vim の CTRL-O と CTRL-I）と、裏のバッファへの切り替え（CTRL-^）。
		// 端末は CTRL-I を Tab と同じ 0x09 で送り、internal/term は <Tab> に読むので、<Tab> も CTRL-I にする（Vim と同じ）。
		// <C-i> は、CTRL-I を区別して送る端末（CSI u）のための登録である。
		{keys: "<C-o>", id: "jump-older", modes: MapNormal},
		{keys: "<Tab>", id: "jump-newer", modes: MapNormal},
		{keys: "<C-i>", id: "jump-newer", modes: MapNormal},
		{keys: "<C-^>", id: "alternate-buffer", modes: MapNormal},
		// 貼り付けの直後に、貼ったテキストを履歴の古いほう（<C-p>）、新しいほう（<C-n>）の項目に置き換える（FR-55。キーは仮。基本設計 4.5）。
		// Vim のノーマルモードの CTRL-P と CTRL-N は k と j と同じ移動で、ここでは移動に割り当てていない。
		{keys: "<C-p>", id: "put-older", modes: MapNormal},
		{keys: "<C-n>", id: "put-newer", modes: MapNormal},
	}
)

// ProviderExtension は、提供者へ振り分ける本体の操作（基本設計 5.5。FR-94）の Binding.Ref.Extension に入れる値である（editcmd.ProviderExtension）。
//
// LSP と DAP の操作は、どの拡張機能が引き受けるかを、呼ばれた時点のバッファのファイルの種類で決める（exthost.Host.Provider）。
// そのためキーマップには拡張機能の ID を書けず、この値を入れておき、問い合わせる側（internal/editor）が提供者に置き換える。
// 実行の側（internal/command）からは拡張機能の操作に見えるので、問い合わせ（command.Extensions）を通る。
// ただしドットリピートは、対象を取らない操作（K、<leader>db など）を記録しない（command の repeatable。FR-06）。
const ProviderExtension = editcmd.ProviderExtension

// 提供者へ振り分ける操作の ID のうち、ext.Operation の名前でないもの。
const (
	// ProviderPeekPrefix は、移動の操作の「ポップアップで見る」を表す ID の接頭辞である（peek.definition）。
	// 後ろは ext.OpDefinition などの操作の名前で、提供者へは Args の peek を true にして問い合わせる。
	ProviderPeekPrefix = "peek."
	// 診断の一覧と移動は、本体が受け取った診断から作り、提供者へ問い合わせない（基本設計 5.5）。
	ProviderDiagnosticList    = "diagnostic.list"
	ProviderDiagnosticListAll = "diagnostic.listAll"
	ProviderDiagnosticNext    = "diagnostic.next"
	ProviderDiagnosticPrev    = "diagnostic.prev"
)

// providerSpec は、提供者へ振り分ける操作の既定のキーの表の 1 行である。
type providerSpec struct {
	keys string // editcmd.ParseKeysLeader の表記。<Leader> を含んでよい
	id   string // ext.Operation の名前か、上の定数
	kind BindingKind
	// modes は、有効なモードである。0 なら種類ごとの既定（defaultModes）である。
	modes MapMode
}

// providerKeys は、LSP と DAP の操作の既定のキーである（要件定義 4.11 の 2 つの表。FR-95、FR-96）。
// K、gq、<C-]> は Vim の標準の操作と重なるが、nvim が LSP の有効なバッファで同じキーを LSP の操作にしているのに合わせる（基本設計 6.3）。
// <leader>l? と <leader>d?（キーの一覧。FR-90）、補完は、まだ割り当てない。
//
// req: FR-95, FR-96
var providerKeys = []providerSpec{
	{keys: "<Leader>lh", id: "hover", kind: BindAction},
	{keys: "K", id: "hover", kind: BindAction},
	{keys: "<Leader>ld", id: ProviderPeekPrefix + "definition", kind: BindAction},
	{keys: "<Leader>lD", id: "definition", kind: BindAction},
	{keys: "<C-]>", id: "definition", kind: BindAction},
	{keys: "<Leader>li", id: ProviderPeekPrefix + "implementation", kind: BindAction},
	{keys: "<Leader>lI", id: "implementation", kind: BindAction},
	{keys: "<Leader>lt", id: ProviderPeekPrefix + "typeDefinition", kind: BindAction},
	{keys: "<Leader>lT", id: "typeDefinition", kind: BindAction},
	{keys: "<Leader>lr", id: "references", kind: BindAction},
	{keys: "<Leader>ln", id: "rename", kind: BindAction},
	{keys: "<Leader>la", id: "codeAction", kind: BindAction},
	{keys: "<Leader>ls", id: "documentSymbols", kind: BindAction},
	{keys: "<Leader>lS", id: "workspaceSymbols", kind: BindAction},
	{keys: "<Leader>le", id: ProviderDiagnosticList, kind: BindAction},
	{keys: "<Leader>lE", id: ProviderDiagnosticListAll, kind: BindAction},
	{keys: "<Leader>lj", id: ProviderDiagnosticNext, kind: BindAction},
	{keys: "<Leader>lk", id: ProviderDiagnosticPrev, kind: BindAction},
	// gq はオペレーターで、範囲を format の引数にする（基本設計 5.5）。ビジュアルモードでは選択を範囲にする。
	{keys: "gq", id: "format", kind: BindOperator},

	{keys: "<Leader>dc", id: "debug.continue", kind: BindAction},
	{keys: "<Leader>dp", id: "debug.pause", kind: BindAction},
	{keys: "<Leader>dt", id: "debug.terminate", kind: BindAction},
	{keys: "<Leader>dr", id: "debug.restart", kind: BindAction},
	{keys: "<Leader>db", id: "breakpoint.toggle", kind: BindAction},
	{keys: "<Leader>dB", id: "breakpoint.conditional", kind: BindAction},
	{keys: "<Leader>dX", id: "breakpoint.clear", kind: BindAction},
	{keys: "<Leader>dn", id: "debug.stepOver", kind: BindAction},
	{keys: "<Leader>di", id: "debug.stepIn", kind: BindAction},
	{keys: "<Leader>do", id: "debug.stepOut", kind: BindAction},
	{keys: "<Leader>dl", id: "debug.runLast", kind: BindAction},
	{keys: "<Leader>du", id: "debug.toggleUI", kind: BindAction},
}

// ProviderOperations は、既定のキーで呼べる、提供者へ振り分ける操作の ID の一覧を、表の順に重なりなく返す。
// ProviderPeekPrefix の付いたものと診断のものを含む。
func ProviderOperations() []string {
	var ids []string
	seen := map[string]bool{}
	for _, s := range providerKeys {
		if !seen[s.id] {
			seen[s.id] = true
			ids = append(ids, s.id)
		}
	}
	return ids
}

// DefaultKeymap は、本体の操作を登録したキーマップを返す。<Leader> は既定の leader（editcmd.DefaultLeader）にする。
// 呼ぶたびに新しい値を返すので、拡張機能の操作を足しても他の呼び出し元に影響しない。
//
// req: FR-03
func DefaultKeymap() *Keymap {
	m, _ := DefaultKeymapLeader(nil)
	return m
}

// DefaultKeymapLeader は、DefaultKeymap と同じだが、LSP と DAP の操作の既定のキー（providerKeys）の <Leader> を leader にする。
// leader が nil なら既定の leader を使う。
// 利用者の leader によって本体の操作と同じキーの列になった既定のキーは登録せず、errs に返す（本体の操作を優先する）。
//
// req: FR-03, FR-95, FR-96, FR-97
func DefaultKeymapLeader(leader []editcmd.Key) (m *Keymap, errs []error) {
	m = builtinKeymap()
	for _, s := range providerKeys {
		keys, err := editcmd.ParseKeysLeader(s.keys, leader)
		if err == nil {
			err = m.Bind(Binding{Kind: s.kind, Modes: s.modes, Keys: keys, Ref: editcmd.Ref{ID: s.id, Extension: ProviderExtension}})
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("本体の操作 %s の既定のキー %s を登録できない: %w", s.id, s.keys, err))
		}
	}
	return m, errs
}

// builtinKeymap は、本体の操作の表（builtinMotions など）だけを登録したキーマップを返す。
func builtinKeymap() *Keymap {
	m := NewKeymap()
	tables := []struct {
		kind  BindingKind
		specs []spec
	}{
		{BindMotion, builtinMotions},
		{BindOperator, builtinOperators},
		{BindTextObject, builtinTextObjects},
		{BindAction, builtinActions},
	}
	for _, t := range tables {
		for _, s := range t.specs {
			err := m.Bind(Binding{
				Kind:            t.kind,
				Modes:           s.modes,
				Keys:            editcmd.MustParseKeys(s.keys),
				Ref:             editcmd.Ref{ID: s.id},
				TakesChar:       s.takesChar,
				TakesSecondChar: s.takesChar2,
				NextMode:        s.next,
			})
			if err != nil {
				// 表の誤りはプログラムの誤りで、テストで検出する。
				panic(fmt.Sprintf("本体の操作の表の誤り: %v", err))
			}
		}
	}
	return m
}
