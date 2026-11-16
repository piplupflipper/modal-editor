package exthost

import "math"

// memoryLimitBytes は、[Config.MemoryLimitMB]（MiB）をバイト数にする。0 以下は 0（上限なし）を返す。
// バイト数が rlimit で表せない大きさになるときも、上限なしとして 0 を返す。
func memoryLimitBytes(mb int64) uint64 {
	if mb <= 0 || mb > math.MaxInt64>>20 {
		return 0
	}
	return uint64(mb) << 20
}
