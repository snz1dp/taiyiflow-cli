//go:build windows

package agentloop

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// lockStoreFile 用 LockFileEx 串行化同一认领文件的跨进程读改写（过期清理段的
// stat/remove/create 不是原子操作，并发清理会双重认领）。进程退出时系统自动释放。
func lockStoreFile(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path+".flock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	overlapped := new(windows.Overlapped)
	if err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, overlapped); err != nil {
		file.Close()
		return nil, err
	}
	return func() {
		_ = windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, overlapped)
		file.Close()
	}, nil
}