//go:build !windows && !linux && !darwin

package main

import "errors"

func readDiskSpace(path string) (diskSpace, error) {
	return diskSpace{}, errors.New("disk space inspection is unavailable on this platform")
}
