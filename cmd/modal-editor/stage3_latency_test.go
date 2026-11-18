package main

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/piplupflipper/modal-editor/internal/server"
	"github.com/piplupflipper/modal-editor/internal/term/ptytest"
)

// envLatency は、段階 3 の応答の計測（TestStage3Latency）を動かす環境変数である。1 のときだけ動かす（段階 1 の計測と同じ）。
const envLatency = "MODAL_EDITOR_LATENCY"

// envLatencyRuns は、統計に使う回数を latencyRuns から変える環境変数である。計測の道具を試すときに少なくする。
const envLatencyRuns = "MODAL_EDITOR_LATENCY_RUNS"

// envLatencyLog は、外れ値を調べるための記録を置くディレクトリを渡す環境変数である。
// 指定すると、回ごとの時刻と値（samples.csv。組み合わせ、回、キーを書いた時刻の UNIX ナノ秒、値のナノ秒）、
// 本体のプロセスを起動した時刻（procs.csv）、本体の標準エラー出力（<組み合わせ>-server.log、-client.log）を書く。
// GODEBUG=gctrace=1 と一緒に使うと、本体のガベージコレクションの記録が標準エラー出力のファイルに入る。
const envLatencyLog = "MODAL_EDITOR_LATENCY_LOG"

// 要件定義 8.1 の測り方。
const (
	latencyLines   = 10_000 // NFR-05 と同じ形のファイルの行数
	latencyWarmup  = 20     // 捨てる最初の回数
	latencyRuns    = 300    // 統計に使う回数
	latencyTimeout = 5 * time.Second
	// latencyWidth と latencyHeight は、偽の端末の大きさである。
	latencyWidth, latencyHeight = 80, 24
	// insertLineLen は、インサートモードの入力で、改行を挟むまでに打つ文字の数である（行が端末の幅を超えて横にスクロールしないようにする）。
	insertLineLen = 60
)

// envLatencyOps は、計測する操作を名前（latencyOp.name）のカンマ区切りで絞る環境変数である。空ならすべて測る。
const envLatencyOps = "MODAL_EDITOR_LATENCY_OPS"

// latencyOp は、計測する操作である。
type latencyOp struct {
	name string
	// otherPre は、回ごとに計測するキーの前に打つ、計測しないキーである。待たずに打ち、その後に間隔を置く。
	// 分けた構成ではもう 1 つの編集の client（-connect で同じ server に繋ぐ）で打ち、1 つのプロセスの構成では測る client で打つ（要件定義 8.1）。
	otherPre string
	// otherSetup は、分けた構成のもう 1 つの client で、計測の前に 1 回だけ打つキーと、打った後に待つその client の画面の条件である。
	otherSetup func(s *latencySession)
	// setup は、計測の前に 1 回だけ打つキーと、打った後に待つ画面の条件である。nil なら何もしない。
	setup func(s *latencySession)
	// step は、i 回目（0 から）に計測するキーと、その入力で変わった画面の条件を返す。
	// 計測しない準備のキー（pre）があれば、先に打って画面の条件 preDone を待つ。
	step func(i int) (pre string, preDone func(*vscreen) bool, key string, done func(*vscreen) bool)
}

// rulerIs は、ステータス行の位置の表示が「行,列」であるかの条件を返す。
func rulerIs(line, col int) func(*vscreen) bool {
	want := fmt.Sprintf(" %d,%d ", line, col)
	return func(v *vscreen) bool { return strings.Contains(v.row(latencyHeight-2), want) }
}

