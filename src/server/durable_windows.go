//go:build windows

package main

import (
	"os"
	"syscall"
	"unsafe"
)

var moveFileExW = syscall.NewLazyDLL("kernel32.dll").NewProc("MoveFileExW")

const moveFileWriteThrough = 0x00000008

// durableRename requests write-through semantics from Windows when committing
// the directory entry. Windows does not support syncing directory handles.
func durableRename(source, destination string) error {
	from, err := syscall.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := syscall.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	result, _, callErr := moveFileExW.Call(uintptr(unsafe.Pointer(from)), uintptr(unsafe.Pointer(to)), moveFileWriteThrough)
	if result == 0 {
		if callErr == syscall.Errno(80) || callErr == syscall.Errno(183) {
			return &os.LinkError{Op: "rename", Old: source, New: destination, Err: os.ErrExist}
		}
		return callErr
	}
	return nil
}

func durableLink(source, destination string) error {
	return durableRename(source, destination)
}
