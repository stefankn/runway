package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/stefankn/runway/internal/config"
	"github.com/stefankn/runway/internal/project"
)

func newInitCommand(opts *options) *cobra.Command {
	var (
		pf    projectFlags
		force bool
	)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create a .runway.toml for the project in this folder",
		Long: `Detect the workspace or project and scheme in this folder and write them to
.runway.toml, so later commands don't have to detect or ask again.

Flags override detection, e.g. runway init --scheme MyApp. Environment
variables and other .runway.toml files are ignored.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cwd, err := opts.cwd()
			if err != nil {
				return err
			}
			path := filepath.Join(cwd, config.FileName)
			if _, err := os.Stat(path); err == nil && !force {
				return fmt.Errorf("%s already exists; use --force to overwrite it", config.FileName)
			} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}

			cfg, err := config.Merge(cwd, "", config.Layer{Source: config.SourceFlag, Config: pf.Config, BaseDir: cwd})
			if err != nil {
				return err
			}
			p, err := project.Resolve(cmd.Context(), opts.client(cmd), &cfg, opts.picker(cmd))
			if err != nil {
				return err
			}

			content := initFileContent(cwd, p, pf.Config)
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Wrote %s (scheme %s, %s %s)\n",
				config.FileName, p.Scheme, p.Container.Kind, displayPath(cwd, p.Container.Path))
			return nil
		},
	}
	pf.register(cmd)
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing .runway.toml")
	return cmd
}

// initFileContent renders .runway.toml with the resolved values and
// commented-out examples for the optional settings.
func initFileContent(dir string, p project.Project, flags config.Config) string {
	var b strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	optional := func(comment, key, value, example string) {
		line("")
		line("# %s", comment)
		if value != "" {
			line("%s = %s", key, tomlString(value))
		} else {
			line("# %s = %s", key, tomlString(example))
		}
	}

	line("# runway settings. Run `runway config` to see the resolved values.")
	line("# Command-line flags and RUNWAY_* environment variables override them.")
	line("")
	line("scheme = %s", tomlString(p.Scheme))
	line("%s = %s", p.Container.Kind, tomlString(displayPath(dir, p.Container.Path)))
	line("configuration = %s", tomlString(p.Configuration))

	derived := ""
	if flags.DerivedData != "" {
		derived = displayPath(dir, p.DerivedData)
	}
	optional("Development team ID (DEVELOPMENT_TEAM), needed to sign for devices.", config.KeyTeam, flags.Team, "ABCDE12345")
	optional(`Default destination: "sim:<name or UDID>", "device:<name or UDID>" or "device".`, config.KeyDestination, "", "sim:iPhone 18 Pro")
	optional("Where build products go, relative to this file.", config.KeyDerivedData, derived, ".build/runway")
	optional("Unified logging subsystem for `runway logs` (defaults to the bundle ID).", config.KeyLogSubsystem, "", "com.example.MyApp")
	return b.String()
}

// tomlString quotes s as a TOML basic string.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
