package editcmd

import (
	"errors"
	"fmt"
)

// DefaultLeader は、設定の leader を省略したときの <Leader> に当たるキーの表記である（FR-97。基本設計 7.2）。
const DefaultLeader = "<Space>"

// leaderName は、キーの表記で leader を表す名前である。大文字と小文字は区別しない（基本設計 7.2。Vim と同じ）。
const leaderName = "<leader>"

// LeadRole は、キーの解釈（internal/input の Parser）が、操作の表を引く前に先に読むキーの役割である。
// 先に読むキーで始まる列は操作の表に登録できない（input.Keymap.Bind）。表は LeadRoleOf の 1 か所に置き、
// input の登録の検査と、設定の leader の検査（ParseLeader）が共有する。
type LeadRole int

// 先に読むキーの役割。
const (
	// LeadNone は、先に読まないキーである。
	LeadNone LeadRole = iota
	// LeadCount は、回数の始まりの 1 から 9 の数字である（ノーマル、ビジュアル、オペレーターの後）。
	LeadCount
	// LeadRegister は、レジスターの指定の " である（ノーマル、ビジュアル）。
	LeadRegister
	// LeadMacro は、マクロとドットリピートの q、@、. である（ノーマル）。
	LeadMacro
)

// LeadRoleOf は、k が Parser の先に読むキーなら、その役割を返す。修飾キーの付いたキーと特殊キーは LeadNone である。
func LeadRoleOf(k Key) LeadRole {
	if !k.Plain() {
		return LeadNone
	}
	switch r := k.Rune; {
	case r >= '1' && r <= '9':
		return LeadCount
	case r == '"':
		return LeadRegister
	case r == 'q' || r == '@' || r == '.':
		return LeadMacro
	}
	return LeadNone
}

// ParseLeader は、設定の leader の値（ParseKeys の表記）をキーの列にする。
// 次の値は誤りにする。
//   - 空の列と、<Leader> を含む表記
//   - 先頭が Parser の先に読むキー（LeadRoleOf）である列。<Leader> で始まるキーを操作の表に登録できなくなるため
//   - 先頭が修飾キーの無い <Esc> か <CR> である列。モードを抜ける、コマンドラインを実行するキーが、leader の続きの待ちになるため
func ParseLeader(s string) ([]Key, error) {
	if indexLeader(s) >= 0 {
		return nil, fmt.Errorf("leader %q に <Leader> は書けない", s)
	}
	keys, err := ParseKeys(s)
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, errors.New("leader が空である")
	}
	if LeadRoleOf(keys[0]) != LeadNone {
		return nil, fmt.Errorf("leader %q は、回数、レジスター、マクロ、ドットリピートとして先に読まれるキーで始まる", s)
	}
	if k := keys[0]; k == Esc || k == Special(CodeEnter) {
		return nil, fmt.Errorf("leader %q は、モードの操作に使う %s で始まる", s, k)
	}
	return keys, nil
}

// ParseKeysLeader は ParseKeys と同じで、加えて <Leader>（大文字と小文字は区別しない）を leader のキーの列に置き換える（FR-97）。
// leader が空なら DefaultLeader を使う。
//
// 置き換えは、キーマップを作る時点の leader で行う。
// 設定の leader を変えれば、<Leader> を書いたキーの割り当ては、書いた順に関わらずすべて新しい leader で始まる。
// Vim の mapleader は :map を実行した時点の値で展開するので、後から変えても既存のマッピングは変わらない。この点が Vim と違う。
//
// 表記を左から見て、"<Leader>" の並びを見つけたところで区切り、残りを ParseKeys で読む。
// このため "<<Leader>" は '<' と leader になる（Vim と同じ）。"<lt>Leader>" は "<Leader>" の並びを含まないので、
// '<' と "Leader>" の 7 文字になる。
//
// req: FR-97
func ParseKeysLeader(s string, leader []Key) ([]Key, error) {
	if len(leader) == 0 {
		leader = MustParseKeys(DefaultLeader)
	}
	var keys []Key
	for {
		i := indexLeader(s)
		part := s
		if i >= 0 {
			part = s[:i]
		}
		ks, err := ParseKeys(part)
		if err != nil {
			return nil, err
		}
		keys = append(keys, ks...)
		if i < 0 {
			return keys, nil
		}
		keys = append(keys, leader...)
		s = s[i+len(leaderName):]
	}
}

// indexLeader は、s の中の最初の "<Leader>"（ASCII の大文字と小文字は区別しない）のバイトの位置を返す。無ければ -1 を返す。
func indexLeader(s string) int {
	for i := 0; i+len(leaderName) <= len(s); i++ {
		if s[i] == '<' && asciiLower(s[i:i+len(leaderName)]) == leaderName {
			return i
		}
	}
	return -1
}

// asciiLower は、s の ASCII の大文字だけを小文字にする。
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}
