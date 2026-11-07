package cli

import (
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/stefankn/runway/internal/config"
)

// projectFlags are the flags that choose what to build. They are shared by
// every command that resolves a project (config, init; later build, run, logs).
type projectFlags struct {
	config.Config
}

func (f *projectFlags) register(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.StringVar(&f.Scheme, "scheme", "", "scheme to build")
	fl.StringVar(&f.Workspace, "workspace", "", "path to the .xcworkspace")
	fl.StringVar(&f.Project, "project", "", "path to the .xcodeproj")
	fl.StringVar(&f.Configuration, "configuration", "", `build configuration (default "Debug")`)
	fl.StringVar(&f.Team, "team", "", "development team ID (DEVELOPMENT_TEAM)")
	fl.StringVar(&f.DerivedData, "derived-data", "", `derived data path (default "<project root>/.build/runway")`)
	cmd.MarkFlagsMutuallyExclusive("workspace", "project")
}

// resolveConfig merges, highest priority first: flags, RUNWAY_* environment
// variables, the nearest .runway.toml, and defaults.
func (o *options) resolveConfig(f *projectFlags) (config.Resolved, error) {
	cwd, err := o.cwd()
	if err != nil {
		return config.Resolved{}, err
	}
	root, file := cwd, config.FindFile(cwd)
	var fileCfg config.Config
	if file != "" {
		if fileCfg, err = config.LoadFile(file); err != nil {
			return config.Resolved{}, err
		}
		root = filepath.Dir(file)
	}
	return config.Merge(root, file,
		config.Layer{Source: config.SourceFlag, Config: f.Config, BaseDir: cwd},
		config.Layer{Source: config.SourceEnv, Config: config.FromEnv(os.Getenv), BaseDir: cwd},
		config.Layer{Source: config.SourceFile, Config: fileCfg, BaseDir: root},
	)
}
