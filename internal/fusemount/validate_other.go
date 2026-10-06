//go:build !windows

package fusemount

import (
	"path"
	"path/filepath"
	"strings"

	"litepan/internal/domain"
)

func NormalizeMountPoint(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", domain.Errorf(domain.CodeValidation, "挂载点不能为空")
	}
	if !filepath.IsAbs(raw) {
		return "", domain.Errorf(domain.CodeValidation, "挂载点必须是绝对路径")
	}
	clean := path.Clean(raw)
	root := path.Clean(MountRoot)
	if clean != root && !strings.HasPrefix(clean, root+"/") {
		return "", domain.Errorf(domain.CodeValidation, "挂载点必须在 %s 目录下", root)
	}
	return clean, nil
}

func isNestedMountPoint(a, b string) bool {
	a = path.Clean(a)
	b = path.Clean(b)
	if a == b {
		return true
	}
	return strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}
