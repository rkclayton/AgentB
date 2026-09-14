package attachment

import "testing"

func TestSanitizeAttachmentBasenameStripsPathsControlsAndLeadingDots(t *testing.T) {
	got, err := SanitizeName("..\\folder/..\x00report.txt")
	if err != nil || got != "report.txt" {
		t.Fatalf("SanitizeName=%q, %v", got, err)
	}
}

func TestSanitizeAttachmentBasenameRefusesEmptyAndWindowsDeviceNames(t *testing.T) {
	for _, name := range []string{"...", "NUL", "con.txt", `folder\\LPT9.log`} {
		if got, err := SanitizeName(name); err == nil {
			t.Fatalf("SanitizeName(%q)=%q, want error", name, got)
		}
	}
}

func TestTextEncodedAttachmentKindsIncludeSVGAndSiblingFormats(t *testing.T) {
	for _, name := range []string{"diagram.svg", "data.json", "document.xml", "README.md", "app.webmanifest", "calendar.ics", "contact.vcf", "document.rtf", "script.cmd"} {
		if got := Classify(name); got != Text {
			t.Fatalf("Classify(%q)=%q, want text", name, got)
		}
	}
}

func TestUnknownExtensionUsesStrictUTF8ContentFallback(t *testing.T) {
	if got := ClassifyContent("notes.unknown", []byte("plain UTF-8: café\n")); got != Text {
		t.Fatalf("valid UTF-8 classified as %q", got)
	}
	for _, content := range [][]byte{{0, 1, 2}, {0xff, 0xfe}} {
		if got := ClassifyContent("blob.unknown", content); got != Binary {
			t.Fatalf("binary bytes classified as %q", got)
		}
	}
}
