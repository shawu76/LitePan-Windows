// Package safego 为后台常驻任务提供崩溃兜底。
//
// Go 的规则是：任何一个后台任务里出现意料之外的崩溃（panic），整个进程都会退出，
// 即使那个任务只是"每 12 小时清理一次旧日志"这种小事。没有兜底时，一处局部故障
// 会直接表现为整个服务下线，而且堆栈只打到标准错误，不进应用日志，事后无从排查。
//
// Guard 把这类故障限制在局部：记录一条带堆栈的错误日志后返回，让调用方继续下一轮。
//
// 这是止损，不是掩盖问题——日志里必须带组件名与完整堆栈，否则等于把 bug 藏起来。
// 兜住之后那个功能本次会静默失败，因此这条日志需要被当作真实缺陷去看待。
package safego

import (
	"log/slog"
	"runtime/debug"
)

// Guard 执行 fn，并兜住 fn 内部意料之外的崩溃。
//
// 返回值表示 fn 是否正常执行完毕；返回 false 表示已经兜住一次崩溃并记录过日志。
// log 为 nil 时退回 slog.Default()，避免调用方漏传导致故障记录整个丢失。
//
// 用法是"兜住一轮"，不是"兜住整个循环"：把 Guard 放在循环体内部每轮调用，
// 一轮出问题只跳过那一轮；若包在整个循环外面，一个反复出错的轮次会立刻退化成
// 反复重启的刷屏循环。
func Guard(log *slog.Logger, component string, fn func()) (completed bool) {
	if log == nil {
		log = slog.Default()
	}
	defer func() {
		if r := recover(); r != nil {
			completed = false
			log.Error("后台任务异常崩溃，已拦截并跳过本次（服务未中断）",
				"component", component,
				"panic", r,
				"stack", string(debug.Stack()),
			)
		}
	}()
	fn()
	return true
}
