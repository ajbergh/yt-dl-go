//go:build !dev

package main

import "testing"

func TestReleaseOriginsExcludeVite(t *testing.T) {
	t.Setenv("ADDR", "127.0.0.1:8080")
	t.Setenv("ALLOWED_ORIGINS", "")
	c, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if c.origins["http://localhost:5173"] || c.origins["http://127.0.0.1:5173"] {
		t.Fatal("release build trusts Vite by default")
	}
	if !c.origins["http://127.0.0.1:8080"] || !c.origins["http://localhost:8080"] {
		t.Fatal("release build rejected its own loopback listener")
	}

	t.Setenv("ALLOWED_ORIGINS", "http://localhost:5173")
	c, err = loadConfig()
	if err != nil || !c.origins["http://localhost:5173"] {
		t.Fatal("explicit origin setting was not honored")
	}
}
