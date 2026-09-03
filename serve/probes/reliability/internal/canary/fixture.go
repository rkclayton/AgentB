package canary

import (
	"fmt"
	"os"
	"path/filepath"
)

// CreateFixture replaces dir with the deterministic calcfix mini-project.
func CreateFixture(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if abs == filepath.VolumeName(abs)+string(filepath.Separator) {
		return fmt.Errorf("refusing to replace volume root %s", abs)
	}
	if filepath.Clean(abs) == filepath.Clean(filepath.Dir(abs)) {
		return fmt.Errorf("refusing unsafe fixture path %s", abs)
	}
	if err := os.RemoveAll(abs); err != nil {
		return err
	}
	for _, child := range []string{"calc", "check"} {
		if err := os.MkdirAll(filepath.Join(abs, child), 0o755); err != nil {
			return err
		}
	}
	files := map[string]string{
		"go.mod": "module calcfix\n\ngo 1.24\n",
		"calc/ops.go": `package calc

import "errors"

func Add(a, b int) int {
	return a + b
}

func Subtract(a, b int) int {
	return a + b
}

func Divide(a, b int) (int, error) {
	if b == 0 {
		return 0, errors.New("division by zero")
	}
	return a / b, nil
}
`,
		"check/main.go": `package main

import (
	"fmt"
	"os"

	"calcfix/calc"
)

func main() {
	quotient, err := calc.Divide(8, 2)
	if err != nil { fmt.Printf("FAIL divide error: %v\n", err); os.Exit(1) }
	checks := []struct {
		name string
		got  int
		want int
	}{
		{"add positive", calc.Add(2, 3), 5},
		{"add negative", calc.Add(-2, -3), -5},
		{"subtract", calc.Subtract(9, 4), 5},
		{"divide", quotient, 4},
		{"divide truncates", mustDivide(7, 2), 3},
	}
	for _, check := range checks {
		if check.got != check.want {
			fmt.Printf("FAIL %s: expected %d got %d\n", check.name, check.want, check.got); os.Exit(1)
		}
	}
	fmt.Printf("OK %d checks\n", len(checks))
}

func mustDivide(a, b int) int {
	value, _ := calc.Divide(a, b); return value
}
`,
		"README.md": "# calcfix\nA tiny arithmetic project used by the reliability canary.\nRun `go run ./check`.\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(abs, filepath.FromSlash(name)), []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// PrepareAddFixture fixes the unrelated planted subtraction bug before the add task.
func PrepareAddFixture(dir string) error {
	path := filepath.Join(dir, "calc", "ops.go")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	old := "func Subtract(a, b int) int {\n\treturn a + b\n}"
	newText := "func Subtract(a, b int) int {\n\treturn a - b\n}"
	updated, ok := replaceUniqueExact(string(data), old, newText)
	if !ok {
		return fmt.Errorf("could not prepare add fixture")
	}
	return os.WriteFile(path, []byte(updated), 0o644)
}
