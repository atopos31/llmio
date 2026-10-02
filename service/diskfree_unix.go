//go:build !windows

package service

import "syscall"

// diskFree 返回 dir 所在文件系统的可用字节数。见 diskfree_windows.go 的说明。
func diskFree(dir string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}
