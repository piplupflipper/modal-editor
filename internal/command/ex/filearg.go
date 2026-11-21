package ex

import "strings"

// Ex コマンドのファイルの引数（:e、:w、:wq、:x、:tab.new、:diffsplit）の読み方と書き方（FR-195、FR-200。基本設計 7.4）。
//
// 読み方（ExpandFileArg）と、コマンドラインの補完が候補を入れる形（EscapeFileArg、EscapeFileName）は、同じ文字の組
// （fileEscapeChars）で往復する。補完の候補を入れて <CR> で開くと、候補の名前そのもののファイルを開く。
//
//   - "\" と、fileEscapeChars の文字の組は、その文字そのものにする（"\ " は空白、"\$" は "$"、"\~" は "~"、"\\" は "\"）。
//     ほかの文字の前の "\" はそのまま残す（"back\slash.txt" は "\" を含む名前）
//   - 引数の先頭の "~"（"~" だけか "~/" で始まるもの）は、ホームディレクトリにする。"~ユーザー名" は展開しない
//   - "\" の付かない "$名前" と "${名前}" は、環境変数の値にする（名前は英数字と "_"）。値の無い環境変数は、そのまま残す
//   - "|" は、"|" で引数を終えるコマンドでは、ここより前に splitBar が "\|" を戻している。補完の語では、ここで戻す
//
// Vim 9.1 と Neovim 0.12.5 は、値の無い環境変数を空にし（:e $NOPE/x は /x）、"~ユーザー名" を展開する。本体はどちらもしない
// （docs/vim-differences.md）。

// FileEnv は、ファイルの引数の "~" と環境変数を展開するための口である（State.FileEnv）。
type FileEnv interface {
	// Home は、"~" を展開するホームディレクトリを返す。空なら "~" を展開しない。
	Home() string
	// LookupEnv は、環境変数 name の値を返す。無ければ ok は false である。
	LookupEnv(name string) (value string, ok bool)
}

// fileEscapeChars は、ファイルの引数で "\" を付けると、その文字そのものになる文字である（Vim 9.1 の PATH_ESC_CHARS のうち、
// 本体が意味を持たせている文字）。"~" は引数の先頭でだけ意味を持つが、どこに付けても "~" に戻す。
const fileEscapeChars = " \t|$\\~"

// ExpandFileArg は、ファイルの引数 arg を、開くファイルのパスにする（ファイルの doc の読み方）。env が nil なら、"~" と
// 環境変数を展開せず、"\" を付けた文字を戻すだけにする。
//
// req: FR-200
func ExpandFileArg(arg string, env FileEnv) string {
	return expandFileArg(arg, env, true)
}

// ExpandFileName は、ファイルの引数のディレクトリの後に続く部分 name を、ExpandFileArg と同じ規則で読む。
// 引数の先頭でないので、"~" は展開しない。
func ExpandFileName(name string, env FileEnv) string {
	return expandFileArg(name, env, false)
}

// expandFileArg は ExpandFileArg の本体である。tilde が false なら、先頭の "~" を展開しない（パスの途中から読むとき）。
func expandFileArg(arg string, env FileEnv, tilde bool) string {
	var sb strings.Builder
	i := 0
	if tilde && env != nil && (arg == "~" || strings.HasPrefix(arg, "~/")) {
		if home := env.Home(); home != "" {
			sb.WriteString(home)
			i = 1
		}
	}
	for i < len(arg) {
		c := arg[i]
		switch {
		case c == '\\' && i+1 < len(arg) && strings.IndexByte(fileEscapeChars, arg[i+1]) >= 0:
			sb.WriteByte(arg[i+1])
			i += 2
			continue
		case c == '$' && env != nil:
			if v, n, ok := lookupEnvRef(arg[i:], env); ok {
				sb.WriteString(v)
				i += n
				continue
			}
		}
		sb.WriteByte(c)
		i++
	}
	return sb.String()
}

// lookupEnvRef は、"$" で始まる s の先頭の "$名前" か "${名前}" を env で引き、値と読んだバイト数を返す。
// 名前が無いか、値が無ければ ok は false である。
func lookupEnvRef(s string, env FileEnv) (value string, n int, ok bool) {
	name := ""
	switch {
	case len(s) > 1 && s[1] == '{':
		k := strings.IndexByte(s, '}')
		if k < 3 {
			return "", 0, false
		}
		name, n = s[2:k], k+1
	default:
		j := 1
		for j < len(s) && (s[j] == '_' || isLetter(s[j]) || isDigit(s[j])) {
			j++
		}
		name, n = s[1:j], j
	}
	if name == "" {
		return "", 0, false
	}
	v, ok := env.LookupEnv(name)
	return v, n, ok
}

// HasEnvRef は、ファイルの引数 arg が、"\" の付かない "$" を含むかを返す（補完が、環境変数を展開したパスを候補にするか決める）。
func HasEnvRef(arg string) bool {
	for i := 0; i < len(arg); i++ {
		switch arg[i] {
		case '\\':
			i++
		case '$':
			return true
		}
	}
	return false
}

// EscapeFileArg は、ファイルの名前 name を、ファイルの引数の全体として書く形にする（ExpandFileArg で name に戻る形）。
// fileEscapeChars の文字の前に "\" を付ける。ただし "~" は、先頭のものにだけ付ける（Neovim 0.12.5 の補完と同じ）。
//
// req: FR-195
func EscapeFileArg(name string) string {
	if strings.HasPrefix(name, "~") {
		return `\~` + EscapeFileName(name[1:])
	}
	return EscapeFileName(name)
}

// EscapeFileName は、ファイルの名前 name を、ファイルの引数のディレクトリの後に続けて書く形にする。
// "~" の前には "\" を付けない（引数の先頭でないので、展開されない）。ほかは EscapeFileArg と同じである。
func EscapeFileName(name string) string {
	if !strings.ContainsAny(name, " \t|$\\") {
		return name
	}
	var sb strings.Builder
	for i := 0; i < len(name); i++ {
		if c := name[i]; c != '~' && strings.IndexByte(fileEscapeChars, c) >= 0 {
			sb.WriteByte('\\')
		}
		sb.WriteByte(name[i])
	}
	return sb.String()
}

// EscapeBufferArg は、バッファの名前 name を、:b と :bd の引数に書く形にする（空白の前と "|" の前に "\" を付ける）。
// runBuffer と splitBar が戻す形（"\ " と "\|"）である。
//
// req: FR-195
func EscapeBufferArg(name string) string {
	return strings.NewReplacer(" ", `\ `, "|", `\|`).Replace(name)
}

// UnescapeBufferArg は、:b と :bd の引数に書いたバッファの名前 arg を、EscapeBufferArg の前の形に戻す。
func UnescapeBufferArg(arg string) string {
	return strings.NewReplacer(`\ `, " ", `\|`, "|").Replace(arg)
}