var latencyOps = []latencyOp{
	{
		// ノーマルモードの j。1 行目から下へ動かす。終わりは、ステータス行の位置が新しい行になったとき。
		name: "j",
		step: func(i int) (string, func(*vscreen) bool, string, func(*vscreen) bool) {
			return "", nil, "j", rulerIs(i+2, 1)
		},
	},
	{
		// インサートモードでの 1 文字の入力。insertLineLen 文字ごとに、計測しない改行を挟む。
		name: "insert",
		setup: func(s *latencySession) {
			s.keys("i", func(v *vscreen) bool { return strings.Contains(v.row(latencyHeight-2), "INSERT") })
		},
		step: func(i int) (string, func(*vscreen) bool, string, func(*vscreen) bool) {
			line, col := i/insertLineLen+1, i%insertLineLen+1
			pre, preDone := "", func(*vscreen) bool { return true }
			if col == 1 && line > 1 {
				pre, preDone = "\r", rulerIs(line, 1)
			}
			return pre, preDone, "x", rulerIs(line, col+1)
		},
	},
	{
		// dd。1 行目を消し続ける。終わりは、画面の 1 行目が次の行の中身になったとき。
		name: "dd",
		step: func(i int) (string, func(*vscreen) bool, string, func(*vscreen) bool) {
			want := fmt.Sprintf("line %06d:", i+2)
			return "", nil, "dd", func(v *vscreen) bool { return strings.HasPrefix(v.row(0), want) }
		},
	},
	{
		// p。最初に 1 行目を yy し、その後は貼るたびに下の行へ移る。終わりは、ステータス行の位置が新しい行になったとき。
		name: "p",
		setup: func(s *latencySession) {
			s.keys("yy", func(*vscreen) bool { return true })
			time.Sleep(100 * time.Millisecond) // yy の後の描画を待つ（画面は変わらないことがある）
		},
		step: func(i int) (string, func(*vscreen) bool, string, func(*vscreen) bool) {
			return "", nil, "p", rulerIs(i+2, 1)
		},
	},
	{
		// もう 1 つの編集の client で yy した直後に、測る client で p を打つ（要件定義 8.1。#215）。
		// 分けた構成では、もう 1 つの client（同じ server に -connect で繋いだもの）で「from-b」の行を作って毎回 yy し、
		// 測る client の p の終わりは、ステータス行の位置が新しい行になり、画面に from-b が出たときとする。
		// 1 つのプロセスの構成では、同じ client で yy してから p を打つ（貼るのは測る client の行）。
		name:     "p-other",
		otherPre: "yy",
		otherSetup: func(s *latencySession) {
			s.keys("ifrom-b\x1b", func(v *vscreen) bool { return strings.Contains(v.row(0), "from-b") })
		},
		step: func(i int) (string, func(*vscreen) bool, string, func(*vscreen) bool) {
			return "", nil, "p", rulerIs(i+2, 1)
		},
	},
}

// screenHas は、画面のどこかに s があるかの条件を返す。
func screenHas(s string) func(*vscreen) bool {
	return func(v *vscreen) bool { return strings.Contains(v.String(), s) }
}

// selectedOps は、MODAL_EDITOR_LATENCY_OPS で絞った操作を返す。
func selectedOps() []latencyOp {
	names := os.Getenv(envLatencyOps)
	if names == "" {
		return latencyOps
	}
	var ops []latencyOp
	for _, op := range latencyOps {
		if slices.Contains(strings.Split(names, ","), op.name) {
			ops = append(ops, op)
		}
	}
	return ops
}

