package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestSkillPrints(t *testing.T) {
	out, _, err := runOpts(t, &options{}, "skill")
	if err != nil {
		t.Fatal(err)
	}
	if out != string(skillMarkdown) {
		t.Error("runway skill should print SKILL.md unchanged")
	}
}

// TestSkillFrontmatter checks the fields agents need to discover the skill,
// within the Agent Skills limits (name: lowercase, ≤ 64; description ≤ 1024).
func TestSkillFrontmatter(t *testing.T) {
	text := string(skillMarkdown)
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		t.Fatal("SKILL.md must start with YAML frontmatter")
	}
	front, _, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		t.Fatal("SKILL.md frontmatter is not closed")
	}
	fields := map[string]string{}
	for _, l := range strings.Split(front, "\n") {
		k, v, _ := strings.Cut(l, ": ")
		fields[k] = v
	}
	if fields["name"] != skillName {
		t.Errorf("name = %q, want %q (the install folder)", fields["name"], skillName)
	}
	if d := fields["description"]; d == "" || len(d) > 1024 {
		t.Errorf("description must be 1–1024 characters, has %d", len(d))
	}
}

// TestSkillFlagsExist keeps SKILL.md in step with the commands: every --flag
// it mentions must exist.
func TestSkillFlagsExist(t *testing.T) {
	var cmds []*cobra.Command
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		cmds = append(cmds, c)
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(newRootCommand(testInfo, &options{}))
	known := func(name string) bool {
		for _, c := range cmds {
			if c.Flags().Lookup(name) != nil || c.PersistentFlags().Lookup(name) != nil {
				return true
			}
		}
		return false
	}

	for _, m := range regexp.MustCompile(`--([a-z][a-z-]*)`).FindAllStringSubmatch(string(skillMarkdown), -1) {
		if !known(m[1]) {
			t.Errorf("SKILL.md mentions --%s, which no command has", m[1])
		}
	}
}

func skillEnv(t *testing.T) (home string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	return home
}

func TestSkillInstallPersonal(t *testing.T) {
	home := skillEnv(t)
	dir := t.TempDir()
	path := filepath.Join(home, ".claude", "skills", "runway", "SKILL.md")

	out, _, err := runOpts(t, &options{workDir: dir}, "skill", "install")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(skillMarkdown) {
		t.Fatalf("installed skill: %v", err)
	}
	if !strings.HasPrefix(out, "Installed the runway skill in "+path) {
		t.Errorf("stdout = %q", out)
	}

	if out, _, err = runOpts(t, &options{workDir: dir}, "skill", "install"); err != nil || !strings.Contains(out, "is up to date") {
		t.Errorf("second install: out = %q, err = %v", out, err)
	}

	if err := os.WriteFile(path, []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err = runOpts(t, &options{workDir: dir}, "skill", "install"); err == nil || !strings.Contains(err.Error(), "use --force") {
		t.Errorf("changed file without --force: err = %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "edited\n" {
		t.Error("changed file was overwritten without --force")
	}
	if out, _, err = runOpts(t, &options{workDir: dir}, "skill", "install", "--force"); err != nil || !strings.HasPrefix(out, "Updated the runway skill") {
		t.Errorf("--force: out = %q, err = %v", out, err)
	}
	if got, _ := os.ReadFile(path); string(got) != string(skillMarkdown) {
		t.Error("--force did not replace the file")
	}
}

func TestSkillInstallClaudeConfigDir(t *testing.T) {
	skillEnv(t)
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	if _, _, err := runOpts(t, &options{workDir: t.TempDir()}, "skill", "install"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cfg, "skills", "runway", "SKILL.md")); err != nil {
		t.Errorf("CLAUDE_CONFIG_DIR ignored: %v", err)
	}
}

func TestSkillInstallProject(t *testing.T) {
	skillEnv(t)

	// The project root is the folder with .runway.toml, even from below it.
	root := projectDir(t, []string{"Sources/App"}, map[string]string{".runway.toml": `scheme = "App"` + "\n"})
	out, _, err := runOpts(t, &options{workDir: filepath.Join(root, "Sources", "App")}, "skill", "install", "--project")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".claude", "skills", "runway", "SKILL.md")); err != nil {
		t.Errorf("not installed in the project root: %v", err)
	}
	if want := filepath.Join("..", "..", ".claude", "skills", "runway", "SKILL.md"); !strings.Contains(out, want) {
		t.Errorf("stdout = %q, want the path relative to the working folder (%s)", out, want)
	}

	// Without .runway.toml, it goes into the working folder.
	dir := t.TempDir()
	if _, _, err := runOpts(t, &options{workDir: dir}, "skill", "install", "--project"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude", "skills", "runway", "SKILL.md")); err != nil {
		t.Errorf("not installed in the working folder: %v", err)
	}
}
