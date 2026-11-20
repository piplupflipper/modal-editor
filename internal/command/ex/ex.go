// Package ex は、コマンドラインモードで確定した Ex コマンドの文字列を解析し、実行する（FR-08、FR-09）。
//
// 基本設計 2 章の internal/command の下にあり、土台の層の internal/buffer と internal/option だけを import する。
// バッファを編集するコマンド（:s、:g、:v、:d）は、バッファへ直接トランザクションを適用する。
// 保存、終了、ファイルを開くといったエディタの状態に関わるコマンド（:w、:q、:wq、:x、:e）は、
// Action の値として呼び出し側へ返し、このパッケージではファイルを読み書きしない。
//
// # 対応するコマンド
//
// コマンドは commands の表に登録する。対象の一覧（issue #9 の案）が裁定で増減しても、表の行を足し引きすればよい。
//
//	:[range]w[rite][!] [file]  Action の ActionWrite を返す
//	:[N]q[uit][!]              Action の ActionQuit を返す。! が無く、未保存の変更があれば E37 で失敗する
//	:[range]wq[!] [file]       ActionWrite と ActionQuit を返す
//	:[range]x[it][!] [file]    変更があるか file を指定したときだけ ActionWrite を返し、続けて ActionQuit を返す
//	:e[dit][!] [file]    Action の ActionEdit を返す。file が無く（読み直し）、! が無く、未保存の変更があれば E37 で失敗する。
//	                     file を開くのは新しいタブなので（FR-113）、file があれば未保存の変更を確かめない
//	:tabm[ove] [N|+N|-N|$]  カーソルのあるタブを動かす ActionTabMove を返す（FR-65）
//	:popu[p]             カーソルのあるタブをポップアップへ縮める ActionPopup（PopupShrink）を返す（FR-70。名前は仮）
//	:tabop[en]           ポップアップをタブへ広げる ActionPopup（PopupExpand）を返す（FR-70。名前は仮。:tabo は Vim の :tabonly なので 5 文字）
//	:diffs[plit] {file}  file を別のペインの client で開いて見比べる ActionDiff（DiffSplit）を返す（FR-99。名前は仮）
//	:difft[his]          ActionDiff（DiffThis）を返す
//	:diffo[ff][!]        ActionDiff（DiffOff）を返す
//	:diffu[pdate][!]     ActionDiff（DiffUpdate）を返す
//	:[range]s[ubstitute]/pat/rep/[flags] [count]
//	:[range]g[lobal][!]/pat/cmd
//	:[range]v[global]/pat/cmd
//	:[range]d[elete] [x] [count]
//	:[range]             範囲の最後の行へ移る（:3 など）。行数を超えた行番号は最後の行に丸める
//	:<コマンドの ID> [引数]  拡張機能のコマンドを実行する Action の ActionExtCommand を返す（State.ExtCommand。例: :acp.prompt 説明して）
//	:out[put] [名前]     出力の欄を開く（名前の欄に切り替える）Action の ActionOutput を返す。Vim に無い本体のコマンド（名前は仮）
//	:outc[lose]          出力の欄を閉じる Action の ActionOutput を返す。Vim に無い本体のコマンド（名前は仮）
//	:ls[!]、:buffers[!]   開いているバッファの一覧を出す Action の ActionBuffer（BufferList）を返す
//	:b[uffer][!] [N|名前]  そのバッファへ移る ActionBuffer（BufferGo）を返す。未保存の変更の確認は呼び出し側が行う
//	:bn[ext][!] [N]      N 個先のバッファへ移る ActionBuffer（BufferNext）を返す
//	:bp[revious][!] [N]  N 個前のバッファへ移る ActionBuffer（BufferPrev）を返す。:bN[ext] も同じ
//	:ju[mps]             ジャンプリストを出す Action の ActionJumpList を返す
//	:reg[isters]         レジスターと履歴の一覧を出す Action の ActionRegisterList を返す（名前は仮）
//	:wa[ll][!]           未保存のバッファをすべて書き出す ActionWriteAll を返す
//	:qa[ll][!]           エディタを閉じる ActionQuitAll を返す。未保存のバッファの確認は呼び出し側が行う
//	:wqa[ll][!]、:xa[ll][!]  ActionWriteAll と ActionQuitAll を返す
//	:bd[elete][!] [N|名前]  バッファを閉じる ActionBufferDelete を返す。未保存の変更の確認は呼び出し側が行う
//	:{range}!{cmd}       範囲の行を cmd の標準入力へ渡し、出力で範囲を置き換える（FR-81。Result.Shell を返す）
//	:!{cmd}              端末を子プロセスへ渡して cmd を実行する（FR-80。Result.Shell の Terminal を true にして返す）
//	:[N]r[ead] !{cmd}    cmd の出力を N 行目（省くとカーソルの行）の後に入れる。:0r は 1 行目の前に入れる（FR-81。:r! も同じ）
//	:reado[nly]          バッファの編集の禁止を切り替え、切り替えた後の状態を Notice で知らせる（FR-86。Vim に無い本体のコマンド。名前は仮）
//	:option.set <名前> <値>  設定の項目の値を変える（State.Options。FR-132）。名前は省略できない
//	:option.get <名前>   設定の項目の今の値を "<名前>=<値>" の形で Notice に出す（FR-132）
//
// # 別名
//
// Ex コマンドの別名は、commands とは別の表（aliases）に置き、コマンドラインを正規のコマンドの呼び出しに変換する（FR-133）。
// 名前は commands の表を先に引き、無ければ別名の表を引く。別名の表の行は、名前を付け替えるだけの項目か、変換を持つ項目である。
//
//	:se[t] {引数} …     Vim の :set の書き方の一部を、:option.set と :option.get の呼び出しに変換する（expandSet）
//
// 編集を禁止したバッファ（buffer.Buffer.ReadOnly）では、テキストを変えるコマンド（:s、:d、:r、:{range}!）を、
// 範囲と引数を確かめる前に E21 で断る（FR-85。Vim 9.1 で 'modifiable' を切ったときと同じ）。:g と :v は断らず、cmd の中のコマンドが断られる。
//
// :{range}! と :r ! は、"|" を含めて行末までを cmd としてシェルへ渡す（Vim と同じ）。
// このパッケージはプロセスを起動しない。実行を Result.Shell で呼び出し側へ求め、呼び出し側が終わった後に
// ShellRequest.Finish で出力をバッファへ入れる（イベントループを止めずに実行するため。基本設計 4.9）。
// 置き換えと挿入は Finish の 1 回でアンドゥの 1 単位になるが、同じ行の前のコマンド（:s/a/b/|2,3!sort の :s）とは別の単位になる。
// 実行の間に続きを止められないので、:g の cmd の中では使えず E319 にする（Vim では使える）。
// Vim と違い、cmd の中の "!"（直前のコマンド）、"%"、"#"（ファイル名）は展開しない。
// 範囲の無い :!{cmd}（端末を渡す実行。FR-80）も同じく Result.Shell で求め、バッファは変えない。編集を禁止したバッファでも使える。
// ファイルを読み込む :r {file} はまだ無く、E319 にする。
//
// :b、:bn、:bp の番号と回数は引数でだけ書け、Vim の範囲で書く形（:2b、:3bn）は E481 にする。
// :b、:bn、:bp、:e は、移った先のバッファでは "|" の後のコマンドを続けない。後のコマンドは移る前のバッファで実行する
// （動作は呼び出し側が Execute の後に行うため）。
//
// 拡張機能のコマンドは、ID と完全に一致する名前だけで呼べ、本体のコマンドの名前か省略形と同じ ID は本体のコマンドを優先する。
// 引数は行末までの文字列で、"|" で区切らない。範囲と "!" は取らず、:w などの動作を返すコマンドの後には置けない（E319）。
//
// :w、:wq、:x の範囲は、バッファ全体（:%w、:1,$w）だけを受け付け、一部の行（:2,3w）は E319 にする。
// Action は書き出す行の範囲を持たないので、一部の行の書き出しには対応していない。
// :q の範囲は、Vim と同じくウィンドウの番号を数える。ウィンドウは 1 つなので、:0q、:1q、:$q、:.q は :q と同じで、:2q は E16 にする。
//
// コマンドは "|" で区切って続けられる（:w|q、:e foo|q）。
// ファイル名や回数を取るコマンドでは、"\" の付かない最初の "|" で引数が終わり、"\|" は "|" そのものになる。
// :s は、パターンと置換の文字列の中の "|" を区切りとせず、フラグと回数の後の "|" で終わる。
// :g と :v は、Vim と同じく "|" を含めて行末までを cmd として受け取り、cmd の中の "|" は印の付いた行ごとに区切る。
//
// # 範囲
//
// アドレスは、行番号、"."（現在の行）、"$"（最後の行）、"/pat/"（次の行から前方へ探す）、"?pat?"（前の行から後方へ探す）と、
// その後に続く "+n"、"-n" で書く。"%" は "1,$" と同じである。
// 行数を超えた行番号は、コマンド名の無い範囲（:10）では最後の行に丸め、コマンドに付けた範囲（:10d、:1,10s/a/b/、:5w、:4q）と
// "|" が続く範囲（:10|）では E16 にする。負の行番号はどれでも E16 にする。Vim 9.1 の Ex モードでない動作に合わせている。
//
// 逆順の範囲（:4,2d）は、コマンドに付けたときは E493 で失敗し、Result.Confirm で「入れ替えてよいか」の確認を求める。
// 呼び出し側が確認を得たら Confirm.Resume で入れ替えて続きを実行し、断られたら何もしない（Vim 9.1 の対話の動作と同じ）。
// 確認を求めない呼び出し側は、E493 をそのまま誤りとして示せばよい（Vim 9.1 が -c やスクリプトで実行したときと同じ）。
// :g の cmd の中の逆順の範囲は、確認を求めずに E16 にする（Vim 9.1 と同じ）。
// コマンド名の無い逆順の範囲は、"|" が続かなければ後の行へ移り（:4,2 は 2 行目）、"|" が続けば E16 にする（Vim 9.1 と同じ）。
//
// アドレスは "," か ";" で区切り、";" は次のアドレスを読む前に現在の行を前のアドレスへ移す。
// ";" で移したカーソルは、その後で誤りになっても残す（Vim 9.1 と同じ。:2;9d は E16 で失敗し、カーソルは 2 行目に移る）。
// 検索は Vim の既定（wrapscan）と同じく、バッファの端で反対の端へ折り返す。
// マーク（"'a"、"'<,'>" など）は State.Mark で行に解決する。
//
// # 正規表現
//
// パターンは Go の regexp（RE2）の文法で解釈する（FR-09）。Vim の正規表現の文法（\v、\< など）には変換しない。
// 利用者は Vim の書き方ではなく RE2 の書き方で書く。
// 例えば "\+" は Vim の「1 回以上」ではなくリテラルの "+"、"\(" はグループではなくリテラルの "("、"\|" は選択ではなくリテラルの "|" である。
// 1 回以上は "+"、グループは "(...)"、選択は "|"、単語の境界は "\b" と書く。
// パターンの中の "\" と区切り文字の組は、区切り文字そのものとして扱う。
// 照合は行ごとに行い、パターンが行をまたいで一致することは無い。
// パターンを空にすると、State.LastPattern（直前に使ったパターン）を使う（:g/pat/s//rep/ など）。
//
// # 誤り
//
// 解析と実行の誤りは *Error で返す。Code は Vim のエラー番号、Pos はコマンドラインの中のバイト位置である。
package ex

