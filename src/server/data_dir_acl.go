package main

import (
	"errors"
	"strings"
)

var sharedDataDirSIDs = map[string]struct{}{
	"S-1-1-0":      {}, // Everyone
	"S-1-5-2":      {}, // Network
	"S-1-5-4":      {}, // Interactive
	"S-1-5-7":      {}, // Anonymous Logon
	"S-1-5-11":     {}, // Authenticated Users
	"S-1-5-13":     {}, // Terminal Server User
	"S-1-5-14":     {}, // Remote Interactive Logon
	"S-1-5-15":     {}, // This Organization
	"S-1-5-32-545": {}, // Builtin Users
	"S-1-5-32-546": {}, // Builtin Guests
	"S-1-5-32-554": {}, // Pre-Windows 2000 Compatible Access
	"S-1-5-32-555": {}, // Remote Desktop Users
	"S-1-15-2-1":   {}, // All Application Packages
	"S-1-15-2-2":   {}, // All Restricted Application Packages
}

func validatePrivateDataDirACL(ownerSID, currentUserSID string, daclPresent bool, allowedSIDs []string) error {
	if !daclPresent {
		return errors.New("directory has a missing or permissive DACL")
	}
	if ownerSID == "" || currentUserSID == "" || !strings.EqualFold(ownerSID, currentUserSID) {
		return errors.New("directory owner is not the current user")
	}
	for _, sid := range allowedSIDs {
		if _, shared := sharedDataDirSIDs[sid]; shared {
			return errors.New("directory grants access to a shared Windows user group")
		}
	}
	return nil
}
