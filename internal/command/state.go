// Package command は、編集コマンドの値（internal/input の Command）を実行し、レジスター、ドットリピート、マクロの記録を受け持つ
// （基本設計 2 章、3 章）。
//
// 編集の状態（バッファ、カーソル、望みの列、モード、レジスター、ドットリピートとマクロの記録、直前の f の検索、Ex の状態）は
// State 1 つにまとめる。State はイベントループの goroutine 1 つだけが触る前提で、排他制御をしない（基本設計 2.1）。
// 端末にも拡張機能のプロセスにも依存しない。拡張機能への問い合わせは Extensions を通して呼び出し側が差し込む。
//
// # 使い方
//
// イベントループは、キーを 1 個ずつ State.Feed に渡す。Feed はモードに応じてキーを解釈し、
// 編集コマンドの値が完成すれば実行して、バッファに適用した変更、Ex コマンドが求める動作、失敗を Result で返す。
// カーソルとモードは、Feed の後に Cursor と Mode で読む。
//
// # Vim との対応
//
// 振る舞いは Vim 9.1 を `vim -Nu NONE` で起動したときの既定の設定に合わせる。
// 'shiftwidth' と 'tabstop' は 8、'expandtab' と 'autoindent' はオフ、'joinspaces' はオン、'selection' は inclusive である。
// ただし 'backspace' は、Vim の既定の空ではなく、多くの利用者が設定する "indent,eol,start" に当たる動きにする
// （インサートモードの <BS> で、挿入を始めた位置より前の文字や行の区切りも消せる）。
// Vim と違う点は、各関数の doc コメントに書く。
package command

import (
	"errors"
	"fmt"

	"github.com/piplupflipper/modal-editor/internal/buffer"
	"github.com/piplupflipper/modal-editor/internal/command/ex"
	"github.com/piplupflipper/modal-editor/internal/command/target"
	"github.com/piplupflipper/modal-editor/internal/input"
)

// Mode は、エディタのモードである（FR-01）。
type Mode int

// モード。
const (
	// ModeNormal は、ノーマルモードである。
	ModeNormal Mode = iota
	// ModeInsert は、インサートモードである。
	ModeInsert
	// ModeVisual は、文字単位のビジュアルモード（v）である。
	ModeVisual
	// ModeVisualLine は、行単位のビジュアルモード（V）である。
	ModeVisualLine
	// ModeCommandLine は、コマンドラインモード（:）である。
	ModeCommandLine
)

// String は、モードの名前を返す。
func (m Mode) String() string {
	switch m {
	case ModeNormal:
		return "normal"
	case ModeInsert:
		return "insert"
	case ModeVisual:
		return "visual"
	case ModeVisualLine:
		return "visual line"
	case ModeCommandLine:
		return "command line"
	}
	return fmt.Sprintf("Mode(%d)", int(m))
}

// visual は、ビジュアルモードのどちらかであるかを返す。
func (m Mode) visual() bool { return m == ModeVisual || m == ModeVisualLine }

// ErrFailed は、操作が成り立たなかったことを表す（Vim がビープを鳴らす場面）。
// モーションの移動先が無い、レジスターが空、回数が行の数を超える、などである。
// 失敗した操作はバッファを変えない。
var ErrFailed = errors.New("command: operation failed")

// failf は、ErrFailed を包んだエラーを返す。
func failf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrFailed, fmt.Sprintf(format, args...))
}

// Result は、Feed、Execute、ExecuteCommandLine の結果である。
type Result struct {
	// Changes は、バッファに適用した変更を、適用した順に並べたものである。拡張機能へ差分を送る（FR-16）ときに使う。
	Changes []buffer.Change
	// Actions は、Ex コマンドが求めるエディタの動作（保存、終了、ファイルを開く）である。呼び出し側が先頭から順に行う。
	Actions []ex.Action
	// Err は、操作の失敗である。マクロの再生は、失敗した時点で止める。
	// 失敗までに適用した変更は Changes に残る。
	Err error
}