import (
	"fmt"
	"slices"
	"strings"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
)

// ActionKind は、呼び出し側が行うエディタの動作の種類を表す。
type ActionKind int

// 動作の種類。
const (
	// ActionWrite は、バッファの内容をファイルへ書き出す動作である。
	// 呼び出し側が buffer.WriteFile で書き、編集中のファイルへ書いたときは MarkSaved を呼ぶ。
	ActionWrite ActionKind = iota + 1
	// ActionQuit は、エディタ（またはウィンドウ）を閉じる動作である。
	ActionQuit
	// ActionEdit は、ファイルを開く動作である。File が空なら、編集中のファイルを読み直す。
	ActionEdit
	// ActionExtCommand は、拡張機能のコマンド（宣言ファイルの contributes.commands）を実行する動作である。
	// Extension と Command にコマンドを、Text に引数の文字列を入れる。
	// internal/command の State は、この動作を呼び出し側へ返さずに、自分で拡張機能へ問い合わせる（基本設計 5.4 の期限の扱いを通すため）。
	ActionExtCommand
	// ActionOutput は、出力の欄（拡張機能の output/append の行。基本設計 6.7）を開く、閉じる、切り替える、スクロールする動作である。
	// Output に操作を、Text に欄の名前（OutputOpen のときだけ。空なら最後に書き足された欄）を入れる。
	// バッファを読み書きしないので、後に拡張機能のコマンドを置ける（:output|acp.prompt ...）。
	ActionOutput
	// ActionCheckTime は、開いているバッファのファイルが外で変わっていないかを確かめる動作（:checktime）である（基本設計 6.7）。
	// バッファを読み書きしないので、範囲も引数も持たない。
	ActionCheckTime
	// ActionBuffer は、開いているバッファの一覧を出す、またはカーソルのあるバッファを切り替える動作である（:ls、:b、:bn、:bp、<C-^>）。
	// Buffer に操作を、Count に回数かバッファの番号を、Text にバッファの名前を、Force に "!" を入れる。
	// 番号と名前からバッファを探すのは、バッファの一覧を持つ呼び出し側である。
	ActionBuffer
	// ActionJump は、ジャンプリストを Count だけ進める（負なら戻る）動作である（<C-o>、<C-i>）。
	// キーの操作だけが使い、Ex コマンドからは返さない。
	ActionJump
	// ActionJumpList は、ジャンプリストを出す動作である（:jumps）。
	ActionJumpList
	// ActionWriteAll は、未保存の変更のあるバッファをすべて書き出す動作である（:wa、:wqa、:xa）。
	ActionWriteAll
	// ActionQuitAll は、エディタを閉じる動作である（:qa、:wqa、:xa）。Force が false なら、
	// 呼び出し側は未保存の変更のあるバッファがあれば終わらずに、そのバッファへ移って知らせる。
	ActionQuitAll
	// ActionBufferDelete は、バッファを閉じる動作である（:bd）。Count にバッファの番号（0 ならカーソルのあるバッファ）を、
	// Text に名前を、Force に "!" を入れる。
	ActionBufferDelete
	// ActionRegisterList は、レジスターと履歴の一覧を浮かべる窓に出す動作である（:registers。FR-54）。
	ActionRegisterList
	// ActionTabMove は、カーソルのあるタブを、client の中のタブの並びの中で動かす動作である（:tabmove。FR-65）。
	// Relative が false なら、Count のタブの後ろへ動かす（0 なら先頭、-1 なら末尾。Vim 9.1 の :tabmove N と :tabmove）。
	// Relative が true なら、Count だけ右（負なら左）へ動かす（:tabmove +N と :tabmove -N）。
	// Text は、誤りの文言に出す引数である。並びの外を指すかは、タブを持つ呼び出し側が確かめる。
	ActionTabMove
	// ActionDiff は、2 つの client のタブの見比べ（基本設計 6.8。FR-99〜FR-101）の操作である。
	// Diff に操作を、File に :diffsplit のファイルを、Count に ]c と [c の回数を入れる。
	// 見比べの相手のバッファと差分を持つのは呼び出し側である。
	ActionDiff
	// ActionPopup は、ポップアップ（基本設計 1.4。FR-68、FR-70）の操作である。Popup に操作を入れる。
	// ポップアップを持つのは呼び出し側である。
	ActionPopup
)

