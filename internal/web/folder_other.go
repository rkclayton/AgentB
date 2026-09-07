//go:build !windows

package web

import "fmt"

func nativeFolderPicker(string) (string, error) {
	return "", fmt.Errorf("native folder picker is available only on Windows")
}
