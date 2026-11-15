// Command fakeadapter は、modal-editor-ext-dap のテストで使う、DAP を話すデバッグアダプターの代役である。
//
// 実際のプログラムは動かさず、受け取ったブレークポイントの位置で順に止まったことにする。
// 既定では標準入出力で話し、-client-addr unix:<path> を渡すと、そのソケットへ自分から接続して話す（dlv dap の --client-addr と同じ形）。
//
// 振る舞いは launch の引数の fake で変える（fakeOptions）。
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// fakeOptions は、launch の引数の fake の項目である。
type fakeOptions struct {
	// Unverified は、検証できなかったことにする行（DAP の行）である。
	Unverified []int `json:"unverified"`
	// MoveBy は、ブレークポイントを実際に置く行を、求められた行からずらす行数である。
	MoveBy int `json:"moveBy"`
	// CrashOn は、この request を受け取ったら応答せずに終了するコマンドの名前である。
	CrashOn string `json:"crashOn"`
	// FailLaunch は、launch に失敗の応答を返す。
	FailLaunch bool `json:"failLaunch"`
	// Reverse は、configurationDone の後に、逆向きの request の runInTerminal を送る。
	Reverse bool `json:"reverse"`
	// NoThreadID は、stopped の event の threadId を省く。
	NoThreadID bool `json:"noThreadId"`
	// Column は、stackTrace のフレームの列である。0 なら 1。
	Column int `json:"column"`
	// Child は、launch で子プロセス（デバッグ対象の代役）を別のプロセスグループで起動し、その pid を出力する。
	// SIGTERM を受け取ったら、子プロセスを終わらせてから終了する（dlv と同じ）。SIGKILL では子プロセスが残る。
	Child bool `json:"child"`
	// LaunchBeforeInitialized は、launch の応答を initialized の event より先に返す。
	LaunchBeforeInitialized bool `json:"launchBeforeInitialized"`
}

type message struct {
	Seq        int             `json:"seq"`
	Type       string          `json:"type"`
	Command    string          `json:"command,omitempty"`
	Arguments  json.RawMessage `json:"arguments,omitempty"`
	RequestSeq int             `json:"request_seq,omitempty"`
	Success    bool            `json:"success"`
	Message    string          `json:"message,omitempty"`
	Body       any             `json:"body,omitempty"`
	Event      string          `json:"event,omitempty"`
}

type location struct {
	path string
	line int
}

type adapter struct {
	r  *bufio.Reader
	w  io.Writer
	mu sync.Mutex
	// seq は、送ったメッセージの番号である。
	seq int

	opts       fakeOptions
	launchSeq  int
	bps        map[string][]int
	stops      []location
	cur        int
	terminate  bool

	childMu sync.Mutex
	child   *exec.Cmd
}

func main() {
	clientAddr := flag.String("client-addr", "", "unix:<path> of the client socket")
	terminate := flag.Bool("terminate", false, "support the terminate request")
	sleep := flag.Bool("sleep", false, "sleep forever (the debuggee stand-in)")
	flag.Parse()
	if *sleep {
		select {}
	}
	var r io.Reader = os.Stdin
	var w io.Writer = os.Stdout
	if *clientAddr != "" {
		c, err := net.Dial("unix", strings.TrimPrefix(*clientAddr, "unix:"))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		r, w = c, c
		fmt.Println("fakeadapter stdout")
	}
	fmt.Fprintln(os.Stderr, "fakeadapter started")
	a := &adapter{r: bufio.NewReader(r), w: w, bps: map[string][]int{}, cur: -1, terminate: *terminate}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM)
	go func() {
		<-sigs
		a.killChild()
		os.Exit(0)
	}()
	for {
		m, err := a.read()
		if err != nil {
			// 接続が切れても自分では終わらず、シグナルを待つ。拡張機能が子プロセスを終わらせる経路（SIGTERM）を試すためである。
			select {}
		}
		a.handle(m)
	}
}

