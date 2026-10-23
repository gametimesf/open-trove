package config

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func llmsValue(cfg *Config, field string) *string {
	if field == "llms_txt_append" {
		return cfg.LLMSTxtAppend
	}
	return cfg.LLMSTxtOverride
}

func TestLoadLLMSTxt(t *testing.T) {
	const content = "# Team documentation\r\n\r\nCafé → diagrams\n  preserve spacing  \n\n"
	for _, field := range []string{"llms_txt_override", "llms_txt_append"} {
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
			t.Run(field+"/"+tc.name, func(t *testing.T) {
				t.Chdir(t.TempDir())
				t.Setenv("ENVIRONMENT", "")
				t.Setenv("TROVE_CONFIG", "")
				t.Setenv("TROVE_CONFIG_YAML", "port: '8081'\n")
				if tc.file != "" {
					body, err := yaml.Marshal(map[string]string{field: tc.file})
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile("trove.yaml", body, 0600); err != nil {
						t.Fatal(err)
					}
				}
				if tc.inline != "" {
					body, err := yaml.Marshal(map[string]string{field: tc.inline})
					if err != nil {
						t.Fatal(err)
					}
					t.Setenv("TROVE_CONFIG_YAML", string(body))
				}
				cfg, err := Load()
				if err != nil {
					t.Fatal(err)
				}
				got := llmsValue(cfg, field)
				if tc.want == nil {
					if got != nil {
						t.Fatal("expected embedded default")
					}
				} else if got == nil || *got != *tc.want {
					t.Fatalf("%s did not preserve exact content", field)
				}
			})
		}
	}
}

func TestLLMSTxtValidation(t *testing.T) {
	for _, field := range []string{"llms_txt_override", "llms_txt_append"} {
		for _, tc := range []struct{ name, content, wantErr string }{
			{"empty", "", "must not be blank"},
			{"whitespace", " \n\t\r\n", "must not be blank"},
			{"invalid UTF-8", string([]byte{0xff}), "valid UTF-8"},
			{"over limit", strings.Repeat("a", (64<<10)+1), "65536 bytes"},
			{"multibyte over limit", strings.Repeat("é", (32<<10)+1), "65536 bytes"},
			{"at limit", strings.Repeat("a", 64<<10), ""},
		} {
			t.Run(field+"/"+tc.name, func(t *testing.T) {
				t.Chdir(t.TempDir())
				t.Setenv("ENVIRONMENT", "")
				t.Setenv("TROVE_CONFIG", "")
				body, err := yaml.Marshal(map[string]string{field: tc.content})
				if err != nil {
					t.Fatal(err)
				}
				t.Setenv("TROVE_CONFIG_YAML", string(body))
				_, err = Load()
				if tc.wantErr == "" {
					if err != nil {
						t.Fatal(err)
					}
				} else if err == nil || (!strings.Contains(err.Error(), field) || !strings.Contains(err.Error(), tc.wantErr)) {
					t.Fatalf("error = %v, want %s %q", err, field, tc.wantErr)
				}
			})
		}
	}
}

func stringPointer(s string) *string { return &s }

func TestLLMSTxtNullSelectsDefault(t *testing.T) {
	for _, field := range []string{"llms_txt_override", "llms_txt_append"} {
		t.Run(field, func(t *testing.T) {
			t.Chdir(t.TempDir())
			t.Setenv("ENVIRONMENT", "")
			t.Setenv("TROVE_CONFIG", "")
			if err := os.WriteFile("trove.yaml", []byte(field+": file content\n"), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("TROVE_CONFIG_YAML", field+": null\n")
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			if llmsValue(cfg, field) != nil {
				t.Fatal("null must select embedded default")
			}
		})
	}
}

func TestLLMSTxtModesAreMutuallyExclusive(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("ENVIRONMENT", "")
	t.Setenv("TROVE_CONFIG", "")
	// A setting in the base file must not be silently hidden by another mode in inline config.
	if err := os.WriteFile("trove.yaml", []byte("llms_txt_override: replace\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TROVE_CONFIG_YAML", "llms_txt_append: extend\n")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("error = %v", err)
	}
	t.Setenv("TROVE_CONFIG_YAML", "llms_txt_override: null\nllms_txt_append: extend\n")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLMSTxtOverride != nil || cfg.LLMSTxtAppend == nil || *cfg.LLMSTxtAppend != "extend" {
		t.Fatal("explicit mode switch failed")
	}
}
