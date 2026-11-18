//go:build !unix

package extmgr

import "os"

// lockWorkDir は、dir を作る。unix 以外では flock が無いので、排他しない（workdir_unix.go の doc）。
func lockWorkDir(dir string) (func(), error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	return func() {}, nil
}
