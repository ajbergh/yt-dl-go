package main

import "golang.org/x/sys/windows"

func readDiskSpace(path string) (diskSpace, error) {
	target, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return diskSpace{}, err
	}
	var available, total, free uint64
	err = windows.GetDiskFreeSpaceEx(target, &available, &total, &free)
	return diskSpace{Available: available, Total: total}, err
}
