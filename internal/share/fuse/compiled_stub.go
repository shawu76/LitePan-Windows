//go:build !fuse && !windows

package fuse

func Compiled() bool { return false }
