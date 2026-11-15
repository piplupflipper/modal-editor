package input

import (
	"slices"
	"strings"
)

// Status は、Parser が返す結果の種類を表す。
type Status int

// 結果の種類。
const (
	// StatusDone は、編集コマンドの値が 1 つ完成したことを表す。
	StatusDone Status = iota + 1
	// StatusInvalid は、どの操作にも当たらない列（dq など）を受け取り、解釈を捨てて初期状態に戻ったことを表す。
	StatusInvalid
	// StatusCanceled は、Esc で解釈を捨てて初期状態に戻ったことを表す。
	StatusCanceled
)

// Result は、Parser の結果の 1 件である。Command は Status が StatusDone のときだけ意味を持つ。
type Result struct {
	Command Command
	Status  Status
}

// MaxCount は、回数の上限である。これを超える回数は MaxCount に丸める。
const MaxCount = 999_999_999

// registerNames は、"x の x に書けるレジスターの名前（英字と数字を除く）である。
const registerNames = `"-*+_/:.%#`

type state int

const (
	stNormal   state = iota // 初期状態。回数、レジスター、モーション、オペレーター、操作を待つ
	stRegister              // " の後。レジスターの名前を待つ
	stOperator              // オペレーターの後。回数、モーション、テキストオブジェクト、重ね押しを待つ
	stChar                  // f や r の後。文字の引数を待つ
)

// Parser は、キーを 1 個ずつ受け取り、編集コマンドの値を組み立てる状態機械である（基本設計 3 章）。
// ノーマルモードのキーだけを扱う。インサートモードとコマンドラインモードのキーは、呼び出し元が別に扱う。
//
// 入力の文法は [count]["x][count]{operator[count]{motion|text-object|operator の重ね押し}|motion|action}
// で、f、t、r のような操作の後には文字の引数が 1 つ付く。
// 回数を 2 か所に書いた場合は積を回数にする（2d3w は 6 回）。
//
// あるキーの列が登録済みの操作であり、かつより長い登録の接頭辞でもある場合（y と、拡張機能の ys）、
// 次のキーを待ってから決める。次のキーで長い登録に進めなければ、短い方に決めて、残りのキーを続けて解釈する。
// 次のキーが来ないまま決めたい場合（Vim の timeoutlen に当たる）は Flush を呼ぶ。
//
// 並行に呼ばれる前提は置かない（基本設計 2.1）。
//
// req: FR-02
type Parser struct {
	km *Keymap

	st      state
	seq     []Key // 操作を引くために溜めているキーの列
	opKeys  []Key // stOperator のときのオペレーターのキーの列
	charArg bool  // stChar のとき、文字の引数が対象（f）か操作（r）か。true なら対象

	count int // 終わった回数の積。0 は回数の指定なし
	cur   int // 入力の途中の回数。0 は入力中でない
	cmd   Command
}

// NewParser は、km を引く Parser を返す。km に後から登録した操作も、次のキーから引かれる。
func NewParser(km *Keymap) *Parser { return &Parser{km: km} }

// Pending は、解釈の途中であるか（初期状態でないか）を返す。
func (p *Parser) Pending() bool {
	return p.st != stNormal || len(p.seq) > 0 || p.count > 0 || p.cur > 0 || p.cmd.Register != 0
}

// Feed は、キーを 1 個受け取り、その時点で決まった結果を返す。
// 解釈の途中なら空のスライスを返す。
// 短い登録に決めて残りのキーを続けて解釈した場合（Parser の説明を参照）、結果は 2 件以上になりうる。
//
// Esc は、どの状態でも解釈を捨てて StatusCanceled を返す。
//
// req: FR-02
func (p *Parser) Feed(k Key) []Result {
	if k.Code == CodeEsc {
		p.reset()
		return []Result{{Status: StatusCanceled}}
	}
	return p.run([]Key{k})
}

// Flush は、より長い登録を待っているキーの列を、待つのをやめて解釈する。
// 待っている列が無ければ何もせず nil を返す。回数やオペレーターだけの途中の状態は捨てない。
func (p *Parser) Flush() []Result {
	if len(p.seq) == 0 {
		return nil
	}
	rs, rest := p.resolvePrefix()
	return append(rs, p.run(rest)...)
}

func (p *Parser) run(queue []Key) []Result {
	var out []Result
	for len(queue) > 0 {
		k := queue[0]
		queue = queue[1:]
		rs, rest := p.step(k)
		out = append(out, rs...)
		queue = append(rest, queue...)
	}
	return out
}

