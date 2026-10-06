//go:build windows && !fuse

package fuse

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"time"

	cgofs "github.com/winfsp/cgofuse/fuse"

	"litepan/internal/domain"
)

// liveWinManager 是基于 WinFsp + cgofuse 的 Windows 挂载管理器。
// Windows 无 FUSE 内核，无法使用 go-fuse；此处通过 cgofuse 的 WinFsp 后端实现文件系统挂载。
type liveWinManager struct {
	deps Deps
	mu   sync.Mutex
	runs map[int64]*runningWinMount
}

type runningWinMount struct {
	host *cgofs.FileSystemHost
	fsys *winFuseFS
}

func NewManager(deps Deps) Manager {
	return &liveWinManager{
		deps: deps,
		runs: make(map[int64]*runningWinMount),
	}
}

func (m *liveWinManager) Mount(_ context.Context, mount *domain.FuseMount) error {
	if mount == nil {
		return domain.Errorf(domain.CodeValidation, "挂载配置无效")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.runs[mount.ID]; ok {
		return nil
	}
	fsys := newWinFuseFS(m.deps, mount)
	host := cgofs.NewFileSystemHost(fsys)
	// WinFsp 挂载选项：允许其他进程（资源管理器等）访问。
	opts := []string{"-o", "uid=-1", "-o", "gid=-1"}
	// cgofuse 的 host.Mount 在 WinFsp 后端会阻塞于 FUSE 主循环（直到 Unmount），
	// 必须在 goroutine 中执行，并通过 Init 回调 / 返回结果确认挂载成败。
	go func() {
		ok := host.Mount(toWinfspMountPoint(mount.MountPoint), opts)
		fsys.mountDone <- ok
	}()
	select {
	case <-fsys.mountReady:
		// WinFsp 已成功创建挂载点（Init 回调）。
		m.runs[mount.ID] = &runningWinMount{host: host, fsys: fsys}
		return nil
	case ok := <-fsys.mountDone:
		// host.Mount 提前返回：挂载失败（WinFsp 未就绪 / 挂载点被占用等）。
		if !ok {
			return domain.Errorf(domain.CodeInternal, "WinFsp 挂载失败 (%s): 请确认已安装 WinFsp 且挂载点未被占用", mount.MountPoint)
		}
		m.runs[mount.ID] = &runningWinMount{host: host, fsys: fsys}
		return nil
	case <-time.After(10 * time.Second):
		// 超时未就绪：主动卸载并报错，避免挂载请求永久挂起。
		host.Unmount()
		return domain.Errorf(domain.CodeInternal, "WinFsp 挂载超时 (%s): 请确认 WinFsp 服务正常运行", mount.MountPoint)
	}
}

func (m *liveWinManager) Unmount(_ context.Context, id int64) error {
	m.mu.Lock()
	run, ok := m.runs[id]
	if ok {
		delete(m.runs, id)
	}
	m.mu.Unlock()
	if !ok {
		return nil
	}
	if run.host == nil {
		return nil
	}
	run.host.Unmount()
	return nil
}

// toWinfspMountPoint 将归一化后的挂载点转换为 WinFsp 接受的格式：
//   - 盘符根（"Z:\" 或 "Z:/"）必须去掉尾部反斜杠，并加 \\.\ 前缀传 "\\?\Z:"，
//     使 WinFsp 走 FspMountSet_MountmgrDrive（内核 Mount Manager 全局注册），
//     盘符在提升与非提升进程间均可见；若传 "Z:" 则走 DefineDosDeviceW，
//     受会话/进程提升级别（IL）隔离，非提升的资源管理器不可见；
//   - 目录挂载点保持 filepath.Clean 后的反斜杠绝对路径（目录由 WinFsp 创建，必须不存在）。
func toWinfspMountPoint(mp string) string {
	mp = filepath.Clean(mp)
	if len(mp) == 3 && mp[1] == ':' && mp[2] == '\\' {
		return `\\?\` + strings.ToUpper(mp[:1]) + ":"
	}
	return mp
}
