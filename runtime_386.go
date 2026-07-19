//go:build linux && 386

// 仅在 linux/386 平台编译。
//
// Go 运行时在 386 架构上的异步抢占（async preemption）机制存在已知缺陷：
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
// 此文件通过 //go:build linux && 386 约束，仅在 linux/386 目标编译，
// 不影响 amd64、arm64 等其他平台。

package main

import "os"

func init() {
	// 仅当 GODEBUG 环境变量未设置时才注入默认值，
	// 避免覆盖用户显式设置的 GODEBUG。
	if v := os.Getenv("GODEBUG"); v == "" {
		os.Setenv("GODEBUG", "asyncpreemptoff=1")
	}
}
