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

// browserOpenCommand keeps platform launch semantics testable on every CI OS.
func browserOpenCommand(goos, target string) (string, []string, bool) {
	switch goos {
	case "windows":
		return "rundll32.exe", []string{"url.dll,FileProtocolHandler", target}, true
	case "darwin":
		return "open", []string{target}, true
	case "linux":
		return "xdg-open", []string{target}, true
	default:
		return "", nil, false
	}
}

// openBrowser starts the platform URL handler without waiting for the browser.
// Unsupported platforms intentionally return nil without launching anything.
func openBrowser(target string) error {
	command, args, supported := browserOpenCommand(runtime.GOOS, target)
	if !supported {
		return nil
	}
	if err := exec.Command(command, args...).Start(); err != nil {
		return fmt.Errorf("launch browser: %w", err)
	}
	return nil
}