// State は、1 つのバッファを編集する状態である。
// ゼロ値は使わず、New で作る。
type State struct {
	// Columns は、望みの列の数え方である。nil なら target.CharColumns を使う。
	// 表示幅で数える実装（internal/ui）は、呼び出し側が差し込む。
	Columns target.Columns
	// Extensions は、拡張機能が提供する操作の問い合わせ先である。nil なら、拡張機能の操作は失敗する。
	Extensions Extensions
	// Ex は、Ex コマンドの呼び出しをまたいで保つ状態である。Mark は New が '< と '> を返す関数にする。
	Ex ex.State

	buf    *buffer.Buffer
	cursor buffer.Position
	want   int
	mode   Mode

	parser    *input.Parser
	registers Registers
	lastFind  target.Find

	// res は、処理中の Feed などの結果を溜める。
	res Result

	// ドットリピート（FR-06）。
	dot        *input.Command
	dotVisual  visualExtent
	replaying  bool // ドットリピートの再実行中
	insert     insertState
	visual     visualState
	cmdline    []rune
	lastCmd    string // @: が繰り返す直前のコマンドライン
	recording  rune   // マクロの記録先。0 なら記録していない
	recorded   []input.Key
	lastPlayed rune // @@ が再生するレジスター
	playDepth  int
}

// New は、バッファ b を編集する State を、ノーマルモード、カーソルは先頭で作る。
// km は、キーを解釈するキーマップである（ふつうは input.DefaultKeymap に拡張機能の操作を足したもの）。
func New(b *buffer.Buffer, km *input.Keymap) *State {
	s := &State{buf: b, parser: input.NewParser(km)}
	s.Ex.Mark = s.mark
	return s
}

// Buffer は、編集しているバッファを返す。
func (s *State) Buffer() *buffer.Buffer { return s.buf }

// Cursor は、カーソルの位置を返す。
// ノーマルモードとビジュアルモードでは、空の行を除いて行末の位置を指さない。インサートモードでは行末の位置を指しうる。
func (s *State) Cursor() buffer.Position { return s.cursor }

// SetCursor は、カーソルを pos へ移す。バッファの外の位置は最も近い位置に丸める。
// 望みの列は、移した位置の列にする。
func (s *State) SetCursor(pos buffer.Position) {
	s.cursor = s.clampPos(pos)
	if s.mode != ModeInsert {
		s.cursor = s.clampNormal(s.cursor)
	}
	s.updateWant()
}

// Want は、望みの列（Vim の curswant）を返す。target.WantEOL は「行末」を表す。
func (s *State) Want() int { return s.want }

// Mode は、現在のモードを返す。
func (s *State) Mode() Mode { return s.mode }

// Registers は、レジスターを返す。呼び出し側が読み書きしてよい。
func (s *State) Registers() *Registers { return &s.registers }

// LastFind は、; と , が繰り返す直前の f、F、t、T の検索を返す。
func (s *State) LastFind() target.Find { return s.lastFind }

// Recording は、マクロを記録しているレジスターを返す。記録していなければ 0 を返す。
func (s *State) Recording() rune { return s.recording }

// Pending は、ノーマルモードかビジュアルモードで、キーの列の解釈が途中であるかを返す。
func (s *State) Pending() bool { return s.parser.Pending() }

// Flush は、時間切れで、待っている曖昧なキーの列を確定させて実行する（input.Parser.Flush）。
// イベントループは、キーが一定の時間来なかったときに呼ぶ。
func (s *State) Flush() Result {
	s.res = Result{}
	if s.mode == ModeNormal || s.mode.visual() {
		s.runResults(s.parser.Flush())
	}
	return s.take()
}

// take は、溜めた結果を返し、溜める場所を空にする。
func (s *State) take() Result {
	r := s.res
	s.res = Result{}
	return r
}

