//go:build !dev

package main

// Release builds accept the configured listener origin, added by loadConfig.
// Other origins require an explicit ALLOWED_ORIGINS setting.
func defaultDevOrigins() []string { return nil }
