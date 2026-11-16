package exthost

import (
	"math"
	"syscall"
	"unsafe"
)

// rlimit64 は、prlimit64 のシステムコールが読み書きする struct rlimit64 である。
// syscall.Rlimit はアーキテクチャによって欄の型が違うので、カーネルの型をそのまま写す。
type rlimit64 struct {
	Cur uint64
	Max uint64
}

// memoryLimitBytes は、[Config.MemoryLimitMB]（MiB）をバイト数にする。0 以下は 0（上限なし）を返す。
// バイト数が rlimit で表せない大きさになるときも、上限なしとして 0 を返す。
func memoryLimitBytes(mb int64) uint64 {
	if mb <= 0 || mb > math.MaxInt64>>20 {
		return 0
	}
	return uint64(mb) << 20
}

// limitAddressSpace は、プロセス pid の RLIMIT_AS（仮想アドレス空間の大きさの上限）を limit バイトにする（FR-20）。
// ソフトとハードの両方に掛け、拡張機能が自分で上限を上げられないようにする。
// pid のハードの上限が既に limit より小さいとき（本体が上限の下で動いているとき）は、ハードの上限を超えない値にする。
// 権限の要らない操作だけにするためである（ハードの上限を上げるには CAP_SYS_RESOURCE が要る）。
//
// cgo を使わない（NFR-02）ので、prlimit64 のシステムコールを syscall.Syscall6 で直接呼ぶ。
//
// req: FR-20
func limitAddressSpace(pid int, limit uint64) error {
	var cur rlimit64
	if err := prlimit(pid, syscall.RLIMIT_AS, nil, &cur); err != nil {
		return err
	}
	// RLIM_INFINITY は uint64 の最大値なので、min でハードの上限が無いときも正しく選べる。
	limit = min(limit, cur.Max)
	lim := rlimit64{Cur: limit, Max: limit}
	return prlimit(pid, syscall.RLIMIT_AS, &lim, nil)
}

// prlimit は、prlimit64(2) を呼ぶ。newLimit が nil なら変えずに、old が nil でなければ今の値を読む。
func prlimit(pid, resource int, newLimit, old *rlimit64) error {
	// unsafe.Pointer は、カーネルへ構造体の番地を渡すためだけに使う。uintptr への変換は呼び出しの引数の中で行う（unsafe の規則 4）。
	_, _, errno := syscall.Syscall6(syscall.SYS_PRLIMIT64, uintptr(pid), uintptr(resource),
		uintptr(unsafe.Pointer(newLimit)), uintptr(unsafe.Pointer(old)), 0, 0) //nolint:gosec // 上の理由で、prlimit64 の引数にだけ使う。
	if errno != 0 {
		return errno
	}
	return nil
}