// PopupOp は、ActionPopup の操作である。
type PopupOp int

// ポップアップの操作。
const (
	// PopupShrink は、カーソルのあるタブをポップアップへ縮める（:popup。名前は仮。FR-70）。
	PopupShrink PopupOp = iota
	// PopupExpand は、フォーカスのあるポップアップのバッファをタブへ広げる（:tabopen。名前は仮。FR-70、FR-110）。
	PopupExpand
	// PopupFocus は、カーソルの近くのポップアップへフォーカスを入れる。入っていれば元のタブへ戻す（<C-w>w。FR-68）。
	// キーの操作だけが使う。
	PopupFocus
)

// DiffOp は、ActionDiff の操作である。
type DiffOp int

// 見比べの操作。
const (
	// DiffSplit は、File を別のペインの新しい編集の client のタブで開き、カーソルのあるタブと見比べる（:diffsplit。仮）。
	DiffSplit DiffOp = iota
	// DiffThis は、カーソルのあるタブを見比べの対象にする。別の client のタブでも打てば、その 2 つを見比べる（:diffthis）。
	DiffThis
	// DiffOff は、カーソルのあるタブの見比べを終える（:diffoff）。
	DiffOff
	// DiffUpdate は、差分を計算し直す（:diffupdate）。
	DiffUpdate
	// DiffNext と DiffPrev は、Count 個先と前の差分の始まりへ移る（]c と [c）。キーの操作だけが使う。
	DiffNext
	DiffPrev
	// DiffObtain は、カーソルの差分を相手の内容で置き換える（do）。キーの操作だけが使う。
	DiffObtain
	// DiffPut は、相手の差分をカーソルの差分の内容で置き換える（dp）。キーの操作だけが使う。
	DiffPut
)

