//go:build !windows

package ocr

import "fmt"

func Extract(string) (string, error) {
	return "", fmt.Errorf("Windows.Media.Ocr is available only on Windows")
}