// TestStage3Latency は、要件定義 8.1 の段階 3 の応答を測る。
// 1 つのプロセスの構成と、server（-listen）と編集の client（-connect）を別のプロセスにした構成（分けた構成）で、
// 80 列 24 行の偽の端末にキーを書いてから、その入力で変わった画面が偽の端末から読めるまでの時間を測る。
// 結果と判定を RESULT と VERDICT で始まる行に出す。手順と結果は docs/measurements/stage3-client-server-latency.md にある。
//
// 環境変数 MODAL_EDITOR_LATENCY が 1 でなければ飛ばす。
// 本体は、MODAL_EDITOR_BUILD_DIR があればその下の modal-editor を、無ければ go build で作ったもの（-race なし）を使う。
func TestStage3Latency(t *testing.T) {
	if os.Getenv(envLatency) != "1" {
		t.Skipf("%s=1 のときだけ動かす（計測に十数分かかる）", envLatency)
	}
	bin := latencyBinary(t)
	file := filepath.Join(t.TempDir(), "lines10000.txt")
	var b strings.Builder
	for i := 1; i <= latencyLines; i++ {
		fmt.Fprintf(&b, "line %06d: the quick brown fox jumps over the lazy dog 0123456789\n", i)
	}
	if err := os.WriteFile(file, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Logf("ENV cpus=%d GOMAXPROCS=%d go=%s", runtime.NumCPU(), runtime.GOMAXPROCS(0), runtime.Version())
	load0, cpu0 := loadAvg(t), readCPUStat(t)
	var ru0 syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_CHILDREN, &ru0) // 読めなければ 0 のまま

	type key struct {
		op       string
		interval time.Duration
		split    bool
	}
	results := map[key]latencyStats{}
	for _, op := range selectedOps() {
		for _, interval := range []time.Duration{20 * time.Millisecond, 200 * time.Millisecond} {
			for _, split := range []bool{false, true} {
				st := measureLatency(t, bin, file, op, interval, split)
				results[key{op.name, interval, split}] = st
				t.Logf("RESULT op=%s interval=%v config=%s p50=%.3fms p99=%.3fms max=%.3fms n=%d",
					op.name, interval, configName(split), st.p50, st.p99, st.max, st.n)
			}
		}
	}

	var ru1 syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_CHILDREN, &ru1)
	load1, cpu1 := loadAvg(t), readCPUStat(t)
	t.Logf("ENV loadavg before=%s after=%s cpu-busy=%.1f%% children-nivcsw=%d",
		load0, load1, cpu1.busySince(cpu0), ru1.Nivcsw-ru0.Nivcsw)

	for _, op := range selectedOps() {
		for _, interval := range []time.Duration{20 * time.Millisecond, 200 * time.Millisecond} {
			one, sp := results[key{op.name, interval, false}], results[key{op.name, interval, true}]
			delay := sp.p99 <= one.p99+1
			maxOK := sp.max <= 20
			t.Logf("VERDICT op=%s interval=%v p99-split=%.3fms p99-single+1ms=%.3fms delay-ok=%v max-split=%.3fms max-ok=%v",
				op.name, interval, sp.p99, one.p99+1, delay, sp.max, maxOK)
		}
	}
}

func configName(split bool) string {
	if split {
		return "split"
	}
	return "single"
}

