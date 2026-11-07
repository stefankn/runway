// Package config loads runway's settings from .runway.toml, RUNWAY_*
// environment variables and command-line flags, and merges them by priority.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// FileName is the name of the project configuration file.
const FileName = ".runway.toml"

// Config holds every setting runway reads. Empty means "not set".
type Config struct {
	Scheme        string `toml:"scheme"`
	Workspace     string `toml:"workspace"`
	Project       string `toml:"project"`
	Configuration string `toml:"configuration"`
	Team          string `toml:"team"`
	Destination   string `toml:"destination"`
	DerivedData   string `toml:"derived_data"`
	LogSubsystem  string `toml:"log_subsystem"`
}

// Source says where a resolved value came from.
type Source string

// Value sources, from highest to lowest priority.
const (
	SourceFlag     Source = "flag"
	SourceEnv      Source = "env"
	SourceFile     Source = "file"
	SourceDetected Source = "detected"
	SourceDefault  Source = "default"
)

// Setting keys, in display order. They match the .runway.toml keys.
const (
	KeyScheme        = "scheme"
	KeyWorkspace     = "workspace"
	KeyProject       = "project"
	KeyConfiguration = "configuration"
	KeyTeam          = "team"
	KeyDestination   = "destination"
	KeyDerivedData   = "derived_data"
	KeyLogSubsystem  = "log_subsystem"
)

type field struct {
	key  string
	ptr  func(*Config) *string
	path bool // relative values resolve against the layer's base directory
}

var fields = []field{
	{KeyScheme, func(c *Config) *string { return &c.Scheme }, false},
	{KeyWorkspace, func(c *Config) *string { return &c.Workspace }, true},
	{KeyProject, func(c *Config) *string { return &c.Project }, true},
	{KeyConfiguration, func(c *Config) *string { return &c.Configuration }, false},
	{KeyTeam, func(c *Config) *string { return &c.Team }, false},
	{KeyDestination, func(c *Config) *string { return &c.Destination }, false},
	{KeyDerivedData, func(c *Config) *string { return &c.DerivedData }, true},
	{KeyLogSubsystem, func(c *Config) *string { return &c.LogSubsystem }, false},
}

// Keys returns all setting keys in display order.
func Keys() []string {
	keys := make([]string, len(fields))
	for i, f := range fields {
		keys[i] = f.key
	}
	return keys
}

func lookup(key string) field {
	i := slices.IndexFunc(fields, func(f field) bool { return f.key == key })
	if i < 0 {
		panic("config: unknown key " + key)
	}
	return fields[i]
}

// Get returns the value for key.
func (c *Config) Get(key string) string { return *lookup(key).ptr(c) }

// Layer is one source of settings. BaseDir is the directory that relative
// paths in it are resolved against.
type Layer struct {
	Source  Source
	Config  Config
	BaseDir string
}

// Resolved is the merged configuration.
type Resolved struct {
	Config
	Sources map[string]Source // source of every key that has a value
	File    string            // path of the .runway.toml used, or ""
	Root    string            // project root: the file's directory, or the working directory
}

// Set records a value and its source, e.g. for auto-detected settings.
func (r *Resolved) Set(key, value string, src Source) {
	*lookup(key).ptr(&r.Config) = value
	r.Sources[key] = src
}

// Describe names where key's value came from, for messages: the flag, the
// environment variable, the config file path, or the source name.
func (r *Resolved) Describe(key string) string {
	switch r.Sources[key] {
	case SourceFlag:
		return "--" + strings.ReplaceAll(key, "_", "-")
	case SourceEnv:
		return EnvVar(key)
	case SourceFile:
		return r.File
	default:
		return string(r.Sources[key])
	}
}

// Merge combines layers, highest priority first: for each key the first layer
// that sets it wins. Workspace and project count as one setting, so a layer
// that sets either overrides both in lower layers. Relative paths become
// absolute, and defaults fill in configuration and derived_data.
func Merge(root, file string, layers ...Layer) (Resolved, error) {
	r := Resolved{Sources: map[string]Source{}, File: file, Root: root}

	for _, l := range layers {
		ws, proj := l.Config.Workspace, l.Config.Project
		if ws != "" && proj != "" {
			return Resolved{}, fmt.Errorf("both workspace and project are set (%s); use only one", l.Source)
		}
		if ws != "" {
			r.Set(KeyWorkspace, absPath(l.BaseDir, ws), l.Source)
			break
		}
		if proj != "" {
			r.Set(KeyProject, absPath(l.BaseDir, proj), l.Source)
			break
		}
	}

	for _, f := range fields {
		if f.key == KeyWorkspace || f.key == KeyProject {
			continue
		}
		for _, l := range layers {
			v := *f.ptr(&l.Config)
			if v == "" {
				continue
			}
			if f.path {
				v = absPath(l.BaseDir, v)
			}
			r.Set(f.key, v, l.Source)
			break
		}
	}

	if r.Configuration == "" {
		r.Set(KeyConfiguration, "Debug", SourceDefault)
	}
	if r.DerivedData == "" {
		r.Set(KeyDerivedData, filepath.Join(root, ".build", "runway"), SourceDefault)
	}
	return r, nil
}

func absPath(base, p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, rest)
		}
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(base, p)
}

// FindFile looks for .runway.toml in start and its parent directories.
// It returns "" if there is none.
func FindFile(start string) string {
	dir := filepath.Clean(start)
	for {
		p := filepath.Join(dir, FileName)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// LoadFile reads a .runway.toml. Unknown keys are an error, so typos don't
// go unnoticed.
func LoadFile(path string) (Config, error) {
	var c Config
	md, err := toml.DecodeFile(path, &c)
	if err != nil {
		if perr, ok := errors.AsType[toml.ParseError](err); ok {
			return Config{}, fmt.Errorf("%s: %s", path, perr.ErrorWithPosition())
		}
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, k := range undecoded {
			keys[i] = fmt.Sprintf("%q", k.String())
		}
		return Config{}, fmt.Errorf("%s: unknown key %s (valid keys: %s)",
			path, strings.Join(keys, ", "), strings.Join(Keys(), ", "))
	}
	return c, nil
}

// EnvVar returns the environment variable for a key, e.g. RUNWAY_DERIVED_DATA.
func EnvVar(key string) string { return "RUNWAY_" + strings.ToUpper(key) }

// FromEnv reads RUNWAY_* environment variables.
func FromEnv(getenv func(string) string) Config {
	var c Config
	for _, f := range fields {
		*f.ptr(&c) = getenv(EnvVar(f.key))
	}
	return c
}
