//go:build windows

package fallocate

// Windows has no direct fallocate(2) equivalent.
// This path is only reached via FUSE ALLOCATE operations, which are
// unavailable on Windows; a no-op preserves correctness (space is
// allocated lazily by the filesystem instead of eagerly).
func fallocate(fd int, mode uint32, off int64, len int64) error {
	_ = fd
	_ = mode
	_ = off
	_ = len
	return nil
}
