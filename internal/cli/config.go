package cli

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/stefankn/runway/internal/config"
	"github.com/stefankn/runway/internal/project"
	"github.com/stefankn/runway/internal/ui"
)

type settingJSON struct {
	Value  string        `json:"value"`
	Source config.Source `json:"source,omitempty"`
}

type configJSON struct {
	File     string                 `json:"file"`
	Root     string                 `json:"root"`
	Settings map[string]settingJSON `json:"settings"`
}

func newConfigCommand(opts *options) *cobra.Command {
	var (
		pf             projectFlags
		asJSON, noScan bool
	)
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show the resolved settings and where each one comes from",
		Long: `Show the settings runway would use and where each one comes from.

Settings are resolved in this order, highest priority first:
  flag      command-line flags
  env       RUNWAY_* environment variables (e.g. RUNWAY_SCHEME)
  file      .runway.toml in this folder or the nearest parent folder
  detected  found in the project folder (workspace/project, scheme)
  default   built-in defaults`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := opts.resolveConfig(&pf)
			if err != nil {
				return err
			}
			// Detection problems are reported after the settings are shown.
			c, detectErr := project.ResolveContainer(&cfg, opts.picker(cmd))
			if detectErr == nil && !noScan {
				_, detectErr = project.ResolveScheme(cmd.Context(), opts.client(cmd), &cfg, c, opts.picker(cmd))
			}

			if asJSON {
				out := configJSON{File: cfg.File, Root: cfg.Root, Settings: map[string]settingJSON{}}
				for _, k := range config.Keys() {
					out.Settings[k] = settingJSON{Value: cfg.Get(k), Source: cfg.Sources[k]}
				}
				if err := writeJSON(cmd.OutOrStdout(), out); err != nil {
					return err
				}
				return detectErr
			}

			cwd, err := opts.cwd()
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			st := ui.NewStyler(w)
			if cfg.File != "" {
				fmt.Fprintf(w, "Config file: %s\n\n", displayPath(cwd, cfg.File))
			} else {
				fmt.Fprintf(w, "Config file: none (no %s in this folder or above; run `runway init` to create one)\n\n", config.FileName)
			}
			t := ui.Table{Header: []string{"KEY", "VALUE", "SOURCE"}, HeaderStyle: st.Bold}
			for _, k := range config.Keys() {
				v := cfg.Get(k)
				if v != "" && (k == config.KeyWorkspace || k == config.KeyProject || k == config.KeyDerivedData) {
					v = displayPath(cwd, v)
				}
				row := ui.Row{Cells: []string{k, orDash(v), string(cfg.Sources[k])}}
				if v == "" {
					row.Style = st.Dim
				}
				t.Rows = append(t.Rows, row)
			}
			if err := t.Render(w); err != nil {
				return err
			}
			return detectErr
		},
	}
	pf.register(cmd)
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the settings as JSON")
	cmd.Flags().BoolVar(&noScan, "no-detect", false, "don't run xcodebuild -list to detect the scheme")
	return cmd
}

// displayPath shows p relative to dir when that is shorter, e.g.
// "ActorTest.xcodeproj" or "../.runway.toml".
func displayPath(dir, p string) string {
	rel, err := filepath.Rel(dir, p)
	if err != nil || len(rel) >= len(p) {
		return p
	}
	return rel
}
