// browser.go derives the local UI URL and delegates opening it to the
// platform's default browser command.
package main

import (
	"fmt"
	"net"
	"os/exec"
	"runtime"
)

// browserURL converts the configured listener into a browser-reachable URL,
// replacing wildcard bind addresses with loopback for local navigation.
func browserURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://127.0.0.1:8080/"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/"
}

// openBrowser starts the platform URL handler without waiting for the browser.
// Unsupported platforms intentionally return nil without launching anything.
func openBrowser(target string) error {
	var command string
	var args []string
	switch runtime.GOOS {
	case "windows":
		command = "rundll32.exe"
		args = []string{"url.dll,FileProtocolHandler", target}
	case "darwin":
		command = "open"
		args = []string{target}
	case "linux":
		command = "xdg-open"
		args = []string{target}
	default:
		return nil
	}
	if err := exec.Command(command, args...).Start(); err != nil {
		return fmt.Errorf("launch browser: %w", err)
	}
	return nil
}
