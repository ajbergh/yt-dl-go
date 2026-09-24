//go:build windows

package main

import (
	"os"
	"testing"
)

func TestCheckPrivateDataDirACLForCurrentUser(t *testing.T) {
	root, err := os.MkdirTemp("", "yt-dl-go-private-acl-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	prepareTestDataDir(t, root)
	if err := checkPrivateDataDirACL(root); err != nil {
		t.Fatalf("new per-user data directory ACL rejected: %v", err)
	}
}

func TestCheckPrivateDataDirACLRejectsEveryoneGrant(t *testing.T) {
	root, err := os.MkdirTemp("", "yt-dl-go-shared-acl-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	prepareTestDataDir(t, root)
	grantTestEveryoneAccess(t, root)
	if err := checkPrivateDataDirACL(root); err == nil {
		t.Fatal("DATA_DIR ACL with an Everyone grant was accepted")
	}
}
