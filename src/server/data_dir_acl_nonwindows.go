//go:build !windows

package main

func checkPrivateDataDirACL(string) error { return nil }
