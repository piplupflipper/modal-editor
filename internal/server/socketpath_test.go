package server

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// shortDir は、権限 0700 の短いパスのディレクトリを作る（ソケットのパスを 103 バイトに収めるため。t.TempDir はテストの名前を含んで長い）。
func shortDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "ms")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return d
}

func TestUnitDirName(t *testing.T) {
	a := UnitDirName("/tmp/tmux-1000/default:@1")
	if a != UnitDirName("/tmp/tmux-1000/default:@1") {
		t.Error("同じ単位から違う名前ができた")
	}
	if a == UnitDirName("/tmp/tmux-1000/default:@2") || a == UnitDirName("/tmp/tmux-1000/other:@1") {
		t.Error("違う単位から同じ名前ができた")
	}
	if !strings.HasPrefix(a, "u-") || len(a) != 2+unitNameLen || strings.ContainsRune(a, '/') {
		t.Errorf("名前 = %q", a)
	}
}

// 同じ単位からは同じパスを決め、ディレクトリを 0700 で作る。XDG_RUNTIME_DIR が無ければ一時ディレクトリの modal-editor-<uid>/ に置く。
//
// req: FR-59
func TestSocketPathForUnit(t *testing.T) {
	runtime, tmp := shortDir(t), shortDir(t)
	unit := "/tmp/tmux-1000/default:@1"
	got, err := SocketPathForUnit(unit, runtime, tmp)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(runtime, "modal-editor", UnitDirName(unit), SocketName)
	if got != want {
		t.Errorf("パス = %q, want %q", got, want)
	}
	if again, aerr := SocketPathForUnit(unit, runtime, tmp); aerr != nil || again != got {
		t.Errorf("2 回目 = %q, %v; want %q", again, aerr, got)
	}
	for _, d := range []string{filepath.Dir(got), filepath.Dir(filepath.Dir(got))} {
		fi, serr := os.Stat(d)
		if serr != nil {
			t.Error(serr)
		} else if fi.Mode().Perm() != 0o700 {
			t.Errorf("%s の権限 = %v, want 0700", d, fi.Mode())
		}
	}

	for _, rt := range []string{"", "relative", filepath.Join(runtime, "missing")} {
		got, err = SocketPathForUnit(unit, rt, tmp)
		want = filepath.Join(tmp, "modal-editor-"+strconv.Itoa(os.Geteuid()), UnitDirName(unit), SocketName)
		if err != nil || got != want {
			t.Errorf("XDG_RUNTIME_DIR=%q のパス = %q, %v; want %q", rt, got, err, want)
		}
	}
}

// 他の利用者の権限があるディレクトリは使わず、次の候補へ移る。どれも使えなければ誤りを返す。
func TestSocketPathForUnitRejectsSharedDir(t *testing.T) {
	runtime, tmp := shortDir(t), shortDir(t)
	unit := "u"
	shared := filepath.Join(runtime, "modal-editor")
	if err := os.Mkdir(shared, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(shared, 0o777); err != nil { //nolint:gosec // 検査で断られることを確かめる
		t.Fatal(err)
	}
	got, err := SocketPathForUnit(unit, runtime, tmp)
	want := filepath.Join(tmp, "modal-editor-"+strconv.Itoa(os.Geteuid()), UnitDirName(unit), SocketName)
	if err != nil || got != want {
		t.Errorf("パス = %q, %v; want %q（一時ディレクトリへ移る）", got, err, want)
	}

	if err := os.Chmod(filepath.Dir(got), 0o755); err != nil { //nolint:gosec // 検査で断られることを確かめる
		t.Fatal(err)
	}
	if got, err := SocketPathForUnit(unit, runtime, tmp); err == nil {
		t.Errorf("どちらも使えないのにパス %q を返した", got)
	}
}

// ソケットのパスが長すぎる候補は使わない。
func TestSocketPathForUnitTooLong(t *testing.T) {
	long := filepath.Join(privateDir(t), strings.Repeat("d", 80))
	if err := os.Mkdir(long, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := SocketPathForUnit("u", long, long); err == nil || !strings.Contains(err.Error(), "too long") {
		t.Errorf("誤り = %v, want too long", err)
	}
}

// ロックを持つ間は、2 つ目の Lock が ErrLocked になり、閉じると取れる。
//
// req: FR-59
func TestLock(t *testing.T) {
	dir := privateDir(t)
	first, err := Lock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Lock(dir); !errors.Is(err, ErrLocked) {
		t.Fatalf("2 つ目の Lock = %v, want ErrLocked", err)
	}
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Lock(dir)
	if err != nil {
		t.Fatalf("閉じた後の Lock = %v", err)
	}
	_ = second.Close()
	fi, err := os.Lstat(filepath.Join(dir, LockName))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("ロックのファイルの権限 = %v, want 0600", fi.Mode())
	}
}

// ロックのファイルがシンボリックリンクなら開かない。
func TestLockRejectsSymlink(t *testing.T) {
	dir := privateDir(t)
	if err := os.Symlink(filepath.Join(dir, "elsewhere"), filepath.Join(dir, LockName)); err != nil {
		t.Fatal(err)
	}
	if f, err := Lock(dir); err == nil {
		_ = f.Close()
		t.Fatal("シンボリックリンクのロックのファイルを開いた")
	}
}