// Feed は、キーを 1 個受け取り、現在のモードに応じて解釈し、完成した操作を実行する。
// マクロの記録中は、受け取ったキーを記録する（FR-05）。
//
// req: FR-01, FR-05
func (s *State) Feed(k input.Key) Result {
	s.res = Result{}
	if s.recording != 0 {
		s.recorded = append(s.recorded, k)
	}
	s.feed(k)
	return s.take()
}

// feed は、キーを 1 個解釈する。マクロの再生もここを通るので、記録はしない。
func (s *State) feed(k input.Key) {
	switch s.mode {
	case ModeInsert:
		s.insertKey(k)
		return
	case ModeCommandLine:
		s.cmdlineKey(k)
		return
	case ModeVisual, ModeVisualLine:
		if k.Code == input.CodeEsc && !s.parser.Pending() {
			s.exitVisual()
			return
		}
	case ModeNormal:
	}
	s.runResults(s.parser.Feed(k))
}

// runResults は、Parser の結果を順に実行する。
func (s *State) runResults(rs []input.Result) {
	for _, r := range rs {
		switch r.Status {
		case input.StatusDone:
			if err := s.execute(r.Command); err != nil {
				s.fail(err)
			}
		case input.StatusInvalid:
			s.fail(failf("invalid key sequence"))
		case input.StatusCanceled:
		}
	}
}

// fail は、失敗を結果に記録する。最初の失敗を残す。
func (s *State) fail(err error) {
	if s.res.Err == nil {
		s.res.Err = err
	}
}

// Execute は、編集コマンドの値 cmd を実行する。
// キーを通さずに値を実行したいとき（拡張機能の操作をコマンドから呼ぶなど）に使う。Feed も内部でこれと同じ処理を通る。
func (s *State) Execute(cmd input.Command) Result {
	s.res = Result{}
	if err := s.execute(cmd); err != nil {
		s.fail(err)
	}
	return s.take()
}

// execute は、編集コマンドの値を 1 つ実行する。
//
// req: FR-02
func (s *State) execute(cmd input.Command) error {
	switch cmd.Kind {
	case input.KindMotion:
		if s.mode.visual() {
			return s.visualMotion(cmd)
		}
		return s.moveMotion(cmd)
	case input.KindOperator:
		if s.mode.visual() {
			return s.visualOperator(cmd)
		}
		return s.change(cmd, func() error { return s.operator(cmd) })
	case input.KindAction:
		return s.change(cmd, func() error { return s.action(cmd) })
	case input.KindRecordStart:
		return s.startRecording(cmd.Char)
	case input.KindRecordStop:
		s.stopRecording()
		return nil
	case input.KindPlay:
		return s.play(cmd)
	case input.KindRepeat:
		return s.repeat(cmd)
	}
	return failf("unknown command kind %d", cmd.Kind)
}

// change は、バッファを変えうる操作 f を、アンドゥの 1 単位として実行する（FR-07）。
// インサートモードへ移った場合は、グループを開いたままにし、インサートモードを抜けるときに閉じる。
//
// req: FR-07
func (s *State) change(cmd input.Command, f func() error) error {
	s.buf.BeginUndoGroup()
	err := f()
	if s.mode == ModeInsert && err == nil {
		return nil
	}
	s.buf.EndUndoGroup()
	if err == nil {
		s.recordDot(cmd)
	}
	return err
}

// apply は、編集の列を 1 つのトランザクションとしてバッファに適用し、変更を結果に溜める。
func (s *State) apply(edits ...buffer.Edit) error {
	if len(edits) == 0 {
		return nil
	}
	ch, err := s.buf.Apply(buffer.Transaction{BaseVersion: s.buf.Version(), Edits: edits})
	if err != nil {
		return err
	}
	s.res.Changes = append(s.res.Changes, ch)
	return nil
}

// applyTx は、拡張機能が返したトランザクションを適用する。元にしたバージョン番号が違えば失敗する（FR-15）。
func (s *State) applyTx(tx buffer.Transaction) error {
	ch, err := s.buf.Apply(tx)
	if err != nil {
		return err
	}
	s.res.Changes = append(s.res.Changes, ch)
	return nil
}
