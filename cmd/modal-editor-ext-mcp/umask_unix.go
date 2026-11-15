//go:build unix

package main

import "syscall"

// withPrivateUmask は、umask を 077 にして f を呼び、元に戻す。
// net.Listen が作るソケットのファイルを、作った時点から所有者だけが読み書きできるようにする（listenSocket）。
// 後から chmod すると、それまでの間に他の利用者が接続できる窓が残るためである。
// umask はプロセス全体の値なので、その間に他の goroutine がファイルを作ると、その権限も絞られる。
// この拡張機能でファイルを作るのは listenSocket だけである。
func withPrivateUmask(f func()) {
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)
	f()
}
