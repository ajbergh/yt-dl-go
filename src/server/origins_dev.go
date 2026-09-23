//go:build dev

package main

// Development builds also trust the local Vite server.
func defaultDevOrigins() []string {
	return []string{"http://localhost:5173", "http://127.0.0.1:5173"}
}
