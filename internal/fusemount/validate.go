package fusemount

import (
	"fmt"
	"strconv"
	"strings"

	"litepan/internal/domain"
)

func ParseModeOctal(s string, def uint32) (uint32, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return def, nil
	}
	if strings.HasPrefix(s, "0") {
		n, err := strconv.ParseUint(s, 8, 32)
		if err != nil {
			return 0, domain.Errorf(domain.CodeValidation, "无效的八进制权限: %s", s)
		}
		return uint32(n), nil
	}
	n, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0, domain.Errorf(domain.CodeValidation, "无效的权限数值: %s", s)
	}
	return uint32(n), nil
}

func ValidateMount(m *domain.FuseMount, all []*domain.FuseMount, excludeID int64) error {
	if m == nil {
		return domain.Errorf(domain.CodeValidation, "挂载配置无效")
	}
	name := strings.TrimSpace(m.Name)
	if name == "" {
		return domain.Errorf(domain.CodeValidation, "挂载名称不能为空")
	}
	m.Name = name
	if m.AccountID <= 0 {
		return domain.Errorf(domain.CodeValidation, "请选择云存储账号")
	}
	if strings.TrimSpace(m.RootItemID) == "" {
		return domain.Errorf(domain.CodeValidation, "请选择源目录")
	}
	mp, err := NormalizeMountPoint(m.MountPoint)
	if err != nil {
		return err
	}
	m.MountPoint = mp
	if m.DirMode == 0 {
		m.DirMode = 0o755
	}
	if m.FileMode == 0 {
		m.FileMode = 0o644
	}
	for _, other := range all {
		if other.ID == excludeID || other.ID == m.ID {
			continue
		}
		if other.MountPoint == m.MountPoint {
			return domain.Errorf(domain.CodeValidation, "挂载点已被「%s」使用", other.Name)
		}
		if isNestedMountPoint(other.MountPoint, m.MountPoint) {
			return domain.Errorf(domain.CodeValidation, "挂载点不能与「%s」嵌套", other.Name)
		}
	}
	return nil
}

func DefaultMount(m *domain.FuseMount) {
	if m.State == "" {
		m.State = domain.FuseStateUnmounted
	}
	m.ReadOnly = true
	if m.DirMode == 0 {
		m.DirMode = 0o755
	}
	if m.FileMode == 0 {
		m.FileMode = 0o644
	}
}

func FormatMode(mode uint32) string {
	return fmt.Sprintf("%04o", mode&0o7777)
}