func (a *adapter) read() (*message, error) {
	length := -1
	for {
		line, err := a.r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if v, ok := strings.CutPrefix(line, "Content-Length: "); ok {
			length, err = strconv.Atoi(v)
			if err != nil {
				return nil, err
			}
		}
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(a.r, buf); err != nil {
		return nil, err
	}
	var m message
	if err := json.Unmarshal(buf, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func (a *adapter) send(m message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.seq++
	m.Seq = a.seq
	body, _ := json.Marshal(m)
	fmt.Fprintf(a.w, "Content-Length: %d\r\n\r\n%s", len(body), body)
}

func (a *adapter) respond(req *message, body any) {
	a.send(message{Type: "response", RequestSeq: req.Seq, Command: req.Command, Success: true, Body: body})
}

func (a *adapter) respondError(req *message, text string) {
	a.send(message{
		Type: "response", RequestSeq: req.Seq, Command: req.Command, Success: false, Message: "failed",
		Body: map[string]any{"error": map[string]any{"id": 1, "format": text}},
	})
}

func (a *adapter) event(name string, body any) {
	a.send(message{Type: "event", Event: name, Body: body})
}

func (a *adapter) output(category, text string) {
	a.event("output", map[string]any{"category": category, "output": text})
}

func (a *adapter) handle(m *message) {
	if m.Type == "response" {
		// 逆向きの request への応答である。
		a.output("console", fmt.Sprintf("runInTerminal success=%v\n", m.Success))
		a.run()
		return
	}
	if m.Type != "request" {
		return
	}
	if a.opts.CrashOn == m.Command {
		os.Exit(3)
	}
	switch m.Command {
	case "initialize":
		var args struct {
			LinesStartAt1 bool `json:"linesStartAt1"`
		}
		_ = json.Unmarshal(m.Arguments, &args)
		if !args.LinesStartAt1 {
			a.respondError(m, "this fake requires linesStartAt1")
			return
		}
		a.respond(m, map[string]any{"supportsConfigurationDoneRequest": true, "supportsTerminateRequest": a.terminate})
	case "launch":
		var args struct {
			Program string      `json:"program"`
			Fake    fakeOptions `json:"fake"`
		}
		_ = json.Unmarshal(m.Arguments, &args)
		a.opts = args.Fake
		if a.opts.FailLaunch {
			a.respondError(m, "cannot build "+args.Program)
			return
		}
		a.output("console", "launch "+args.Program+"\n")
		a.output("console", fmt.Sprintf("adapter %d\n", os.Getpid()))
		if a.opts.Child {
			if err := a.startChild(); err != nil {
				a.respondError(m, err.Error())
				return
			}
		}
		if wd, err := os.Getwd(); err == nil {
			a.output("console", "cwd "+wd+"\n")
		}
		if a.opts.LaunchBeforeInitialized {
			a.respond(m, nil)
		} else {
			a.launchSeq = m.Seq
		}
		a.event("initialized", nil)
	case "setBreakpoints":
		var args struct {
			Source struct {
				Path string `json:"path"`
			} `json:"source"`
			Breakpoints []struct {
				Line int `json:"line"`
			} `json:"breakpoints"`
		}
		_ = json.Unmarshal(m.Arguments, &args)
		var lines []int
		var res []map[string]any
		for _, b := range args.Breakpoints {
			line := b.Line + a.opts.MoveBy
			lines = append(lines, line)
			res = append(res, map[string]any{"verified": !slices.Contains(a.opts.Unverified, b.Line), "line": line})
		}
		a.bps[args.Source.Path] = lines
		a.output("console", fmt.Sprintf("setBreakpoints %s %v\n", filepath.Base(args.Source.Path), lines))
		a.respond(m, map[string]any{"breakpoints": res})
	case "configurationDone":
		a.respond(m, nil)
		if a.launchSeq != 0 {
			a.respond(&message{Seq: a.launchSeq, Command: "launch"}, nil)
		}
		a.output("stdout", "hello from debuggee\n")
		a.output("telemetry", "telemetry must not be shown\n")
		if a.opts.Reverse {
			a.send(message{Type: "request", Command: "runInTerminal", Arguments: json.RawMessage(`{"args":["true"]}`)})
			return
		}
		a.run()
	case "threads":
		a.respond(m, map[string]any{"threads": []map[string]any{{"id": 1, "name": "main"}}})
	case "stackTrace":
		loc := a.stops[a.cur]
		col := a.opts.Column
		if col == 0 {
			col = 1
		}
		a.respond(m, map[string]any{"stackFrames": []map[string]any{{
			"id": 1000, "name": "main.main", "line": loc.line, "column": col,
			"source": map[string]any{"name": filepath.Base(loc.path), "path": loc.path},
		}}})
	case "continue":
		a.respond(m, map[string]any{"allThreadsContinued": true})
		a.next("breakpoint")
	case "next":
		a.respond(m, nil)
		a.stops[a.cur].line++
		a.stopped("step")
	case "stepIn", "stepOut":
		a.respond(m, nil)
		a.stopped("step")
	case "terminate":
		a.respond(m, nil)
		a.output("console", "terminate requested\n")
		a.killChild()
		a.event("terminated", nil)
	case "disconnect":
		a.respond(m, nil)
		a.killChild()
		os.Exit(0)
	default:
		a.respondError(m, "unknown command "+m.Command)
	}
}

// startChild は、自分を -sleep で別のプロセスグループに起動し、pid を出力する。
func (a *adapter) startChild() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "-sleep")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	a.childMu.Lock()
	a.child = cmd
	a.childMu.Unlock()
	a.output("console", fmt.Sprintf("child %d\n", cmd.Process.Pid))
	return nil
}

// killChild は、子プロセスを終わらせて待つ。
func (a *adapter) killChild() {
	a.childMu.Lock()
	defer a.childMu.Unlock()
	if a.child != nil {
		_ = a.child.Process.Kill()
		_ = a.child.Wait()
		a.child = nil
	}
}

// run は、ブレークポイントの位置を止まる順に並べ、最初の位置で止まる。
func (a *adapter) run() {
	paths := make([]string, 0, len(a.bps))
	for p := range a.bps {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	for _, p := range paths {
		for _, l := range a.bps[p] {
			a.stops = append(a.stops, location{p, l})
		}
	}
	a.next("breakpoint")
}

// next は、次のブレークポイントで止まる。無ければ終了する。
func (a *adapter) next(reason string) {
	a.cur++
	if a.cur >= len(a.stops) {
		a.event("exited", map[string]any{"exitCode": 0})
		a.event("terminated", nil)
		return
	}
	a.stopped(reason)
}

func (a *adapter) stopped(reason string) {
	body := map[string]any{"reason": reason, "allThreadsStopped": true}
	if !a.opts.NoThreadID {
		body["threadId"] = 1
	}
	a.event("stopped", body)
}
