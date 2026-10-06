//go:build windows

package fusemount

import (
	"os"
	"path/filepath"
)

// forceReleaseMountPoint Windows 下无需系统级强制卸载：
// WinFsp 挂载由 cgofuse host.Unmount() 优雅卸载，这里保持 no-op。
func forceReleaseMountPoint(mountPoint string) error {
	_ = mountPoint
	return nil
}

func reclaimMountPoint(mountPoint string) error {
	return forceReleaseMountPoint(mountPoint)
}

// ensureMountPointDir 为挂载点准备目录；盘符根（Z:\）无需也不应 MkdirAll。
//
// WinFsp 目录挂载的特殊要求：挂载点目录必须【不存在】——WinFsp 会在挂载时
// 自动创建该目录，卸载时自动删除；若目录已存在（即使是空目录），WinFsp 会
// 拒绝挂载并报 "mount point in use"。因此这里绝不能预创建目录。
func ensureMountPointDir(mountPoint string) error {
	return nil
}

// removeMountPointDir 删除挂载点目录；盘符根跳过，避免误删盘根。
func removeMountPointDir(mountPoint string) error {
	if isDriveRootPath(mountPoint) {
		return nil
	}
	mountPoint = filepath.Clean(mountPoint)
	if err := os.Remove(mountPoint); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return nil
}
