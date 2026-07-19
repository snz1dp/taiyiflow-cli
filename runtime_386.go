//go:build linux && 386

// 仅在 linux/386 平台编译。
//
// Go 运行时在 386 架构上存在两个已知问题，需要通过构建配置和运行时参数共同规避：
//
// 问题一：异步抢占（async preemption）信号处理缺陷
//
// 信号处理路径 sigtrampgo → sigaddset 中，信号编号可能越界
//（runtime.goPanicIndex 触发 "index out of range"），
// 继而引发 "morestack on g0" 和 "panic during panic" 的级联崩溃。
//
// 根因：runtime/os_linux_generic.go 的 sigaddset 使用固定大小的
// sigset 数组（[_NSIG/32]uint32，386 上长度为 2），当收到异常信号编号时
// 会越界。这在 386 的异步抢占信号（SIGURG）处理路径上尤为明显。
//
// 修复方式：在 init() 中通过 os.Setenv("GODEBUG", "asyncpreemptoff=1")
// 动态禁用信号抢占。Go 运行时在 os.Setenv("GODEBUG", ...) 时会通过
// syscall.runtimeSetenv → godebugNotify → reparsedebugvars 链路
// 热更新 debug.asyncpreemptoff 变量（参见 runtime/runtime.go:202）。
// 该变量在每次 preemptM 调用前动态检查（参见 runtime/preempt.go:223），
// 因此 init() 中设置即可生效。
//
// 问题二：bad g in signal handler（信号到达非 Go 线程时崩溃）
//
// 在 CGO_ENABLED=0 的非 CGO 构建中，Go 运行时不会创建 extra M
//（cgoHasExtraM = false）。当信号到达一个尚未完成 TLS 初始化的线程时，
// sigFetchG() 通过 TLS 段寄存器（386 上为 GS 段）读取 g 指针，返回 nil。
// sigtrampgo 检测到 gp == nil 后调用 badsignal()，而 badsignal 在
// !iscgo && !cgoHasExtraM 条件下直接执行 exit(2) 崩溃退出
//（参见 runtime/signal_unix.go:1134-1140）。
//
// 信号来源包括：
//   - sigPerThreadSyscall（SIGRTMIN+1 = 33）：Go 运行时在
//     doAllThreadsSyscall 中向所有线程发送此信号（参见
//     runtime/os_linux.go:852），用于 per-thread 系统调用传播。
//   - 同步信号（SIGSEGV、SIGBUS 等）：内存访问异常。
//   - 外部信号：如 kill 命令。
//
// 在 386 架构上，TLS 使用段寄存器（GS 段）实现，初始化过程比 64 位更复杂
//（涉及 setldt 系统调用），线程在 TLS 初始化完成前收到信号的概率更高。
//
// 修复方式：在 Makefile 中为 linux/386 目标单独设置 CGO_ENABLED=1
// 和 CC=i686-linux-gnu-gcc。启用 CGO 后，runtime/cgo 包通过 iscgo.go
// 将 runtime.iscgo 设为 true（参见 runtime/cgo/iscgo.go:17），
// mstartm0() 中检测到 iscgo == true 后创建 extra M 并设置
// cgoHasExtraM = true（参见 runtime/proc.go:1948-1950）。
// 这样 badsignal 会走 needm() 正常处理路径而非直接 exit(2)。
//
// 问题三：禁用异步抢占后的 GC STW 死锁风险
//
// 禁用异步抢占后，GC Stop-The-World (STW) 无法通过信号强制
// 停止处于非协作状态（无函数调用的紧凑循环）的 Goroutine。多 P 并发时，
// 若某个 P 上的 Goroutine 不 yield，GC 将无法完成 STW，导致死锁或调度饥饿。
// 因此必须同时设置 GOMAXPROCS=1，将调度收敛到单 P，消除跨 P 同步需求。
// 注意：GOMAXPROCS 环境变量在运行时初始化阶段（init() 之前）读取，
// 在 init() 中通过 os.Setenv 设置不会生效，必须直接调用
// runtime.GOMAXPROCS(1)。
//
// 此文件通过 //go:build linux && 386 约束，仅在 linux/386 目标编译，
// 不影响 amd64、arm64 等其他平台。

package main

import (
	"os"
	"runtime"
)

func init() {
	// 仅当 GODEBUG 环境变量未设置时才注入默认值，
	// 避免覆盖用户显式设置的 GODEBUG。
	if v := os.Getenv("GODEBUG"); v == "" {
		os.Setenv("GODEBUG", "asyncpreemptoff=1")
	}

	// 禁用异步抢占后，必须限制 GOMAXPROCS=1 以规避 GC STW 死锁风险。
	// 即使用户显式设置了 GOMAXPROCS 环境变量，在 asyncpreemptoff=1 下
	// 多 P 并发也不安全，因此强制覆盖为 1。
	runtime.GOMAXPROCS(1)
}
