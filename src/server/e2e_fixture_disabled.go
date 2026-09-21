//go:build !e2e

package main

func configureE2EFixture(*server) bool { return false }
