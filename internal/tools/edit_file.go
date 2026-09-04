package tools

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"harness/internal/session"
)

type EditFile struct{ coordinator *FileCoordinator }

func NewEditFile(c *FileCoordinator) *EditFile { return &EditFile{coordinator: c} }
func (*EditFile) Name() string                 { return "edit_file" }
func (*EditFile) Description() string {
	return "Replace one exact, unique old_string in path with new_string. Unlike write_file, it avoids reproducing the whole file."
}
func (*EditFile) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}, "old_string": map[string]any{"type": "string"}, "new_string": map[string]any{"type": "string"}}, "required": []string{"path", "old_string", "new_string"}}
}
func (e *EditFile) Call(ctx context.Context, s *session.Session, args map[string]any) (string, error) {
	path, _ := args["path"].(string)
	old, oldOK := args["old_string"].(string)
	replacement, newOK := args["new_string"].(string)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	if !oldOK {
		return "", fmt.Errorf("old_string is required")
	}
	if !newOK {
		return "", fmt.Errorf("new_string is required")
	}
	if old == "" {
		return "", fmt.Errorf("old_string is empty; use write_file to create a file, or give the exact text to replace.")
	}
	resolved, err := resolveForTool(ctx, s.Workspace, path)
	if err != nil {
		return "", err
	}
	prefix, err := e.coordinator.check(s, path, resolved)
	if err != nil {
		return "", err
	}
	fail := func(cause error) (string, error) {
		if prefix != "" {
			return "", fmt.Errorf("%serror: %v", prefix, cause)
		}
		return "", cause
	}
	raw, err := os.ReadFile(resolved)
	if err != nil {
		return fail(err)
	}
	sample := raw
	if len(sample) > 8192 {
		sample = sample[:8192]
	}
	if bytes.IndexByte(sample, 0) >= 0 {
		return fail(fmt.Errorf("binary file refused: %s", cleanRel(path)))
	}
	bom := bytes.HasPrefix(raw, []byte{0xef, 0xbb, 0xbf})
	if bom {
		raw = raw[3:]
	}
	crlf := bytes.Contains(raw, []byte("\r\n"))
	text := normalizeLF(string(raw))
	old = normalizeLF(old)
	replacement = normalizeLF(replacement)
	updated, start, end, note, matched, err := exactTier(text, old, replacement)
	if err != nil {
		return fail(err)
	}
	if !matched {
		updated, start, end, note, matched, err = whitespaceTier(text, old, replacement, false)
		if err != nil {
			return fail(err)
		}
	}
	if !matched {
		updated, start, end, note, matched, err = whitespaceTier(text, old, replacement, true)
		if err != nil {
			return fail(err)
		}
	}
	if !matched {
		if replacement != "" && strings.Count(text, replacement) == 1 {
			line := lineAt(text, strings.Index(text, replacement))
			last := line + strings.Count(replacement, "\n")
			return fail(fmt.Errorf("old_string not found, but new_string already exists at lines %d–%d; this edit may already be applied.", line, last))
		}
		return fail(nearMiss(path, text, old))
	}
	output := updated
	if crlf {
		output = strings.ReplaceAll(output, "\n", "\r\n")
	}
	data := []byte(output)
	if bom {
		data = append([]byte{0xef, 0xbb, 0xbf}, data...)
	}
	temp, err := os.CreateTemp(filepath.Dir(resolved), ".agentb-edit-*")
	if err != nil {
		return fail(err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if _, err = temp.Write(data); err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fail(err)
	}
	if err := atomicReplace(tempPath, resolved); err != nil {
		return fail(err)
	}
	e.coordinator.record(s, resolved)
	if replacement == "" {
		return prefix + fmt.Sprintf("ok: deleted lines %d–%d", start, end), nil
	}
	newLines := strings.Count(replacement, "\n") + 1
	delta := newLines - (end - start + 1)
	result := fmt.Sprintf("ok: replaced lines %d–%d with %d lines (%+d)", start, end, newLines, delta)
	if note != "" {
		result += "; " + note
	}
	return prefix + result + "\n\n" + contextRegion(updated, start, newLines), nil
}

func normalizeLF(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n")
}
func exactTier(text, old, replacement string) (string, int, int, string, bool, error) {
	count := strings.Count(text, old)
	if count > 1 {
		positions := occurrenceLines(text, old, 5)
		return text, 0, 0, "", false, fmt.Errorf("%s", multipleError(count, positions))
	}
	if count == 1 {
		index := strings.Index(text, old)
		start := lineAt(text, index)
		return strings.Replace(text, old, replacement, 1), start, start + strings.Count(old, "\n"), "", true, nil
	}
	return text, 0, 0, "", false, nil
}

func whitespaceTier(text, old, replacement string, indent bool) (string, int, int, string, bool, error) {
	fileLines := strings.Split(text, "\n")
	oldLines := strings.Split(old, "\n")
	matches := []int{}
	fileIndents := map[int]string{}
	oldIndent := commonIndent(oldLines)
	for i := 0; i+len(oldLines) <= len(fileLines); i++ {
		window := fileLines[i : i+len(oldLines)]
		equal := true
		if indent {
			fileIndent := commonIndent(window)
			fileIndents[i] = fileIndent
			for j := range oldLines {
				if indentCompare(stripIndent(window[j], fileIndent)) != indentCompare(stripIndent(oldLines[j], oldIndent)) {
					equal = false
					break
				}
			}
		} else {
			for j := range oldLines {
				if strings.TrimRight(window[j], " \t") != strings.TrimRight(oldLines[j], " \t") {
					equal = false
					break
				}
			}
		}
		if equal {
			matches = append(matches, i)
		}
	}
	if len(matches) > 1 {
		lines := make([]int, 0, min(5, len(matches)))
		for _, i := range matches[:min(5, len(matches))] {
			lines = append(lines, i+1)
		}
		return text, 0, 0, "", false, fmt.Errorf("%s", multipleError(len(matches), lines))
	}
	if len(matches) != 1 {
		return text, 0, 0, "", false, nil
	}
	index := matches[0]
	newText := replacement
	note := "applied with trailing-whitespace normalization"
	if indent {
		fileIndent := fileIndents[index]
		newLines := strings.Split(replacement, "\n")
		for i, line := range newLines {
			if strings.TrimSpace(line) != "" {
				relative := stripIndent(line, oldIndent)
				if strings.Contains(fileIndent, "\t") {
					relative = spacesToTabs(relative)
				}
				newLines[i] = fileIndent + relative
			}
		}
		newText = strings.Join(newLines, "\n")
		note = "applied; indentation adjusted (" + indentDelta(oldIndent, fileIndent) + ")"
	}
	out := append([]string{}, fileLines[:index]...)
	out = append(out, strings.Split(newText, "\n")...)
	out = append(out, fileLines[index+len(oldLines):]...)
	return strings.Join(out, "\n"), index + 1, index + len(oldLines), note, true, nil
}

func multipleError(count int, lines []int) string {
	parts := make([]string, len(lines))
	for i, line := range lines {
		parts[i] = fmt.Sprint(line)
	}
	return fmt.Sprintf("old_string matches %d places (lines %s); include more surrounding lines so it matches once.", count, strings.Join(parts, ", "))
}
func occurrenceLines(text, needle string, limit int) []int {
	out := []int{}
	offset := 0
	for len(out) < limit {
		index := strings.Index(text[offset:], needle)
		if index < 0 {
			break
		}
		absolute := offset + index
		out = append(out, lineAt(text, absolute))
		offset = absolute + len(needle)
	}
	return out
}
func lineAt(text string, index int) int { return strings.Count(text[:max(0, index)], "\n") + 1 }
func commonIndent(lines []string) string {
	var common string
	first := true
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		prefix := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		if first {
			common = prefix
			first = false
			continue
		}
		for !strings.HasPrefix(prefix, common) && common != "" {
			common = common[:len(common)-1]
		}
	}
	return common
}
func stripIndent(line, indent string) string {
	if strings.HasPrefix(line, indent) {
		return line[len(indent):]
	}
	return strings.TrimLeft(line, " \t")
}
func trimCompare(value string) string { return strings.TrimRight(value, " \t") }
func indentCompare(value string) string {
	value = strings.TrimRight(value, " \t")
	leading := value[:len(value)-len(strings.TrimLeft(value, " \t"))]
	columns := 0
	for _, ch := range leading {
		if ch == '\t' {
			columns += 4
		} else {
			columns++
		}
	}
	return strings.Repeat(" ", columns) + value[len(leading):]
}
func spacesToTabs(value string) string {
	leading := value[:len(value)-len(strings.TrimLeft(value, " "))]
	return strings.Repeat("\t", len(leading)/4) + strings.Repeat(" ", len(leading)%4) + value[len(leading):]
}
func indentDelta(old, file string) string {
	if strings.Contains(file, "\t") || strings.Contains(old, "\t") {
		return "tabs"
	}
	delta := len(file) - len(old)
	return fmt.Sprintf("%+d spaces", delta)
}
func contextRegion(text string, start, count int) string {
	lines := strings.Split(text, "\n")
	from := max(0, start-3)
	to := min(len(lines), start-1+count+2)
	selected := lines[from:to]
	if len(selected) > 40 {
		selected = append(append([]string{}, selected[:20]...), append([]string{"[… cut …]"}, selected[len(selected)-20:]...)...)
	}
	return strings.Join(selected, "\n")
}

