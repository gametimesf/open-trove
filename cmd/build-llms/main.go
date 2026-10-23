// Command build-llms prepares the guide before it is embedded in the server.
package main

import (
	"crypto/sha256"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"unicode/utf8"
)

func main() {
	guide := flag.String("guide", "docs/llms.txt", "embedded guide path")
	appendPath := flag.String("append", "", "optional appendix file")
	overridePath := flag.String("override", "", "optional full replacement file")
	flag.Parse()
	if err := buildGuide(*guide, *appendPath, *overridePath); err != nil {
		log.Fatal(err)
	}
}

func buildGuide(guide, appendPath, overridePath string) error {
	if appendPath != "" && overridePath != "" {
		return fmt.Errorf("append and override are mutually exclusive")
	}
	if appendPath == "" && overridePath == "" {
		return nil
	}
	source := appendPath
	if overridePath != "" {
		source = overridePath
	}
	content, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("reading customization: %w", err)
	}
	if !utf8.Valid(content) || strings.TrimSpace(string(content)) == "" || len(content) > 65536 {
		return fmt.Errorf("customization must be nonblank UTF-8, at most 65536 bytes")
	}
	mode := "override"
	if appendPath != "" {
		base, err := os.ReadFile(guide)
		if err != nil {
			return fmt.Errorf("reading embedded guide: %w", err)
		}
		content = append(append(base, '\n', '\n'), content...)
		mode = "append"
	}
	if err := os.WriteFile(guide, content, 0644); err != nil {
		return fmt.Errorf("writing embedded guide: %w", err)
	}
	log.Printf("built llms.txt mode=%s bytes=%d sha256=%x", mode, len(content), sha256.Sum256(content))
	return nil
}
