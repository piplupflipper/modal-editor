//go:build !linux

package exthost

import "errors"

// limitAddressSpace は、Linux 以外では上限を掛けられないので、常に誤りを返す（rlimit_linux.go の doc）。
// 本体の対象は Linux だけである（NFR-01）。[Config.MemoryLimitMB] が 0 なら呼ばれない。
func limitAddressSpace(int, uint64) error {
	return errors.New("exthost: RLIMIT_AS is supported only on linux")
}
