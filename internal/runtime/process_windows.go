//go:build windows

package runtime

import (
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var processJobs sync.Map

func configureProcessTree(cmd *exec.Cmd) {
	cmd.WaitDelay = 5 * time.Second
	cmd.Cancel = func() error {
		if value, ok := processJobs.Load(cmd); ok {
			return windows.TerminateJobObject(value.(windows.Handle), 1)
		}
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		return exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
	}
}

// assignProcessTree 在 Start 成功后立即把进程放入作业对象：TerminateJobObject 作用于
// 句柄而非 PID，不会像 taskkill 那样因 PID 复用误杀无关进程树。须紧随 Start 调用，
// 缩小子进程在入组前逃逸的窗口；入组失败时 Cancel 退回 taskkill 兜底。
func assignProcessTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return
	}
	handle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		windows.CloseHandle(job)
		return
	}
	err = windows.AssignProcessToJobObject(job, handle)
	windows.CloseHandle(handle)
	if err != nil {
		windows.CloseHandle(job)
		return
	}
	processJobs.Store(cmd, job)
}

// releaseProcessTree 在 Wait 后关闭作业句柄，KILL_ON_JOB_CLOSE 清理仍存活的成员进程。
func releaseProcessTree(cmd *exec.Cmd) {
	if value, ok := processJobs.LoadAndDelete(cmd); ok {
		windows.CloseHandle(value.(windows.Handle))
	}
}