//go:build unix

package service

import "syscall"

// diskSpace returns the free and total bytes of the file system of path.
func diskSpace(path string) (free, total int64, ok bool) {
	var st syscall.Statfs_t
	if path == "" || syscall.Statfs(path, &st) != nil {
		return 0, 0, false
	}
	return int64(st.Bavail) * int64(st.Bsize), int64(st.Blocks) * int64(st.Bsize), true
}
