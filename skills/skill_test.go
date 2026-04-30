// Package skills_test verifies that every SKILL.md in this directory is
// well-formed (has YAML frontmatter with name + description, and a
// non-empty body). Catches accidental edits that would silently disable a
// skill in the harness.
package skills_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestSkillFiles(t *testing.T) {
	matches, err := filepath.Glob(filepath.Join("..", "skills", "*", "SKILL.md"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) == 0 {
		// also try same-dir glob in case CWD differs
		matches, _ = filepath.Glob(filepath.Join(".", "*", "SKILL.md"))
	}
	if len(matches) == 0 {
		t.Fatal("no SKILL.md files found")
	}
	for _, path := range matches {
		t.Run(path, func(t *testing.T) {
			validateSkill(t, path)
		})
	}
}

func validateSkill(t *testing.T, path string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	contents := string(raw)
	if !strings.HasPrefix(contents, "---\n") {
		t.Fatal("missing opening --- frontmatter marker")
	}
	rest := contents[4:]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		t.Fatal("missing closing --- frontmatter marker")
	}
	frontmatter := rest[:end]
	body := strings.TrimSpace(rest[end+5:])
	if body == "" {
		t.Fatal("empty body after frontmatter")
	}

	keys := map[string]string{}
	var current string
	for _, line := range strings.Split(frontmatter, "\n") {
		// Continuation lines (start with whitespace) belong to the previous key.
		if current != "" && (strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "\t")) {
			keys[current] += " " + strings.TrimSpace(line)
			continue
		}
		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		k := strings.TrimSpace(line[:idx])
		v := strings.TrimSpace(line[idx+1:])
		keys[k] = v
		current = k
	}

	if name, ok := keys["name"]; !ok || name == "" {
		t.Error("frontmatter missing required `name` key")
	} else if strings.ContainsAny(name, " \t/\\") {
		t.Errorf("frontmatter `name` must be a slug (no spaces or path separators): %q", name)
	} else if folder := filepath.Base(filepath.Dir(path)); folder != name {
		t.Errorf("frontmatter `name` must match skill folder: name=%q folder=%q", name, folder)
	}
	if desc, ok := keys["description"]; !ok || len(desc) < 40 {
		t.Errorf("frontmatter `description` is too short (must explain trigger conditions): %q", desc)
	} else if strings.ContainsAny(desc, "<>") {
		t.Errorf("frontmatter `description` must not use angle-bracket placeholders: %q", desc)
	} else if len(desc) > 1024 {
		t.Errorf("frontmatter `description` is too long: %d characters", len(desc))
	}

	// Defense against silently erasing the prompt-injection guidance during
	// future edits. Read-only skills MUST surface untrusted-content warnings.
	if !strings.Contains(strings.ToLower(body), "untrusted") {
		t.Error("body is missing the untrusted-content warning — required for any skill that surfaces user-supplied data to the assistant")
	}
	if lines := strings.Count(body, "\n") + 1; lines > 500 {
		t.Errorf("body is too long for progressive disclosure: %d lines", lines)
	}

	if name := keys["name"]; name != "" {
		validateOpenAIMetadata(t, path, name)
	}
}

func validateOpenAIMetadata(t *testing.T, skillPath, skillName string) {
	t.Helper()
	path := filepath.Join(filepath.Dir(skillPath), "agents", "openai.yaml")
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		t.Fatalf("read openai metadata: %v", err)
	}
	fields := parseOpenAIInterface(raw)
	for _, key := range []string{"display_name", "short_description", "default_prompt"} {
		if fields[key] == "" {
			t.Errorf("agents/openai.yaml missing interface.%s", key)
		}
	}
	if short := fields["short_description"]; short != "" && (len(short) < 25 || len(short) > 64) {
		t.Errorf("interface.short_description must be 25-64 characters: %q (%d)", short, len(short))
	}
	if prompt := fields["default_prompt"]; prompt != "" && !strings.Contains(prompt, "$"+skillName) {
		t.Errorf("interface.default_prompt must mention $%s: %q", skillName, prompt)
	}
}

func parseOpenAIInterface(raw []byte) map[string]string {
	fields := map[string]string{}
	inInterface := false
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if !strings.HasPrefix(line, " ") && strings.HasSuffix(strings.TrimSpace(line), ":") {
			inInterface = strings.TrimSpace(line) == "interface:"
			continue
		}
		if !inInterface || !strings.HasPrefix(line, "  ") {
			continue
		}
		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		value := strings.TrimSpace(line[idx+1:])
		if unquoted, err := strconv.Unquote(value); err == nil {
			value = unquoted
		}
		fields[key] = value
	}
	return fields
}
