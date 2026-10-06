//go:build windows

package fusemount

import (
	"path/filepath"
	"strings"

	"litepan/internal/domain"
)

// isDriveRootPath 判断路径是否为盘符根（如 Z:\、Z:/、Z:）。
func isDriveRootPath(p string) bool {
	// 只有 "X:"（2位）或 "X:\" / "X:/"（3位）才是盘符根；
	// "X:\Users\..." 这类完整路径不能误判为盘符根。
	if len(p) == 2 && p[1] == ':' {
		return true
	}
	if len(p) != 3 || p[1] != ':' {
		return false
	}
	return p[2] == '\\' || p[2] == '/'
}

// NormalizeMountPoint 在 Windows 上允许两种挂载点：
//  1. 盘符根（Z: / Z:\ / z:/），归一化为 "Z:\"，可直接映射为本地盘符；
//  2. MountRoot 下的绝对目录（用于 WinFsp 目录挂载）。
func NormalizeMountPoint(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", domain.Errorf(domain.CodeValidation, "挂载点不能为空")
	}
	if isDriveRootPath(raw) {
		return strings.ToUpper(raw[:1]) + `:\`, nil
	}
	if !filepath.IsAbs(raw) {
		return "", domain.Errorf(domain.CodeValidation, "挂载点必须是绝对路径或盘符（如 Z:）")
	}
	clean := filepath.Clean(raw)
	root := filepath.Clean(MountRoot)
	if !strings.EqualFold(clean, root) && !strings.HasPrefix(clean, root+string(filepath.Separator)) {
		return "", domain.Errorf(domain.CodeValidation, "挂载点必须在 %s 目录下，或使用未占用的盘符（如 Z:）", root)
	}
	return clean, nil
}

// isNestedMountPoint 大小写不敏感地比较 Windows 路径嵌套关系。
func isNestedMountPoint(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if strings.EqualFold(a, b) {
		return true
	}
	sep := string(filepath.Separator)
	return strings.HasPrefix(strings.ToLower(a), strings.ToLower(b)+sep) ||
		strings.HasPrefix(strings.ToLower(b), strings.ToLower(a)+sep)
}
