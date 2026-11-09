//go:build linux || darwin

package main

import "golang.org/x/sys/unix"

func readDiskSpace(path string) (diskSpace, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return diskSpace{}, err
	}
	return diskSpace{Available: uint64(stat.Bavail) * uint64(stat.Bsize), Total: uint64(stat.Blocks) * uint64(stat.Bsize)}, nil
}
