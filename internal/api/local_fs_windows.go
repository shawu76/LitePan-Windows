//go:build windows

package api

import "os"

// isVolumeRoot 报告 path 是否为 Windows 盘符根（如 C:\ 或 C:/）。
func isVolumeRoot(path string) bool {
	if len(path) != 3 {
		return false
	}
	c := path[0]
	if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')) {
		return false
	}
	if path[1] != ':' {
		return false
	}
	return path[2] == '\\' || path[2] == '/'
}

// listVolumeRoots 枚举当前系统上存在的盘符根目录。
func listVolumeRoots() []localDirEntry {
	var roots []localDirEntry
	for d := 'A'; d <= 'Z'; d++ {
		p := string(d) + ":\\"
		if _, err := os.Stat(p); err == nil {
			roots = append(roots, localDirEntry{Name: p, Path: p})
		}
	}
	return roots
}
