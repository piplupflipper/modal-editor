//go:build unix

package extmgr

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// lockWorkDir は、dir を作り、dir/lock のファイルに排他の flock を掛ける。
// 別のプロセス（別の server）が掛けていれば、待たずに ErrBusy を返す。
// 返した関数でロックを外す。ロックはプロセスが終わればカーネルが外すので、強制終了で残らない。
func lockWorkDir(dir string) (func(), error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("作業のディレクトリのロックを開けない: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrBusy
		}
		return nil, fmt.Errorf("作業のディレクトリをロックできない: %w", err)
	}
	// 閉じる前に明示して外す。fork した直後で exec の前の子プロセス（他の goroutine が起動した git など）は、
	// close-on-exec のファイル記述子も一時的に共有するので、閉じるだけでは、その間はロックが残る。
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
