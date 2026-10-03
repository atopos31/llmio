//go:build windows

package service

import (
	"syscall"
	"unsafe"
)

// diskFree 返回 dir 所在卷的可用字节数。
//
// 单独一个文件是为了不让平台细节渗进迁移逻辑：VACUUM 要 2× 库大小的空闲
// 空间，不预检的话磁盘满会以一句 SQLite 的 I/O 报错收场，而那个报错
// 看不出是"盘满了"还是"库坏了"——这两件事的处置方式天差地别。
//
// 直接用 kernel32 而不是 golang.org/x/sys：这套方案从头到尾只用标准库，
// 为一行"看还剩多少盘"引入一个新依赖不划算。
var procGetDiskFreeSpaceEx = syscall.NewLazyDLL("kernel32.dll").
	NewProc("GetDiskFreeSpaceExW")

func diskFree(dir string) (int64, error) {
	p, err := syscall.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	var free, total, avail uint64
	r, _, callErr := procGetDiskFreeSpaceEx.Call(
		uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&free)),
		uintptr(unsafe.Pointer(&total)),
		uintptr(unsafe.Pointer(&avail)),
	)
	if r == 0 {
		return 0, callErr
	}
	return int64(free), nil
}
