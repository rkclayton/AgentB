package attachment

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

type Kind string

const (
	Text   Kind = "text"
	Office Kind = "office"
	PDF    Kind = "pdf"
	Image  Kind = "image"
	ZIP    Kind = "zip"
	Binary Kind = "binary"
)

var deviceName = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[1-9]|lpt[1-9])$`)

func SanitizeName(name string) (string, error) {
	name = strings.ReplaceAll(name, `\`, "/")
	if slash := strings.LastIndexByte(name, '/'); slash >= 0 {
		name = name[slash+1:]
	}
	name = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || strings.ContainsRune(`<>:"|?*`, r) || unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	name = strings.TrimLeft(name, ".")
	name = strings.TrimRight(name, ". ")
	if name == "" {
		return "", fmt.Errorf("attachment filename is empty after sanitizing")
	}
	stem := strings.TrimRight(strings.SplitN(name, ".", 2)[0], ". ")
	if deviceName.MatchString(stem) {
		return "", fmt.Errorf("attachment filename %q is a reserved Windows device name", name)
	}
	return name, nil
}

func Classify(path string) Kind {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".txt", ".text", ".go", ".c", ".h", ".cc", ".cpp", ".cs", ".java", ".js", ".mjs", ".cjs", ".ts", ".tsx", ".jsx", ".py", ".rb", ".rs", ".sh", ".ps1", ".bat", ".cmd", ".vbs", ".json", ".jsonl", ".webmanifest", ".map", ".csv", ".tsv", ".md", ".markdown", ".log", ".yaml", ".yml", ".xml", ".svg", ".html", ".htm", ".css", ".sql", ".toml", ".ini", ".cfg", ".conf", ".rtf", ".ics", ".vcf":
		return Text
	case ".docx", ".xlsx", ".pptx":
		return Office
	case ".pdf":
		return PDF
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return Image
	case ".zip":
		return ZIP
	default:
		return Binary
	}
}

func ClassifyContent(path string, content []byte) Kind {
	kind := Classify(path)
	if kind != Binary {
		return kind
	}
	if utf8.Valid(content) && !bytes.ContainsRune(content, '\x00') {
		return Text
	}
	return Binary
}

func ClassifyFile(path string) Kind {
	kind := Classify(path)
	if kind != Binary {
		return kind
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return Binary
	}
	return ClassifyContent(path, content)
}

func SidecarPath(path string) string { return path + ".txt" }

func ContentType(path string) string {
	if value := mime.TypeByExtension(strings.ToLower(filepath.Ext(path))); value != "" {
		return strings.SplitN(value, ";", 2)[0]
	}
	return "application/octet-stream"
}

func ExtractOffice(path string, maxBytes int64) ([]byte, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("open office document: %w", err)
	}
	defer reader.Close()
	wanted := officeXMLFiles(reader.File, strings.ToLower(filepath.Ext(path)))
	var output bytes.Buffer
	remaining := maxBytes
	for _, file := range wanted {
		if remaining <= 0 {
			return nil, fmt.Errorf("office XML exceeds %d bytes", maxBytes)
		}
		opened, err := file.Open()
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", file.Name, err)
		}
		before := remaining
		limited := &io.LimitedReader{R: opened, N: before + 1}
		decoder := xml.NewDecoder(limited)
		var section strings.Builder
		for {
			token, tokenErr := decoder.Token()
			if tokenErr == io.EOF {
				break
			}
			if tokenErr != nil {
				opened.Close()
				return nil, fmt.Errorf("parse %s: %w", file.Name, tokenErr)
			}
			if data, ok := token.(xml.CharData); ok {
				value := strings.TrimSpace(string(data))
				if value != "" {
					if section.Len() > 0 {
						section.WriteByte(' ')
					}
					section.WriteString(value)
				}
			}
		}
		consumed := before + 1 - limited.N
		if consumed > before {
			opened.Close()
			return nil, fmt.Errorf("office XML exceeds %d bytes", maxBytes)
		}
		remaining = before - consumed
		if closeErr := opened.Close(); closeErr != nil {
			return nil, closeErr
		}
		if section.Len() > 0 {
			if output.Len() > 0 {
				output.WriteByte('\n')
			}
			output.WriteString(section.String())
		}
	}
	if output.Len() == 0 {
		return nil, fmt.Errorf("office document contains no readable XML text")
	}
	return output.Bytes(), nil
}

func officeXMLFiles(files []*zip.File, extension string) []*zip.File {
	result := []*zip.File{}
	for _, file := range files {
		name := strings.ToLower(filepath.ToSlash(file.Name))
		match := false
		switch extension {
		case ".docx":
			match = name == "word/document.xml" || strings.HasPrefix(name, "word/header") || strings.HasPrefix(name, "word/footer")
		case ".xlsx":
			match = name == "xl/sharedstrings.xml" || strings.HasPrefix(name, "xl/worksheets/sheet")
		case ".pptx":
			match = strings.HasPrefix(name, "ppt/slides/slide") && strings.HasSuffix(name, ".xml")
		}
		if match {
			result = append(result, file)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func WriteSidecar(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	if closeErr := file.Close(); writeErr == nil {
		writeErr = closeErr
	}
	return writeErr
}