// BufferOp は、ActionBuffer の操作である。
type BufferOp int

// バッファの操作。
const (
	// BufferList は、開いているバッファの一覧を出す（:ls、:buffers）。
	BufferList BufferOp = iota
	// BufferGo は、Count の番号か Text の名前のバッファへ移る（:b）。どちらも無ければ移らない。
	BufferGo
	// BufferNext と BufferPrev は、番号の順に Count 個先か前のバッファへ移る。端では反対の端へ回る（:bn、:bp、:bN）。
	BufferNext
	BufferPrev
	// BufferAlternate は、裏のバッファ（直前にカーソルのあったバッファ）へ移る（<C-^>）。
	// Count が 0 より大きければ、その番号のバッファへ移る（N<C-^>）。
	BufferAlternate
)

// OutputOp は、ActionOutput の操作である。
type OutputOp int

// 出力の欄の操作。
const (
	// OutputOpen は、欄を開く。開いていれば、Text の欄に切り替える。
	OutputOpen OutputOp = iota
	// OutputClose は、欄を閉じる。
	OutputClose
	// OutputNext は、閉じていれば開き、開いていれば次の欄に切り替える。
	OutputNext
	// OutputScrollUp と OutputScrollDown は、欄を半分の高さだけ上と下へスクロールする。
	OutputScrollUp
	OutputScrollDown
)

// Action は、Ex コマンドが呼び出し側へ求めるエディタの動作である。
type Action struct {
	Kind ActionKind
	// File は、コマンドの引数に書かれたファイル名である。空なら編集中のファイルを指す。
	// 先頭と末尾の空白を除き、"\ " を空白に、"\|" を "|" に戻す以外の展開（%、~、ワイルドカード）はしない。
	File string
	// Force は、コマンドに "!" が付いていたかどうかである。
	// ActionWrite では既存のファイルや読み取り専用のファイルへの上書き、ActionQuit と ActionEdit では未保存の変更の破棄を表す。
	Force bool
	// Extension と Command は、ActionExtCommand で、コマンドを宣言した拡張機能の ID と、コマンドの ID である。
	Extension string
	Command   string
	// Text は、ActionExtCommand で、コマンドの ID の後の空白を除いた、行末までの引数の文字列である。
	// "|" と '"' も引数に含める（Vim の -bar を付けない利用者定義のコマンドと同じ）。引数が無ければ空である。
	// ActionOutput では、開く欄の名前である。ActionBuffer では、:b の引数のバッファの名前である。
	Text string
	// Output は、ActionOutput の操作である。
	Output OutputOp
	// Buffer は、ActionBuffer の操作である。
	Buffer BufferOp
	// Count は、ActionBuffer の回数かバッファの番号（BufferOp の doc）と、ActionJump の進める数と、ActionTabMove の位置である。
	Count int
	// Relative は、ActionTabMove の Count が今の位置からの数であることを表す。
	Relative bool
	// ClientOnly は、ActionWriteAll で、書き出すバッファを、コマンドを打った client のタブに限ることを表す（:wqa、:xa。基本設計 4.4）。
	// false なら server のすべてのバッファを書き出す（:wa）。
	ClientOnly bool
	// Diff は、ActionDiff の操作である。
	Diff DiffOp
	// Popup は、ActionPopup の操作である。
	Popup PopupOp
}

// Deletion は、:d が消した行の記録である。
// レジスターは internal/command が持つので、呼び出し側がこの記録をレジスターへ入れる。
type Deletion struct {
	// Register は、:d の引数に書かれたレジスターの名前である。省略したときは 0 である。
	// 書き込めるレジスターの名前（英字、"、-、_、*、+）であることは、このパッケージが検査する。
	Register rune
	// Lines は、消した行を改行を含まずに並べたものである。行単位のテキストとして扱う。
	Lines []string
}

// CursorMove は、実行した後にカーソルをどう置くかを表す。
type CursorMove int

