//go:build !windows

package api

func isVolumeRoot(path string) bool { return false }

func listVolumeRoots() []localDirEntry { return nil }