// latencyBinary は、計測する本体のパスを返す。
func latencyBinary(t *testing.T) string {
	t.Helper()
	if dir := os.Getenv(envBuildDir); dir != "" {
		return filepath.Join(dir, "modal-editor")
	}
	bin := filepath.Join(t.TempDir(), "modal-editor")
	cmd := exec.Command("go", "build", "-o", bin, ".") //nolint:gosec // 計測する本体をビルドする
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

type latencyStats struct {
	p50, p99, max float64 // ms
	n             int
}

// statsOf は、d の p50、p99、最大を返す。p は昇順に並べた ⌈n × p / 100⌉ 番目（nearest-rank。段階 1 の計測と同じ）。
func statsOf(d []time.Duration) latencyStats {
	s := slices.Sorted(slices.Values(d))
	rank := func(p int) float64 {
		i := int(math.Ceil(float64(len(s)*p)/100)) - 1
		return float64(s[max(i, 0)].Nanoseconds()) / 1e6
	}
	return latencyStats{p50: rank(50), p99: rank(99), max: float64(s[len(s)-1].Nanoseconds()) / 1e6, n: len(s)}
}

// latencySession は、計測する 1 つの構成のエディタである。
type latencySession struct {
	t      *testing.T
	master *os.File
	reads  chan ptyRead
	screen *vscreen
}

// keys は、計測せずに k を打ち、画面が done を満たすまで待つ。
func (s *latencySession) keys(k string, done func(*vscreen) bool) {
	s.t.Helper()
	if _, err := s.master.WriteString(k); err != nil {
		s.t.Fatal(err)
	}
	s.waitFor(done)
}

// waitFor は、画面が done を満たすまで出力を読み、満たした read の時刻を返す。
func (s *latencySession) waitFor(done func(*vscreen) bool) time.Time {
	s.t.Helper()
	if done(s.screen) {
		return time.Now()
	}
	timeout := time.After(latencyTimeout)
	for {
		select {
		case r, ok := <-s.reads:
			if !ok {
				s.t.Fatalf("端末の出力が閉じた。画面:\n%s", s.screen)
			}
			s.screen.feed(r.data)
			if done(s.screen) {
				return r.at
			}
		case <-timeout:
			s.t.Fatalf("%v の間、期待する画面にならない。画面:\n%s", latencyTimeout, s.screen)
		}
	}
}

// drain は、溜まった出力を読んで画面に当てる（前の操作の後に遅れて届いた出力を、次の計測に混ぜない）。
func (s *latencySession) drain() {
	for {
		select {
		case r, ok := <-s.reads:
			if !ok {
				return
			}
			s.screen.feed(r.data)
		default:
			return
		}
	}
}

// measureLatency は、1 つの構成で op を latencyWarmup + latencyRuns 回測る。回ごとに、キーの前に interval だけ待つ。
func measureLatency(t *testing.T, bin, file string, op latencyOp, interval time.Duration, split bool) latencyStats {
	t.Helper()
	// 回ごとに元のファイルの写しを開く（dd と p でファイルを変えないが、念のため保存しない）。
	path := filepath.Join(t.TempDir(), filepath.Base(file))
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0o600); err != nil { //nolint:gosec // テストの一時ディレクトリに書く
		t.Fatal(err)
	}
	xdg := t.TempDir()
	env := append(muxFreeEnv(), "TERM=xterm-256color", "XDG_RUNTIME_DIR="+filepath.Join(xdg, "run"))
	for _, name := range []string{"CONFIG", "DATA", "STATE", "CACHE"} {
		env = append(env, "XDG_"+name+"_HOME="+filepath.Join(xdg, strings.ToLower(name)))
	}
	if err = os.Mkdir(filepath.Join(xdg, "run"), 0o700); err != nil {
		t.Fatal(err)
	}

	name := fmt.Sprintf("%s-%v-%s", op.name, interval, configName(split))
	logDir := os.Getenv(envLatencyLog)
	args := []string{path}
	var srv *exec.Cmd
	var srvDone chan error
	if split {
		sock := filepath.Join(xdg, "run", "m", server.SocketName)
		srv = exec.Command(bin, "-listen", sock, path) //nolint:gosec // 計測する本体を server として起動する
		srv.Env = env
		var out bytes.Buffer
		srv.Stdout, srv.Stderr = &out, &out
		if logDir != "" {
			f := createLog(t, filepath.Join(logDir, name+"-server.log"))
			srv.Stdout, srv.Stderr = f, f
		}
		if err = srv.Start(); err != nil {
			t.Fatal(err)
		}
		appendLog(t, logDir, "procs.csv", fmt.Sprintf("%s,server,%d\n", name, time.Now().UnixNano()))
		srvDone = make(chan error, 1)
		go func() { srvDone <- srv.Wait() }()
		deadline := time.Now().Add(latencyTimeout)
		for {
			if _, err = os.Stat(sock); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("server のソケットができない。出力: %q", out.String())
			}
			time.Sleep(time.Millisecond)
		}
		args = []string{"-connect", sock}
	}

	defer func() {
		if srv != nil {
			_ = srv.Process.Kill()
			if werr := <-srvDone; werr != nil {
				t.Logf("止めた server: %v", werr)
			}
		}
	}()
	s, stop := startLatencyClient(t, bin, args, env, logDir, name+"-client")
	defer stop()
	s.waitFor(rulerIs(1, 1))
	if op.setup != nil {
		op.setup(s)
	}
	// 分けた構成で otherPre を打つ操作は、同じ server にもう 1 つの編集の client を繋ぐ（#215）。
	other := s
	done := func(keyDone func(*vscreen) bool) func(*vscreen) bool { return keyDone }
	if split && op.otherPre != "" {
		var stopOther func()
		other, stopOther = startLatencyClient(t, bin, args, env, logDir, name+"-other")
		defer stopOther()
		other.waitFor(screenHas("NORMAL"))
		if op.otherSetup != nil {
			op.otherSetup(other)
		}
		done = func(keyDone func(*vscreen) bool) func(*vscreen) bool {
			return func(v *vscreen) bool { return keyDone(v) && screenHas("from-b")(v) }
		}
	}
	runs := latencyRuns
	if n, aerr := strconv.Atoi(os.Getenv(envLatencyRuns)); aerr == nil && n > 0 {
		runs = n
	}
	var samples []time.Duration
	var b strings.Builder // 回ごとの記録（MODAL_EDITOR_LATENCY_LOG）。計測の後にまとめて書く
	for i := range latencyWarmup + runs {
		pre, preDone, key, keyDone := op.step(i)
		if pre != "" {
			s.keys(pre, preDone)
		}
		if op.otherPre != "" {
			if _, err = other.master.WriteString(op.otherPre); err != nil {
				t.Fatal(err)
			}
		}
		time.Sleep(interval)
		s.drain()
		if other != s {
			other.drain()
		}
		start := time.Now()
		if _, err = s.master.WriteString(key); err != nil {
			t.Fatal(err)
		}
		end := s.waitFor(done(keyDone))
		if i >= latencyWarmup {
			samples = append(samples, end.Sub(start))
		}
		fmt.Fprintf(&b, "%s,%d,%d,%d\n", name, i, start.UnixNano(), end.Sub(start).Nanoseconds())
	}
	appendLog(t, logDir, "samples.csv", b.String())
	return statsOf(samples)
}

