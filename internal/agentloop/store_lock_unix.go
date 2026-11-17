//go:build !windows

package agentloop

import (
	"os"
	"path/filepath"
	"syscall"
)

// lockStoreFile 用 flock 串行化同一认领文件的跨进程读改写（过期清理段的 stat/remove/create
// 不是原子操作，并发清理会双重认领）。阻塞等待；进程退出时内核自动释放，不会留下死锁。
func lockStoreFile(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path+".flock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX)
		if err != syscall.EINTR {
			break
		}
	}
	if err != nil {
		file.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		file.Close()
	}, nil
}