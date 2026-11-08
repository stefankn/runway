package cli

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/stefankn/runway/internal/config"
)

// skillMarkdown is the Agent Skill that teaches AI coding agents (Claude Code
// and others that read SKILL.md) to use runway. It ships in the binary, so it
// always matches the installed version.
//
//go:embed SKILL.md
var skillMarkdown []byte

// skillName is the skill's folder name; it must match `name` in SKILL.md.
const skillName = "runway"

func newSkillCommand(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Print the Agent Skill that teaches AI coding agents to use runway",
		Long: `Print runway's Agent Skill (SKILL.md), which tells AI coding agents such as
Claude Code how to build, run and read logs with runway.

Use "runway skill install" to install it for Claude Code, or paste the output
into an agent's instructions file (CLAUDE.md, AGENTS.md).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := cmd.OutOrStdout().Write(skillMarkdown)
			return err
		},
	}
	cmd.AddCommand(newSkillInstallCommand(opts))
	return cmd
}

func newSkillInstallCommand(opts *options) *cobra.Command {
	var project, force bool
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install the skill for Claude Code",
		Long: `Install runway's Agent Skill for Claude Code.

By default it goes to ~/.claude/skills/runway/SKILL.md (or under
$CLAUDE_CONFIG_DIR when that is set), for every project. With --project it
goes to .claude/skills/runway/SKILL.md in the project root (the folder with
.runway.toml, or else this folder), so it can be committed and shared.

After upgrading runway, run this again with --force to update the skill.`,
		Example: `  runway skill install
  runway skill install --project
  runway skill install --force`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cwd, err := opts.cwd()
			if err != nil {
				return err
			}
			dir, err := skillDir(cwd, project)
			if err != nil {
				return err
			}
			path := filepath.Join(dir, skillName, "SKILL.md")
			shown := path
			if project {
				shown = displayPath(cwd, path)
			}

			existing, err := os.ReadFile(path)
			switch {
			case err == nil && bytes.Equal(existing, skillMarkdown):
				fmt.Fprintf(cmd.OutOrStdout(), "The runway skill in %s is up to date\n", shown)
				return nil
			case err == nil && !force:
				return fmt.Errorf("%s already exists and differs from this version's skill; use --force to replace it", shown)
			case err != nil && !errors.Is(err, fs.ErrNotExist):
				return err
			}

			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, skillMarkdown, 0o644); err != nil {
				return err
			}
			verb := "Installed"
			if existing != nil {
				verb = "Updated"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s the runway skill in %s\n", verb, shown)
			return nil
		},
	}
	cmd.Flags().BoolVar(&project, "project", false, "install into this project's .claude/skills instead of your home folder")
	cmd.Flags().BoolVar(&force, "force", false, "replace an existing SKILL.md that differs")
	return cmd
}

// skillDir returns the skills folder to install into: the project's
// .claude/skills, or Claude Code's personal one.
func skillDir(cwd string, project bool) (string, error) {
	if project {
		root := cwd
		if file := config.FindFile(cwd); file != "" {
			root = filepath.Dir(file)
		}
		return filepath.Join(root, ".claude", "skills"), nil
	}
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "skills"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "skills"), nil
}
