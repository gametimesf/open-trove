package config

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestLoadLLMSTxtOverride(t *testing.T) {
	const content = "# Team documentation\r\n\r\nCafé → diagrams\n  preserve spacing  \n\n"
	for _, tc := range []struct {
		name, file, inline string
		want               *string
	}{
		{name: "default"},
		{name: "file", file: content, want: stringPointer(content)},
		{name: "inline", inline: content, want: stringPointer(content)},
		{name: "inline wins", file: "old document", inline: content, want: stringPointer(content)},
		{name: "unrelated inline preserves file", file: content, want: stringPointer(content)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			t.Setenv("ENVIRONMENT", "")
			t.Setenv("TROVE_CONFIG", "")
			t.Setenv("TROVE_CONFIG_YAML", "port: '8081'\n")
			if tc.file != "" {
				body, err := yaml.Marshal(map[string]string{"llms_txt_override": tc.file})
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile("trove.yaml", body, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.inline != "" {
				body, err := yaml.Marshal(map[string]string{"llms_txt_override": tc.inline})
				if err != nil {
					t.Fatal(err)
				}
				t.Setenv("TROVE_CONFIG_YAML", string(body))
			}
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == nil {
				if cfg.LLMSTxtOverride != nil {
					t.Fatal("expected embedded default")
				}
			} else if cfg.LLMSTxtOverride == nil || *cfg.LLMSTxtOverride != *tc.want {
				t.Fatalf("override did not preserve exact content: %v", cfg.LLMSTxtOverride)
			}
		})
	}
}

func TestLLMSTxtOverrideValidation(t *testing.T) {
	for _, tc := range []struct{ name, content, wantErr string }{
		{"empty", "", "must not be blank"},
		{"whitespace", " \n\t\r\n", "must not be blank"},
		{"invalid UTF-8", string([]byte{0xff}), "valid UTF-8"},
		{"over limit", strings.Repeat("a", (64<<10)+1), "65536 bytes"},
		{"multibyte over limit", strings.Repeat("é", (32<<10)+1), "65536 bytes"},
		{"at limit", strings.Repeat("a", 64<<10), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			t.Setenv("ENVIRONMENT", "")
			t.Setenv("TROVE_CONFIG", "")
			body, err := yaml.Marshal(map[string]string{"llms_txt_override": tc.content})
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("TROVE_CONFIG_YAML", string(body))
			_, err = Load()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func stringPointer(s string) *string { return &s }

func TestLLMSTxtNullSelectsDefault(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("ENVIRONMENT", "")
	t.Setenv("TROVE_CONFIG", "")
	if err := os.WriteFile("trove.yaml", []byte("llms_txt_override: file content\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TROVE_CONFIG_YAML", "llms_txt_override: null\n")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLMSTxtOverride != nil {
		t.Fatal("null override must select embedded default")
	}
}
