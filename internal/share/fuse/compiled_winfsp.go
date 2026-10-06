//go:build windows && !fuse

package fuse

func Compiled() bool { return true }
