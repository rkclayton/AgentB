//go:build !windows

package tools

import "os"

func atomicReplace(source, target string) error { return os.Rename(source, target) }
