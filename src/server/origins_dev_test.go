//go:build dev

package main

import "testing"

func TestDevelopmentOriginsIncludeVite(t *testing.T) {
	t.Setenv("ADDR", "127.0.0.1:8080")
	t.Setenv("ALLOWED_ORIGINS", "")
	c, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	for _, origin := range []string{"http://localhost:5173", "http://127.0.0.1:5173", "http://127.0.0.1:8080"} {
		if !c.origins[origin] {
			t.Fatalf("development build rejected %s", origin)
		}
	}
}
