//go:build !windows

package main

func processIsElevated() bool { return false }