func nearMiss(path, text, old string) error {
	fileLines := strings.Split(text, "\n")
	oldLines := strings.Split(old, "\n")
	best, bestStart := -1.0, 0
	for i := 0; i+len(oldLines) <= len(fileLines); i++ {
		window := fileLines[i : i+len(oldLines)]
		matching := 0
		for j := range oldLines {
			if collapse(window[j]) == collapse(oldLines[j]) {
				matching++
			}
		}
		lineScore := float64(matching) / float64(len(oldLines))
		charScore := similarity(strings.Join(window, "\n"), old)
		score := lineScore + charScore/1000
		if score > best {
			best = score
			bestStart = i
		}
	}
	lineScore := math.Floor(best*1000) / 1000
	if lineScore < .6 {
		return fmt.Errorf("old_string not found in %s (%d lines); no similar region. Read the file before editing.", cleanRel(path), len(fileLines))
	}
	window := fileLines[bestStart : bestStart+len(oldLines)]
	diff := 0
	for diff < len(oldLines) && collapse(window[diff]) == collapse(oldLines[diff]) {
		diff++
	}
	if diff >= len(oldLines) {
		diff = len(oldLines) - 1
	}
	kind := "different text"
	if strings.ReplaceAll(window[diff], "\t", "    ") == strings.ReplaceAll(oldLines[diff], "\t", "    ") {
		kind = "tab vs spaces"
	} else if strings.TrimRight(window[diff], " \t") == strings.TrimRight(oldLines[diff], " \t") {
		kind = "trailing whitespace"
	}
	return fmt.Errorf("old_string not found. Closest match: lines %d–%d (similarity %.2f). First difference at line %d — file has %q, old_string has %q, %s. Re-read the file before retrying.", bestStart+1, bestStart+len(oldLines), math.Min(1, math.Max(lineScore, similarity(strings.Join(window, "\n"), old))), bestStart+diff+1, visible(window[diff]), visible(oldLines[diff]), kind)
}
func collapse(value string) string {
	return strings.Join(strings.Fields(strings.TrimRight(value, " \t")), " ")
}
func similarity(a, b string) float64 {
	ar, br := []rune(collapse(a)), []rune(collapse(b))
	if len(ar)+len(br) == 0 {
		return 1
	}
	if len(ar)*len(br) > 4*1024*4*1024 {
		return 0
	}
	row := make([]int, len(br)+1)
	for _, x := range ar {
		prior := 0
		for j, y := range br {
			saved := row[j+1]
			if x == y {
				row[j+1] = prior + 1
			} else if row[j] > row[j+1] {
				row[j+1] = row[j]
			}
			prior = saved
		}
	}
	return 2 * float64(row[len(br)]) / float64(len(ar)+len(br))
}
func visible(value string) string { return strings.ReplaceAll(value, "\t", "\\t") }