// カーソルの置き方。列は buffer の行の内容から呼び出し側が決める。
const (
	// CursorUnchanged は、カーソルを動かさないことを表す（:w、:q、一致の無い :s/x/y/e など）。
	CursorUnchanged CursorMove = iota
	// CursorFirstNonBlank は、Result.Cursor の行の、最初の空白でない文字へ置くことを表す（:s、:d、:<行番号>）。
	CursorFirstNonBlank
	// CursorLineStart は、Result.Cursor の行の先頭（0 バイト目）へ置くことを表す。
	// :g が最後の印の行へ移り、cmd がカーソルを動かさなかったとき（:g/pat/s/x/y/ で x が無い）に当たる。
	CursorLineStart
	// CursorKeepColumn は、Result.Cursor の行へ移し、列は Execute を呼ぶ前のバイトの位置を保つ（その行に収まるように丸める）ことを表す。
	// 範囲の ";" が行を移し、その後のコマンドがカーソルを置かなかったとき（:2;9d の E16、:2;4s/x/y/e で一致が無い）に当たる。
	// Vim 9.1 は ";" で行だけを移し、列はそのまま残す。
	CursorKeepColumn
)

// State は、Execute の呼び出しをまたいで保つ状態である。呼び出し側（internal/editor）が 1 つ持ち、毎回同じものを渡す。
// Execute は、この中身を Vim と同じ時機に更新する。
type State struct {
	// LastPattern は、直前に使った検索のパターン（Go の regexp の文法）である。
	// :s、:g、"/pat/" のアドレスで使ったものを記録し、空のパターン（:s//x/）はこれを使う。
	// ノーマルモードの / と n と共有する前提で、呼び出し側が書き換えてもよい。
	// コンパイルに失敗したパターンは記録しない。
	LastPattern string
	// LastSubstitute は、直前の :s のパターン、置換の文字列、フラグである。:s を一度も実行していなければ nil である。
	// 置換の文字列の ~ はこの Replacement に置き換わる。& と :&& は未実装だが、この値を使う想定である。
	LastSubstitute *Substitution
	// Mark は、マークの名前（'a の a、'< の <）を行（0 始まり）に解決する。
	// nil か、ok が false を返したときは、そのマークのアドレスを E20 にする。
	Mark func(name rune) (line int, ok bool)
	// ExtCommand は、コマンドの ID が拡張機能のコマンド（宣言ファイルの contributes.commands）であれば、
	// それを宣言した拡張機能の ID と true を返す。nil なら、拡張機能のコマンドは呼べない（E492 にする）。
	ExtCommand func(id string) (extension string, ok bool)
	// Options は、設定の項目の値を読み書きする口である（:option.set、:option.get と、その別名の :set。FR-132）。
	// nil なら、これらのコマンドは E319 にする。
	Options Options
}

// Notice は、Ex コマンドが利用者に知らせる 1 行の通知である（"3 行をフィルターしました" など）。
type Notice struct {
	// Text は通知の文言である。空なら通知は無い。
	Text string
	// Warning は、警告として出すことを表す（シェルの終了コードが 0 でないとき）。
	Warning bool
}

// Substitution は、:s の 1 回分のパターン、置換の文字列、フラグである。
type Substitution struct {
	Pattern string
	// Replacement は、~ を直前の置換の文字列に展開した後の、Vim の書き方の置換の文字列である。
	Replacement string
	Global      bool // g フラグ
	IgnoreCase  bool // i フラグ（I で打ち消す）
}

// Result は、Execute の結果である。
// Execute が誤りを返した場合も、誤りまでに行った変更と動作を保持する。
type Result struct {
	// Cursor は、実行した後のカーソルの行（0 始まり）である。Move が CursorUnchanged なら、渡した行（丸めたもの）のままである。
	Cursor int
	// Move は、カーソルを動かしたかと、列の置き方である。Vim 9.1 の既定（'startofline' がオン）に合わせる。
	Move CursorMove
	// Actions は、呼び出し側が先頭から順に行う動作である。
	// 動作が失敗したら（:wq の ActionWrite の書き出しの失敗など）、残りの動作は行わずに捨て、誤りを利用者に示す。
	// 例えば :wq で書き出しに失敗したら終了しない。
	Actions []Action
	// Changes は、バッファに適用した変更を適用した順に並べたものである。
	// 拡張機能へ差分を送る（FR-16）ときに使う。
	Changes []buffer.Change
	// Deletions は、:d が消した行を、消した順に並べたものである。
	Deletions []Deletion
	// Confirm は、続きを実行する前に利用者の確認が要るときに nil でない。このとき Execute は E493 の *Error も返す。
	// 確認が得られたら Confirm.Resume で続きを実行する。
	Confirm *Confirm
	// Jumped は、ジャンプに当たるコマンド（置き換えた :s、印を付けた行のある :g と :v、:d）を実行したかである。
	// 呼び出し側は、Execute を呼ぶ前のカーソルの位置をジャンプリストに記録する（Vim 9.1 の setpcmark と同じ）。
	// :g の cmd の中のコマンドは、:g の 1 回のほかには記録しない（Vim の global_busy の間と同じ）。
	Jumped bool
	// Notice は、最後に通知を出したコマンドの通知である（:{range}! と :r ! の行の数と、シェルの終了コード）。
	Notice Notice
	// Shell は、外部のコマンドの実行を求めるときに nil でない（:{range}! と :r !）。行の最後のコマンドなので、後に続くコマンドは無い。
	// 呼び出し側はコマンドを実行し、終わったら Shell.Finish で出力をバッファへ入れる。
	Shell *ShellRequest
}

// Confirm は、Ex コマンドの続きを実行する前に、利用者に確認を求めることを表す。
// 今は、逆順の範囲（:4,2d）を入れ替えてよいかの確認だけである（Vim 9.1 の "Backwards range given, OK to swap (y/n)?"）。
type Confirm struct {
	// Prompt は、利用者に示す問いである。
	Prompt string
	// cursor は、確認を求めたコマンドを解析し始めたときのカーソルの行（0 始まり）である。
	// 範囲の "." はこの行で評価するので、";" で移した後の行ではなく、この行から解析し直す。
	cursor int
	// src と base は、確認を求めたコマンドから後のコマンドラインと、その先頭のコマンドラインの中の位置である。
	src  string
	base int
}

