package server

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/piplupflipper/modal-editor/ext"
)

// inheritChildEnv があれば、TestInheritedSocketChild は子プロセスとして InheritedSocket を試す。
const inheritChildEnv = "SERVER_TEST_INHERIT_CHILD"

// TestInheritedSocketChild は、TestInheritedSocket が起動する子プロセスの本体である。単独では何もしない。
// 引き継いだ接続へ "conn" を書き、待ち受けで 1 本受け付けて "listener" を書いて終わる。
func TestInheritedSocketChild(t *testing.T) {
	if os.Getenv(inheritChildEnv) == "" {
		t.Skip("子プロセスとしてだけ動く")
	}
	ln, conns, err := InheritedSocket()
	if err != nil || ln == nil || len(conns) != 1 {
		fmt.Fprintf(os.Stderr, "InheritedSocket = %v, %d conns, %v\n", ln, len(conns), err)
		os.Exit(1)
	}
	if v, ok := os.LookupEnv(ext.EnvSocketFDs); ok {
		fmt.Fprintf(os.Stderr, "%s remains: %q\n", ext.EnvSocketFDs, v)
		os.Exit(1)
	}
	_, _ = io.WriteString(conns[0], "conn\n")
	_ = conns[0].Close()
	c, err := ln.Accept()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_, _ = io.WriteString(c, "listener\n")
	_ = c.Close()
	os.Exit(0)
}

// 本体が ExtraFiles で渡した待ち受けと接続を、子プロセスが InheritedSocket で受け取って使える。
func TestInheritedSocket(t *testing.T) {
	if os.Getenv(inheritChildEnv) != "" {
		t.Skip("子プロセスでは動かさない")
	}
	dir, err := os.MkdirTemp("", "srvsock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "s.sock")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	first, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	accepted, err := ln.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	lf, err := ln.File()
	if err != nil {
		t.Fatal(err)
	}
	cf, err := accepted.File()
	if err != nil {
		t.Fatal(err)
	}
	_ = accepted.Close()

	cmd := exec.Command(os.Args[0], "-test.run=^TestInheritedSocketChild$") //nolint:gosec // テストの実行ファイル自身を子プロセスとして起動する
	cmd.Env = append(os.Environ(), inheritChildEnv+"=1", ext.EnvSocketFDs+"=2")
	cmd.ExtraFiles = []*os.File{lf, cf}
	cmd.Stderr = os.Stderr
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = lf.Close()
	_ = cf.Close()

	read := func(c net.Conn) string {
		t.Helper()
		_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
		s, rerr := bufio.NewReader(c).ReadString('\n')
		if rerr != nil {
			t.Fatalf("read: %v", rerr)
		}
		return strings.TrimSpace(s)
	}
	if got := read(first); got != "conn" {
		t.Errorf("引き継いだ接続 = %q, want conn", got)
	}
	second, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	if got := read(second); got != "listener" {
		t.Errorf("引き継いだ待ち受け = %q, want listener", got)
	}
	if err := cmd.Wait(); err != nil {
		t.Errorf("子プロセス = %v", err)
	}
}

// 環境変数が無ければ何も返さず、壊れた値は誤りにする。
func TestInheritedSocketWithoutEnv(t *testing.T) {
	t.Setenv(ext.EnvSocketFDs, "")
	if ln, conns, err := InheritedSocket(); ln != nil || conns != nil || err != nil {
		t.Errorf("InheritedSocket = %v, %v, %v; want nothing", ln, conns, err)
	}
	t.Setenv(ext.EnvSocketFDs, "0")
	if _, _, err := InheritedSocket(); err == nil {
		t.Error("値 0 で誤りにならない")
	}
}
