package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
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
	return "Replace one unique old_string in path with new_string using ordered exact, whitespace-normalized, then block-anchor matching. Preserves line endings, returns a unified diff, and runs an available syntax checker."
}
func (*EditFile) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}, "old_string": map[string]any{"type": "string"}, "new_string": map[string]any{"type": "string"}}, "required": []string{"path", "old_string", "new_string"}}
}
func (e *EditFile) Call(ctx context.Context, s *session.Session, args map[string]any) (string, error) {
	if s.Role == "d" && !s.PlanWriteAllowed() {
		return "", fmt.Errorf("plan-page writes require accepting a proposal")
	}
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
	if s.Role == "d" {
		if err := refuseRepoPolicyWrite(".", path); err != nil {
			return "", err
		}
	}
	root, rootErr := s.WriteRoot(path)
	if rootErr != nil {
		return "", rootErr
	}
	if err := refuseRepoPolicyWrite(root, path); err != nil {
		return "", err
	}
	resolved, err := resolveForSessionTool(ctx, s, root, path)
	if err != nil {
		return "", err
	}
	if err := refuseRepoPolicyWrite(root, resolved); err != nil {
		return "", err
	}
	if err := refusePlanManifestWrite(s, resolved); err != nil {
		return "", err
	}
	displayPath := cleanRel(path)
	if relative, relativeErr := filepath.Rel(root, resolved); relativeErr == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		displayPath = cleanRel(relative)
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
		updated, start, end, note, matched, err = blockAnchorTier(text, old, replacement)
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
	newLines := 0
	if replacement == "" {
		newLines = 0
	} else {
		newLines = strings.Count(replacement, "\n") + 1
	}
	delta := newLines - (end - start + 1)
	result := fmt.Sprintf("ok: replaced lines %d–%d with %d lines (%+d)", start, end, newLines, delta)
	if replacement == "" {
		result = fmt.Sprintf("ok: deleted lines %d–%d", start, end)
	}
	if note != "" {
		result += "; " + note
	}
	result += "\n\n" + unifiedDiff(displayPath, text, updated)
	if check := syntaxCheck(resolved, displayPath); check != "" {
		result += "\n\n" + check
	}
	return prefix + result, nil
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
		return strings.Replace(text, old, replacement, 1), start, start + strings.Count(old, "\n"), "strategy: exact", true, nil
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
				if normalizedCompare(stripIndent(window[j], fileIndent), true) != normalizedCompare(stripIndent(oldLines[j], oldIndent), true) {
					equal = false
					break
				}
			}
		} else {
			for j := range oldLines {
				if normalizedCompare(window[j], false) != normalizedCompare(oldLines[j], false) {
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
	note := "strategy: whitespace-normalized (trailing whitespace or Unicode punctuation)"
	if indent {
		fileIndent := fileIndents[index]
		newLines := []string{}
		if replacement != "" {
			newLines = strings.Split(replacement, "\n")
		}
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
		note = "strategy: whitespace-normalized; indentation adjusted (" + indentDelta(oldIndent, fileIndent) + ")"
	}
	out := append([]string{}, fileLines[:index]...)
	if replacement != "" {
		out = append(out, strings.Split(newText, "\n")...)
	}
	out = append(out, fileLines[index+len(oldLines):]...)
	return strings.Join(out, "\n"), index + 1, index + len(oldLines), note, true, nil
}

func blockAnchorTier(text, old, replacement string) (string, int, int, string, bool, error) {
	fileLines := strings.Split(text, "\n")
	oldLines := strings.Split(old, "\n")
	if len(oldLines) < 3 || len(oldLines) > len(fileLines) {
		return text, 0, 0, "", false, nil
	}
	first := collapse(oldLines[0])
	last := collapse(oldLines[len(oldLines)-1])
	matches := []int{}
	for i := 0; i+len(oldLines) <= len(fileLines); i++ {
		if collapse(fileLines[i]) == first && collapse(fileLines[i+len(oldLines)-1]) == last {
			matches = append(matches, i)
		}
	}
	if len(matches) > 1 {
		lines := make([]int, 0, min(5, len(matches)))
		for _, i := range matches[:min(5, len(matches))] {
			lines = append(lines, i+1)
		}
		return text, 0, 0, "", false, fmt.Errorf("block anchors match %d places (lines %s); include unique first and last lines", len(matches), strings.Trim(strings.Join(strings.Fields(fmt.Sprint(lines)), ", "), "[]"))
	}
	if len(matches) != 1 {
		return text, 0, 0, "", false, nil
	}
	index := matches[0]
	oldIndent := commonIndent(oldLines)
	fileIndent := commonIndent(fileLines[index : index+len(oldLines)])
	newLines := []string{}
	if replacement != "" {
		newLines = strings.Split(replacement, "\n")
	}
	for i, line := range newLines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		relative := stripIndent(line, oldIndent)
		if strings.Contains(fileIndent, "\t") {
			relative = spacesToTabs(relative)
		}
		newLines[i] = fileIndent + relative
	}
	out := append([]string{}, fileLines[:index]...)
	out = append(out, newLines...)
	out = append(out, fileLines[index+len(oldLines):]...)
	return strings.Join(out, "\n"), index + 1, index + len(oldLines), "strategy: block-anchor; loose middle accepted", true, nil
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

var punctuationNormalizer = strings.NewReplacer(
	"\u2018", "'", "\u2019", "'", "\u201c", "\"", "\u201d", "\"",
	"\u2013", "-", "\u2014", "-",
)

func normalizedCompare(value string, indent bool) string {
	value = punctuationNormalizer.Replace(strings.TrimRight(value, " \t"))
	if indent {
		return indentCompare(value)
	}
	return value
}

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
func unifiedDiff(path, before, after string) string {
	beforeLines := strings.Split(before, "\n")
	afterLines := strings.Split(after, "\n")
	prefix := 0
	for prefix < len(beforeLines) && prefix < len(afterLines) && beforeLines[prefix] == afterLines[prefix] {
		prefix++
	}
	beforeEnd, afterEnd := len(beforeLines), len(afterLines)
	for beforeEnd > prefix && afterEnd > prefix && beforeLines[beforeEnd-1] == afterLines[afterEnd-1] {
		beforeEnd--
		afterEnd--
	}
	oldBlock := beforeLines[prefix:beforeEnd]
	newBlock := afterLines[prefix:afterEnd]
	start := prefix + 1
	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n@@ -%d,%d +%d,%d @@\n", cleanRel(path), cleanRel(path), start, len(oldBlock), start, len(newBlock))
	for _, line := range oldBlock {
		b.WriteString("-")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	for _, line := range newBlock {
		b.WriteString("+")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func syntaxCheck(resolved, display string) string {
	ext := strings.ToLower(filepath.Ext(resolved))
	data, err := os.ReadFile(resolved)
	if err != nil {
		return "syntax check: failed: unable to read the edited file"
	}
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	switch ext {
	case ".json":
		if json.Valid(data) {
			return "syntax check: passed (json)"
		}
		var value any
		if err := json.Unmarshal(data, &value); err != nil {
			return "syntax check: failed (json): " + boundedDiagnostic(err.Error(), resolved, display)
		}
	case ".go":
		if _, err := parser.ParseFile(token.NewFileSet(), display, data, parser.AllErrors); err != nil {
			return "syntax check: failed (go): " + boundedDiagnostic(err.Error(), resolved, display)
		}
		return "syntax check: passed (go)"
	default:
		return ""
	}
	return ""
}

func boundedDiagnostic(value, resolved, display string) string {
	value = strings.ReplaceAll(value, resolved, cleanRel(display))
	value = strings.ReplaceAll(value, "\r\n", "\n")
	if len(value) > 2048 {
		value = value[:2048] + " [… cut …]"
	}
	return value
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
	return strings.Join(strings.Fields(punctuationNormalizer.Replace(strings.TrimRight(value, " \t"))), " ")
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
