//go:build !linux && !windows

package fusemount

import (
	"os"
	"path/filepath"
)

func reclaimMountPoint(mountPoint string) error {
	return forceReleaseMountPoint(mountPoint)
}

func forceReleaseMountPoint(mountPoint string) error {
	_ = mountPoint
	return nil
}

func ensureMountPointDir(mountPoint string) error {
	return os.MkdirAll(filepath.Clean(mountPoint), 0o755)
}

func removeMountPointDir(mountPoint string) error {
	mountPoint = filepath.Clean(mountPoint)
	if err := os.Remove(mountPoint); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return nil
}
