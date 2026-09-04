package tools

import (
	"fmt"
	"unicode/utf8"
)

// byteWindow is one-based so the first byte is offset 1. A returned nextOffset
// can be passed back unchanged; offsets inside a UTF-8 encoding are refused.
type byteWindow struct {
	Content    string
	Offset     int
	Bytes      int
	TotalBytes int
	More       bool
	NextOffset int
}

func windowUTF8(text string, offset, limit int) (byteWindow, error) {
	if offset < 1 {
		return byteWindow{}, fmt.Errorf("offset must be at least 1")
	}
	if limit < 1 {
		return byteWindow{}, fmt.Errorf("limit must be positive")
	}
	if !utf8.ValidString(text) {
		return byteWindow{}, fmt.Errorf("text is not valid UTF-8")
	}

	data := []byte(text)
	if offset > len(data)+1 {
		return byteWindow{}, fmt.Errorf("offset %d exceeds text byte length %d", offset, len(data))
	}
	start := offset - 1
	if start < len(data) && !utf8.RuneStart(data[start]) {
		return byteWindow{}, fmt.Errorf("offset %d is inside a multi-byte UTF-8 rune", offset)
	}
	end := min(len(data), start+limit)
	for end > start && end < len(data) && !utf8.RuneStart(data[end]) {
		end--
	}
	if end == start && start < len(data) {
		_, width := utf8.DecodeRune(data[start:])
		return byteWindow{}, fmt.Errorf("limit %d is too small for the %d-byte UTF-8 rune at offset %d", limit, width, offset)
	}

	window := byteWindow{
		Content:    string(data[start:end]),
		Offset:     offset,
		Bytes:      end - start,
		TotalBytes: len(data),
		More:       end < len(data),
	}
	if window.More {
		window.NextOffset = end + 1
	}
	return window, nil
}

func byteWindowHeader(window byteWindow) string {
	header := fmt.Sprintf("[byte window: offset=%d bytes=%d total=%d more=%t", window.Offset, window.Bytes, window.TotalBytes, window.More)
	if window.More {
		header += fmt.Sprintf(" next_offset=%d", window.NextOffset)
	}
	return header + "]"
}
