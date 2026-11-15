//go:build !unix

package main

// withPrivateUmask は、umask の無い OS では f を呼ぶだけである。
func withPrivateUmask(f func()) { f() }
