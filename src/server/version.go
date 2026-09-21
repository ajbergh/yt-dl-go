package main

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	buildVersion = "dev"
	buildCommit  = "unknown"
	buildDate    = "unknown"
)

type buildInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

func currentBuildInfo() buildInfo {
	return buildInfo{
		Version: strings.TrimSpace(buildVersion),
		Commit:  strings.TrimSpace(buildCommit),
		Date:    strings.TrimSpace(buildDate),
	}
}

var semverPattern = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$`)

type semanticVersion struct {
	major, minor, patch int
	prerelease          string
}

func parseSemanticVersion(value string) (semanticVersion, bool) {
	match := semverPattern.FindStringSubmatch(strings.TrimSpace(value))
	if match == nil {
		return semanticVersion{}, false
	}
	major, err1 := strconv.Atoi(match[1])
	minor, err2 := strconv.Atoi(match[2])
	patch, err3 := strconv.Atoi(match[3])
	if err1 != nil || err2 != nil || err3 != nil {
		return semanticVersion{}, false
	}
	return semanticVersion{major: major, minor: minor, patch: patch, prerelease: match[4]}, true
}

func compareSemanticVersions(left, right string) (int, bool) {
	a, okA := parseSemanticVersion(left)
	b, okB := parseSemanticVersion(right)
	if !okA || !okB {
		return 0, false
	}
	if a.major != b.major {
		if a.major < b.major { return -1, true }
		return 1, true
	}
	if a.minor != b.minor {
		if a.minor < b.minor { return -1, true }
		return 1, true
	}
	if a.patch != b.patch {
		if a.patch < b.patch { return -1, true }
		return 1, true
	}
	if a.prerelease == b.prerelease {
		return 0, true
	}
	if a.prerelease == "" {
		return 1, true
	}
	if b.prerelease == "" {
		return -1, true
	}
	if a.prerelease < b.prerelease {
		return -1, true
	}
	return 1, true
}
