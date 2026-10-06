package fusemount

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	KeyMountRoot         = "fuse_mount_root"
	KeyEnabled           = "fuse_enabled"
	DefaultEntryTimeoutS = 30
	DefaultAttrTimeoutS  = 3
)

// MountRoot 是 FUSE 挂载根目录。
// 取值优先级：界面设置（ApplyConfiguredMountRoot 启动时注入）> LITEPAN_MOUNT_ROOT > 平台默认值。
// 修改后需重启程序生效。
var MountRoot = resolveMountRootFromEnv()

func resolveMountRootFromEnv() string {
	if v := strings.TrimSpace(os.Getenv("LITEPAN_MOUNT_ROOT")); v != "" {
		return v
	}
	if runtime.GOOS == "windows" {
		if local := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); local != "" {
			return filepath.Join(local, "LitePan", "mounts")
		}
		return `C:\litepan\mounts`
	}
	return "/app/mounts"
}