// Resume は、確認が得られたときに、確認を求めたコマンドの逆順の範囲を入れ替えて、そのコマンドから後を実行する。
// 確認を求めた Execute の後、バッファと st を変えずに呼ぶ。返す値は Execute と同じで、続くコマンドがまた確認を求めることもある。
// Resume で行った編集は、確認を求めた Execute とは別のアンドゥの単位になる。
//
// req: FR-08
func (cf *Confirm) Resume(b *buffer.Buffer, st *State) (Result, error) {
	if st == nil {
		st = &State{}
	}
	c := &execCtx{b: b, st: st, cursor: clampLine(b, cf.cursor), swapBackwards: true}
	b.BeginUndoGroup()
	defer b.EndUndoGroup()
	err := c.runLine(cf.src, cf.base)
	return c.res(), err
}

// Execute は、コマンドラインの文字列 cmdline を Ex コマンドとして解析し、バッファ b に対して実行する。
// cursor は実行する前のカーソルの行（0 始まり）で、バッファの外を指していれば最も近い行に丸める。
// cmdline の先頭の ":" と空白は読み飛ばし、"|" で区切った複数のコマンドを順に実行する（:w|q）。
// st は呼び出しをまたいで保つ状態で、Execute が更新する。nil なら、この呼び出しの中だけの状態を使う。
//
// 1 回の Execute で行った編集は、アンドゥの 1 単位にまとめる（:g で多数の行を消しても u 1 回で戻る）。
// 誤りは *Error で返す。誤りを返した場合も、それまでに行った編集はバッファに残り、Result に記録される。
// "|" で区切ったコマンドの途中で誤りがあれば、残りのコマンドは実行しない。
//
// req: FR-08
func Execute(b *buffer.Buffer, st *State, cursor int, cmdline string) (Result, error) {
	if st == nil {
		st = &State{}
	}
	c := &execCtx{b: b, st: st, cursor: clampLine(b, cursor)}
	b.BeginUndoGroup()
	defer b.EndUndoGroup()
	err := c.runLine(cmdline, 0)
	return c.res(), err
}

// execCtx は、1 回の Execute の間の状態である。
type execCtx struct {
	b      *buffer.Buffer
	st     *State
	cursor int // 0 始まりの行
	move   CursorMove
	// inGlobal は、:g の中で実行しているかどうかである。:g の入れ子を禁じるのに使う。
	inGlobal  bool
	actions   []Action
	changes   []buffer.Change
	deletions []Deletion
	// wroteVersion は、編集中のファイルへの ActionWrite を返した時点の版である。返していなければ nil である。
	wroteVersion *uint64
	// replaced は、行の置き換えの記録である。:g が、印を付けた行の番号を編集に合わせて動かすのに使う。
	replaced []lineReplace
	// swapBackwards は、次に解析するコマンドの逆順の範囲を、確認を求めずに入れ替えるかどうかである（Confirm.Resume）。
	// 1 つのコマンドを解析したら false に戻す。
	swapBackwards bool
	// confirm は、確認を求めて止めたときの続きである。
	confirm *Confirm
	// jumped は、ジャンプに当たるコマンドを実行したかである（Result.Jumped）。
	jumped bool
	// notice は、最後に通知を出したコマンドの通知である（Result.Notice）。
	notice Notice
	// optionText は、:option.get が最後に出した通知の文言である。続けて読んだ値を同じ通知に足すかを決めるのに使う（runOptionGet）。
	optionText string
	// shell は、外部のコマンドの実行を求めたときの求めである（Result.Shell）。
	shell *ShellRequest
}

// markJump は、ジャンプに当たるコマンドを実行したことを記録する。:g の cmd の中では記録しない。
func (c *execCtx) markJump() {
	if !c.inGlobal {
		c.jumped = true
	}
}

// lineReplace は、start 行目（0 始まり）から old 行を、new 行で置き換えたことを表す。
type lineReplace struct {
	start, old, new int
}

func (c *execCtx) res() Result {
	return Result{Cursor: c.cursor, Move: c.move, Actions: c.actions, Changes: c.changes, Deletions: c.deletions, Confirm: c.confirm, Jumped: c.jumped, Notice: c.notice, Shell: c.shell}
}

// setCursor は、カーソルを line 行目（0 始まり）へ移し、列の置き方を記録する。
func (c *execCtx) setCursor(line int, mv CursorMove) {
	c.cursor = line
	c.move = mv
}

// apply は、edits を 1 つのトランザクションとして現在の版に適用し、変更を記録する。
func (c *execCtx) apply(edits ...buffer.Edit) error {
	ch, err := c.b.Apply(buffer.Transaction{BaseVersion: c.b.Version(), Edits: edits})
	if err != nil {
		return fmt.Errorf("ex: apply edits: %w", err)
	}
	c.changes = append(c.changes, ch)
	return nil
}

// runLine は、"|" で区切った Ex コマンドの列 src を順に実行する。base は、src の先頭がコマンドラインの何バイト目かである（誤りの位置に使う）。
//
// req: FR-08
func (c *execCtx) runLine(src string, base int) error {
	for {
		next, nextPos, more, err := c.runOne(src, base)
		if err != nil || !more {
			return err
		}
		src, base = next, nextPos
	}
}