// startLatencyClient は、80 列 24 行の偽の端末の上で本体を args で起動し、その画面を読む latencySession と、止める関数を返す。
// logDir が空でなければ、本体の標準エラー出力を logDir/<name>.log に書き、起動した時刻を procs.csv に書く。
func startLatencyClient(t *testing.T, bin string, args, env []string, logDir, name string) (*latencySession, func()) {
	t.Helper()
	master, tty := ptytest.Open(t, latencyWidth, latencyHeight)
	cmd := exec.Command(bin, args...) //nolint:gosec // 計測する本体を偽の端末の上で起動する
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	if logDir != "" {
		// GODEBUG=gctrace=1 の行を画面に混ぜないよう、標準エラー出力だけをファイルへ出す（本体は標準入力と標準出力で端末を使う）。
		cmd.Stderr = createLog(t, filepath.Join(logDir, name+".log"))
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	reads := make(chan ptyRead, 4096)
	go func() {
		defer close(reads)
		buf := make([]byte, 65536)
		for {
			n, rerr := master.Read(buf)
			if n > 0 {
				reads <- ptyRead{at: time.Now(), data: bytes.Clone(buf[:n])}
			}
			if rerr != nil {
				return
			}
		}
	}()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	appendLog(t, logDir, "procs.csv", fmt.Sprintf("%s,client,%d\n", name, time.Now().UnixNano()))
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	stop := func() {
		_ = cmd.Process.Kill() // 終わっていなければ止める
		if werr := <-done; werr != nil {
			t.Logf("止めた client: %v", werr)
		}
	}
	return &latencySession{t: t, master: master, reads: reads, screen: newVScreen(latencyWidth, latencyHeight)}, stop
}

// createLog は、計測の記録のファイルを作る。テストの終わりで閉じる。
func createLog(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.Create(path) //nolint:gosec // 計測する人が MODAL_EDITOR_LATENCY_LOG で指定したディレクトリに書く
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// appendLog は、dir が空でなければ、dir/name に s を書き足す。
func appendLog(t *testing.T, dir, name, s string) {
	t.Helper()
	if dir == "" {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) //nolint:gosec // 計測する人が指定したディレクトリに書く
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err = f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

// vscreen は、本体が端末へ書いた出力から作った画面の文字である。internal/term の Painter が使う制御シーケンスだけを解釈する
// （CUP で位置を決め、ED 2 で消す。属性とカーソルの形は読み飛ばす）。
type vscreen struct {
	w, h    int
	cells   [][]rune
	rowCur  int // カーソルの行（0 から）
	col     int // カーソルの列（0 から）
	pending []byte
}

func newVScreen(w, h int) *vscreen {
	v := &vscreen{w: w, h: h}
	v.clear()
	return v
}

func (v *vscreen) clear() {
	v.cells = make([][]rune, v.h)
	for r := range v.cells {
		v.cells[r] = []rune(strings.Repeat(" ", v.w))
	}
}

func (v *vscreen) row(r int) string { return string(v.cells[r]) }

func (v *vscreen) String() string {
	rows := make([]string, v.h)
	for r := range rows {
		rows[r] = v.row(r)
	}
	return strings.Join(rows, "\n")
}

// feed は、端末への出力 data を画面に当てる。制御シーケンスが read の境目で切れていれば、続きを待つ。
func (v *vscreen) feed(data []byte) {
	b := slices.Concat(v.pending, data)
	v.pending = nil
	for len(b) > 0 {
		if b[0] != 0x1b {
			r, size := utf8DecodeRune(b)
			if size == 0 {
				v.pending = b
				return
			}
			b = b[size:]
			if r < 0x20 {
				continue
			}
			if v.rowCur >= 0 && v.rowCur < v.h && v.col >= 0 && v.col < v.w {
				v.cells[v.rowCur][v.col] = r
			}
			v.col++
			continue
		}
		n := v.escape(b)
		if n == 0 {
			v.pending = b
			return
		}
		b = b[n:]
	}
}

// escape は、b の先頭の制御シーケンスを解釈し、その長さを返す。切れていれば 0 を返す。
func (v *vscreen) escape(b []byte) int {
	if len(b) < 2 {
		return 0
	}
	switch b[1] {
	case '[':
		for i := 2; i < len(b); i++ {
			if b[i] >= 0x40 && b[i] <= 0x7e {
				params := string(b[2:i])
				switch b[i] {
				case 'H':
					row, col := 1, 1
					if p := strings.Split(params, ";"); len(p) == 2 {
						row, _ = strconv.Atoi(p[0])
						col, _ = strconv.Atoi(p[1])
					}
					v.rowCur, v.col = row-1, col-1
				case 'J':
					if params == "2" {
						v.clear()
					}
				}
				return i + 1
			}
		}
		return 0
	case ']', 'P', '_', '^':
		for i := 2; i < len(b); i++ {
			if b[i] == 0x07 {
				return i + 1
			}
			if b[i] == 0x1b && i+1 < len(b) && b[i+1] == '\\' {
				return i + 2
			}
		}
		return 0
	}
	return 2
}

// utf8DecodeRune は、b の先頭の文字とそのバイト数を返す。多バイト文字が read の境目で切れていれば 0, 0 を返す。
func utf8DecodeRune(b []byte) (rune, int) {
	if !utf8.FullRune(b) {
		return 0, 0
	}
	return utf8.DecodeRune(b)
}

// cpuStat は、/proc/stat の cpu の行の、全体と待機の時間である。
type cpuStat struct{ total, idle uint64 }

func readCPUStat(t *testing.T) cpuStat {
	t.Helper()
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		t.Fatal(err)
	}
	line, _, _ := strings.Cut(string(b), "\n")
	var s cpuStat
	for i, f := range strings.Fields(line)[1:] {
		n, _ := strconv.ParseUint(f, 10, 64)
		s.total += n
		if i == 3 || i == 4 { // idle と iowait
			s.idle += n
		}
	}
	return s
}

// busySince は、prev からの CPU の使用率（%）を返す。
func (s cpuStat) busySince(prev cpuStat) float64 {
	total := s.total - prev.total
	if total == 0 {
		return 0
	}
	return 100 * float64(total-(s.idle-prev.idle)) / float64(total)
}

func loadAvg(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		t.Fatal(err)
	}
	f := strings.Fields(string(b))
	return strings.Join(f[:3], "/")
}
