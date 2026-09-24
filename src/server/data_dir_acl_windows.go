//go:build windows

package main

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	accessAllowedCompoundACEType       = 4
	accessAllowedACEType               = 0
	accessAllowedObjectACEType         = 5
	accessAllowedCallbackACEType       = 9
	accessAllowedCallbackObjectACEType = 11
	objectTypePresent                  = 0x1
	inheritedObjectTypePresent         = 0x2
)

func checkPrivateDataDirACL(path string) error {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("read directory security descriptor: %w", err)
	}
	if sd == nil {
		return fmt.Errorf("directory has no security descriptor")
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return fmt.Errorf("read directory owner: %w", err)
	}
	if owner == nil {
		return fmt.Errorf("directory has no owner")
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil { // A missing or null DACL allows unrestricted access.
		return fmt.Errorf("directory has a missing or permissive DACL")
	}
	tokenUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("read current Windows user SID: %w", err)
	}
	if tokenUser == nil || tokenUser.User.Sid == nil {
		return fmt.Errorf("current Windows user SID is unavailable")
	}
	var sharedGrants []string
	for i := uint16(0); i < dacl.AceCount; i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(i), &ace); err != nil {
			return fmt.Errorf("read directory ACL entry: %w", err)
		}
		if ace == nil || ace.Mask == 0 {
			continue
		}
		sidOffset, recognized := allowedACESIDOffset(ace)
		if !recognized {
			continue
		}
		if uint32(sidOffset)+8 > uint32(ace.Header.AceSize) {
			return fmt.Errorf("directory ACL contains a malformed access rule")
		}
		sid := (*windows.SID)(unsafe.Add(unsafe.Pointer(ace), sidOffset))
		sidLength := uint32(8) + uint32(*(*uint8)(unsafe.Add(unsafe.Pointer(sid), 1)))*4
		if uint32(sidOffset)+sidLength > uint32(ace.Header.AceSize) {
			return fmt.Errorf("directory ACL contains a malformed access rule")
		}
		sharedGrants = append(sharedGrants, sid.String())
	}
	return validatePrivateDataDirACL(owner.String(), tokenUser.User.Sid.String(), true, sharedGrants)
}

func allowedACESIDOffset(ace *windows.ACCESS_ALLOWED_ACE) (uintptr, bool) {
	switch ace.Header.AceType {
	case accessAllowedACEType, accessAllowedCallbackACEType:
		return unsafe.Offsetof(ace.SidStart), true
	case accessAllowedCompoundACEType:
		return unsafe.Offsetof(ace.SidStart) + 4, true
	case accessAllowedObjectACEType, accessAllowedCallbackObjectACEType:
		flags := *(*uint32)(unsafe.Add(unsafe.Pointer(ace), unsafe.Offsetof(ace.SidStart)))
		offset := unsafe.Offsetof(ace.SidStart) + unsafe.Sizeof(uint32(0))
		if flags&objectTypePresent != 0 {
			offset += 16
		}
		if flags&inheritedObjectTypePresent != 0 {
			offset += 16
		}
		return offset, true
	default:
		return 0, false
	}
}