// runOne は、src の先頭の Ex コマンドを 1 つ解析して実行する。
// "|" の後に続くコマンドがあれば、その文字列と位置を next、nextPos に入れ、more を true にして返す。
//
// req: FR-08
func (c *execCtx) runOne(src string, base int) (next string, nextPos int, more bool, err error) {
	startCursor := c.cursor
	swap := c.swapBackwards
	c.swapBackwards = false
	s := &scanner{src: src, base: base}
	s.skipBlanksAndColons()
	cmdStart := s.i
	r, err := c.parseRange(s, c.addrKindAt(s.rest()))
	if err != nil {
		return "", 0, false, err
	}
	s.skipBlanks()
	if s.eof() || s.peek() == '"' || s.peek() == '|' {
		// コマンド名が無ければ、範囲の最後の行へ移る（:3、:/pat/）。
		// 後に "|" が続くとき（:3|）は、Vim では :print に当たるので、範囲がバッファの外なら E16 にする。
		// 続かないとき（:3、:1,10）は、行数を超えた line2 を最後の行に丸め、line1 は見ない（Vim 9.1 の ex_range_without_command と同じ）。
		bar := s.peek() == '|'
		if r.count > 0 {
			if bar {
				if err := r.check(c.b.LineCount()); err != nil {
					return "", 0, false, err
				}
			}
			c.setCursor(min(max(r.line2, 1), c.b.LineCount())-1, CursorFirstNonBlank)
		}
		if bar {
			s.next()
			return s.rest(), s.pos(), true, nil
		}
		return "", 0, false, nil
	}

	if done, err := c.runExtCommand(s, r); done || err != nil {
		return "", 0, false, err
	}

	namePos := s.pos()
	var name string
	var cmd *command
	if s.peek() == '!' {
		// :{range}!{cmd} は、"!" をコマンド名として読む。直後の "!" は cmd の一部である。
		s.next()
		name, cmd = "!", &filterCommand
	} else {
		name = readName(s)
		if name == "" {
			return "", 0, false, errAt(namePos, codeNotEditorCommand, "エディタのコマンドではありません: %s", strings.TrimSpace(src))
		}
		cmd = resolve(name)
		if cmd == nil {
			return "", 0, false, errAt(namePos, codeNotEditorCommand, "エディタのコマンドではありません: %s", strings.TrimSpace(src))
		}
	}
	if c.inGlobal && !cmd.inGlobal {
		return "", 0, false, errAt(namePos, codeNotAvailable, ":global の中では使えません: %s", cmd.name)
	}
	// 範囲の無い :!{cmd}（FR-80）はバッファを変えないので、編集を禁止したバッファでも使える。
	if cmd.modify && c.b.ReadOnly() && (cmd != &filterCommand || r.count > 0) {
		// req: FR-85
		return "", 0, false, errAt(namePos, codeReadOnly, "%s", msgReadOnly)
	}

	inv := invocation{name: name, namePos: namePos}
	if cmd != &filterCommand && s.peek() == '!' {
		if !cmd.bang {
			return "", 0, false, errAt(s.pos(), codeNoBang, "! は使えません")
		}
		inv.bang = true
		s.next()
	}

	switch cmd.rng {
	case rangeNone:
		if r.count > 0 {
			return "", 0, false, errAt(r.pos, codeNoRange, "範囲は使えません")
		}
	case rangeCurrent, rangeWhole, rangeWindow:
		if r.count == 0 {
			switch cmd.rng {
			case rangeWhole:
				r.line1, r.line2 = 1, c.b.LineCount()
			case rangeWindow:
				r.line1, r.line2 = windowCount, windowCount
			case rangeNone, rangeCurrent:
				r.line1, r.line2 = c.cursor+1, c.cursor+1
			}
		}
		// 逆順の範囲は、行数を超えているかを調べる前に確認を求める（:9,2d も E493。Vim 9.1 と同じ）。
		// :g の cmd の中では確認を求めず、次の check で E16 にする（Vim 9.1 は global_busy の間は確認しない）。
		if r.line1 > r.line2 && !c.inGlobal {
			if !swap {
				c.confirm = &Confirm{Prompt: "範囲が逆順です。入れ替えますか (y/n)?", cursor: startCursor, src: src[cmdStart:], base: base + cmdStart}
				return "", 0, false, errAt(r.pos, codeBackwardsRange, "範囲が逆順です")
			}
			r.line1, r.line2 = r.line2, r.line1
			r.pos1, r.pos2 = r.pos2, r.pos1
		}
		// 範囲を伴うコマンドは、行数を超えた行を丸めずに E16 にする（:4d、:1,4s/a/b/、:5w、:2q。Vim 9.1 と同じ）。
		limit := c.b.LineCount()
		if cmd.rng == rangeWindow {
			limit = windowCount
		}
		if err := r.check(limit); err != nil {
			return "", 0, false, err
		}
		if cmd.rng != rangeWindow {
			// :0r は 1 行目の前を指すので、0 だったことを覚えておく。
			inv.zero = r.count > 0 && r.line2 == 0
			// 行番号 0 は、範囲を取るコマンドでは 1 行目として扱う（Vim と同じ）。
			r.line1, r.line2 = max(r.line1, 1)-1, max(r.line2, 1)-1
		}
	}
	inv.line1, inv.line2, inv.rangeCount, inv.rangePos = r.line1, r.line2, r.count, r.pos

	s.skipBlanks()
	inv.arg, inv.argPos = s.rest(), s.pos()
	if cmd.bar == barSplit {
		var n int
		inv.arg, n, more = splitBar(inv.arg)
		if more {
			inv.next, inv.nextPos, inv.hasNext = s.rest()[n:], s.pos()+n, true
		}
	}
	if err := cmd.run(c, &inv); err != nil {
		return "", 0, false, err
	}
	return inv.next, inv.nextPos, inv.hasNext, nil
}