// step は、キーを 1 個解釈する。rest は、短い登録に決めたために解釈し直すキーの列である。
func (p *Parser) step(k Key) (results []Result, rest []Key) {
	switch p.st {
	case stRegister:
		if !k.isPlain() || !isRegisterName(k.Rune) {
			return p.fail(), nil
		}
		p.cmd.Register = k.Rune
		p.st = stNormal
		return nil, nil
	case stChar:
		if !k.isPlain() {
			return p.fail(), nil
		}
		if p.charArg {
			p.cmd.Target.Char = k.Rune
		} else {
			p.cmd.Char = k.Rune
		}
		return p.done(), nil
	case stNormal, stOperator:
	}

	if len(p.seq) == 0 && k.isPlain() {
		if d, ok := countDigit(k.Rune, p.cur > 0); ok {
			p.cur = min(p.cur*10+d, MaxCount)
			return nil, nil
		}
		if k.Rune == '"' && p.st == stNormal {
			p.endCount()
			p.st = stRegister
			return nil, nil
		}
	}

	p.seq = append(p.seq, k)
	exact, longer := p.match(p.seq)
	if longer {
		return nil, nil
	}
	if exact != nil {
		p.seq = nil
		return p.apply(*exact), nil
	}
	return p.resolvePrefix()
}

// resolvePrefix は、溜めた列のうち登録に一致する最長の接頭辞に決め、残りを rest で返す。
// 一致する接頭辞が無ければ、解釈を捨てて StatusInvalid を返す。
func (p *Parser) resolvePrefix() (results []Result, rest []Key) {
	for n := len(p.seq); n > 0; n-- {
		if exact, _ := p.match(p.seq[:n]); exact != nil {
			rest = slices.Clone(p.seq[n:])
			p.seq = nil
			return p.apply(*exact), rest
		}
	}
	return p.fail(), nil
}

// match は、現在の状態で引く操作のうち、seq に一致するものと、より長い登録があるかを返す。
// オペレーターの後では、重ね押し（オペレーターのキーの列そのものか、その最後のキー）も候補にする。
// 重ね押しとモーションの両方に一致する場合は重ね押しを優先する。
func (p *Parser) match(seq []Key) (exact *Binding, longer bool) {
	if p.st == stNormal {
		return p.km.match(normalKinds, seq)
	}
	exact, longer = p.km.match(pendingKinds, seq)
	for _, form := range [][]Key{p.opKeys, p.opKeys[len(p.opKeys)-1:]} {
		switch {
		case slices.Equal(form, seq):
			exact = &Binding{Kind: bindLine}
		case len(form) > len(seq) && slices.Equal(form[:len(seq)], seq):
			longer = true
		}
	}
	return exact, longer
}

// apply は、引いた操作を組み立て中の値に反映する。値が完成すれば結果を返す。
func (p *Parser) apply(b Binding) []Result {
	p.endCount()
	switch b.Kind {
	case BindOperator:
		p.cmd.Kind = KindOperator
		p.cmd.Operator = b.Ref
		p.cmd.Mode = b.Mode
		p.opKeys = b.Keys
		p.st = stOperator
		return nil
	case BindMotion, BindTextObject:
		tk := TargetMotion
		if b.Kind == BindTextObject {
			tk = TargetTextObject
		}
		p.cmd.Target = Target{Kind: tk, Ref: b.Ref}
		if p.st == stNormal {
			p.cmd.Kind = KindMotion
			p.cmd.Mode = b.Mode
		}
		if b.TakesChar {
			p.st, p.charArg = stChar, true
			return nil
		}
	case BindAction:
		p.cmd.Kind = KindAction
		p.cmd.Action = b.Ref
		p.cmd.Mode = b.Mode
		if b.TakesChar {
			p.st, p.charArg = stChar, false
			return nil
		}
	case bindLine:
		p.cmd.Target = Target{Kind: TargetLine}
	}
	return p.done()
}

// endCount は、入力の途中の回数を、終わった回数の積に掛ける。
func (p *Parser) endCount() {
	if p.cur == 0 {
		return
	}
	if p.count == 0 {
		p.count = p.cur
	} else {
		p.count = min(p.count*p.cur, MaxCount) // どちらも MaxCount 以下なので、64 ビットの int では積が溢れない
	}
	p.cur = 0
}

func (p *Parser) done() []Result {
	p.endCount()
	cmd := p.cmd
	cmd.Count, cmd.CountGiven = 1, false
	if p.count > 0 {
		cmd.Count, cmd.CountGiven = p.count, true
	}
	p.reset()
	return []Result{{Command: cmd, Status: StatusDone}}
}

func (p *Parser) fail() []Result {
	p.reset()
	return []Result{{Status: StatusInvalid}}
}

func (p *Parser) reset() {
	*p = Parser{km: p.km}
}

// countDigit は、r が回数の数字であれば、その値を返す。
// 0 は、回数の入力の途中（inCount）でだけ数字で、そうでなければモーションである。
func countDigit(r rune, inCount bool) (int, bool) {
	if r >= '1' && r <= '9' || (r == '0' && inCount) {
		return int(r - '0'), true
	}
	return 0, false
}

func isRegisterName(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
		strings.ContainsRune(registerNames, r)
}
