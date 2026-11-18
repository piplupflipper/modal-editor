package editcmd

import (
	"errors"
	"fmt"
)

// DefaultLeader は、設定の leader を省略したときの <Leader> に当たるキーの表記である（FR-97。基本設計 7.2）。
const DefaultLeader = "<Space>"

// leaderName は、キーの表記で leader を表す名前である。大文字と小文字は区別しない（基本設計 7.2。Vim と同じ）。
const leaderName = "<leader>"

// ParseLeader は、設定の leader の値（ParseKeys の表記）をキーの列にする。
// 空の列と、<Leader> を含む表記は誤りにする。
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