// readName は、s の位置から本体のコマンドの名前を読む（runOne と addrKindAt）。
// 点を含む正規の名前（:option.set）は、拡張機能のコマンドの ID と同じく、空白、"|"、"!" の手前までを名前として読み、
// Ex コマンドの表にあるときだけその名前にする。それ以外は、続く英字の列を名前にする（:w、:se）。
func readName(s *scanner) string {
	if id := extCommandName(s.rest()); strings.Contains(id, ".") && lookup(id) != nil {
		s.i += len(id)
		return id
	}
	return s.letters()
}

// extCommandName は、拡張機能のコマンドの ID として読む、コマンド名の位置から続く文字列を返す。
// 空白、"|"、"!" の手前までである。ID が空白を含むコマンドは、コマンドラインからは呼べない。
func extCommandName(src string) string {
	if i := strings.IndexAny(src, " \t|!"); i >= 0 {
		return src[:i]
	}
	return src
}

// runExtCommand は、s の位置のコマンド名が拡張機能のコマンドなら、ActionExtCommand を返す動作として記録し、done を true で返す。
// 拡張機能のコマンドでなければ、何も読まずに done を false で返す。
//
// 名前は省略できず、ID と完全に一致したときだけ拡張機能のコマンドとする。
// 本体のコマンドの名前か省略形と同じ ID（"w"、"write"、"s" など）は、本体のコマンドを優先する。
// 範囲と "!" は取らず、付ければ E481 と E477 にする（ビジュアルモードの : が入れる '<,'> も E481。Vim の -range なしと同じ）。
// :g の cmd の中と、同じ行の前のコマンドが動作（Result.Actions。:w|acp.prompt の :w）を積んだ後は E319 にする。
// ただし、バッファを読み書きしない出力の欄の動作（ActionOutput。:output|acp.prompt の :output）の後には置ける。
// 動作は呼び出し側が Execute の後に行うので、後に置いた拡張機能のコマンドが先に動き、:w が拡張機能の編集の後の内容を書き出すためである。
// バッファの中で済むコマンド（:s/a/b/|acp.prompt）の後には置ける。
// 引数は、ID の後の空白を除いた行末までで、"|" を区切りとしない（Vim の -bar を付けない利用者定義のコマンドと同じ）。
//
// req: FR-08, FR-11
func (c *execCtx) runExtCommand(s *scanner, r lineRange) (done bool, err error) {
	if c.st.ExtCommand == nil {
		return false, nil
	}
	namePos := s.pos()
	id := extCommandName(s.rest())
	if id == "" || resolve(id) != nil {
		return false, nil
	}
	extID, ok := c.st.ExtCommand(id)
	if !ok {
		return false, nil
	}
	switch {
	case c.inGlobal:
		return true, errAt(namePos, codeNotAvailable, ":global の中では使えません: %s", id)
	case slices.ContainsFunc(c.actions, func(a Action) bool { return a.Kind != ActionOutput }):
		// 前のコマンドの動作（:w の書き出しなど）は、呼び出し側が Execute の後に行う。
		// 拡張機能のコマンドの問い合わせはそれより先に行われるので、Vim の左から右の順を保てない。
		return true, errAt(namePos, codeNotAvailable, ":w などの後には拡張機能のコマンドを置けません: %s", id)
	case r.count > 0:
		return true, errAt(r.pos, codeNoRange, "範囲は使えません")
	}
	s.i += len(id)
	if s.peek() == '!' {
		return true, errAt(s.pos(), codeNoBang, "! は使えません")
	}
	s.skipBlanks()
	c.actions = append(c.actions, Action{Kind: ActionExtCommand, Extension: extID, Command: id, Text: s.rest()})
	return true, nil
}

// addrKindAt は、範囲から始まるコマンドライン src のコマンドが、範囲のアドレスで何を数えるかを返す。
// 範囲を評価する前に、範囲を読み飛ばしてコマンド名を引く（Vim 9.1 の do_one_cmd と同じ順）。
// コマンド名が無いか、知らないコマンドなら addrLines を返す（誤りはコマンド名を引き直すときに出す）。
func (c *execCtx) addrKindAt(src string) addrKind {
	s := &scanner{src: src}
	skipRange(s)
	if cmd := resolve(readName(s)); cmd != nil && cmd.rng == rangeWindow {
		return addrWindows
	}
	return addrLines
}

// splitBar は、引数 arg を、"\" の付かない最初の "|" の手前までと、その後に分ける。
// 手前の部分の "\|" は "|" に戻す（ファイル名に "|" を書くとき）。
// n は、"|" を含めて読んだバイト数である。"|" が無ければ more は false である。
func splitBar(arg string) (part string, n int, more bool) {
	var sb strings.Builder
	for i := 0; i < len(arg); i++ {
		switch {
		case arg[i] == '\\' && i+1 < len(arg) && arg[i+1] == '|':
			sb.WriteByte('|')
			i++
		case arg[i] == '|':
			return sb.String(), i + 1, true
		default:
			sb.WriteByte(arg[i])
		}
	}
	return sb.String(), len(arg), false
}

// clampLine は、line（0 始まり）をバッファの中の行に丸める。
func clampLine(b *buffer.Buffer, line int) int {
	return min(max(line, 0), b.LineCount()-1)
}
