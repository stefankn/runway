// Package project finds the workspace or project and the scheme to build,
// from configuration or by looking at the project folder.
package project

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/stefankn/runway/internal/config"
	"github.com/stefankn/runway/internal/ui"
	"github.com/stefankn/runway/internal/xcode"
)

// Project is everything runway needs to know to build.
type Project struct {
	Container     xcode.Container
	Scheme        string
	Configuration string
	Team          string
	DerivedData   string
	Root          string
}

// Detect returns the workspaces in dir or, if there are none, the projects.
// Only dir itself is searched, never inside bundles (every .xcodeproj
// contains a project.xcworkspace). Results are sorted by name.
//
// Xcode 27.2's JSON project format is a project.xcproj file inside the
// .xcodeproj bundle, so .xcodeproj bundles cover both formats.
func Detect(dir string) ([]xcode.Container, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var workspaces, projects []xcode.Container
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		path := filepath.Join(dir, name)
		if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
			continue
		}
		switch filepath.Ext(name) {
		case ".xcworkspace":
			workspaces = append(workspaces, xcode.Container{Kind: xcode.Workspace, Path: path})
		case ".xcodeproj":
			projects = append(projects, xcode.Container{Kind: xcode.Project, Path: path})
		}
	}
	if len(workspaces) > 0 {
		return workspaces, nil
	}
	return projects, nil
}

// ResolveContainer returns the configured workspace or project, or detects
// one in cfg.Root and records it in cfg with source "detected". pick may be
// nil when runway can't prompt; several candidates are then an error.
func ResolveContainer(cfg *config.Resolved, pick ui.Picker) (xcode.Container, error) {
	for _, c := range []struct {
		key  string
		kind xcode.ContainerKind
		path string
	}{
		{config.KeyWorkspace, xcode.Workspace, cfg.Workspace},
		{config.KeyProject, xcode.Project, cfg.Project},
	} {
		if c.path == "" {
			continue
		}
		if fi, err := os.Stat(c.path); err != nil || !fi.IsDir() {
			return xcode.Container{}, fmt.Errorf("%s %s (from %s) does not exist", c.key, c.path, cfg.Describe(c.key))
		}
		return xcode.Container{Kind: c.kind, Path: c.path}, nil
	}

	found, err := Detect(cfg.Root)
	if err != nil {
		return xcode.Container{}, err
	}
	if len(found) == 0 {
		return xcode.Container{}, fmt.Errorf("no Xcode workspace or project found in %s\n"+
			"Run runway in your project folder, or set workspace or project in %s", cfg.Root, config.FileName)
	}
	names := make([]string, len(found))
	for i, c := range found {
		names[i] = filepath.Base(c.Path)
	}
	kind := string(found[0].Kind)
	i, err := choose(kind, names, pick,
		fmt.Sprintf("Set %s in %s, or pass --%s.", kind, config.FileName, kind))
	if err != nil {
		return xcode.Container{}, err
	}
	cfg.Set(kind, found[i].Path, config.SourceDetected)
	return found[i], nil
}

// ResolveScheme returns the configured scheme or asks xcodebuild for the
// container's schemes and records the chosen one in cfg. A configured scheme
// is not checked, which keeps builds fast; xcodebuild reports unknown schemes.
func ResolveScheme(ctx context.Context, client *xcode.Client, cfg *config.Resolved, c xcode.Container, pick ui.Picker) (string, error) {
	if cfg.Scheme != "" {
		return cfg.Scheme, nil
	}
	info, err := client.List(ctx, c)
	if err != nil {
		return "", err
	}
	if len(info.Schemes) == 0 {
		return "", fmt.Errorf("%s has no schemes; create one in Xcode (Product → Scheme → Manage Schemes…)", filepath.Base(c.Path))
	}
	i, err := choose("scheme", info.Schemes, pick,
		fmt.Sprintf("Set scheme in %s, or pass --scheme.", config.FileName))
	if err != nil {
		return "", err
	}
	cfg.Set(config.KeyScheme, info.Schemes[i], config.SourceDetected)
	return info.Schemes[i], nil
}

// Resolve resolves the container and scheme and returns the full Project.
func Resolve(ctx context.Context, client *xcode.Client, cfg *config.Resolved, pick ui.Picker) (Project, error) {
	c, err := ResolveContainer(cfg, pick)
	if err != nil {
		return Project{}, err
	}
	scheme, err := ResolveScheme(ctx, client, cfg, c, pick)
	if err != nil {
		return Project{}, err
	}
	return Project{
		Container:     c,
		Scheme:        scheme,
		Configuration: cfg.Configuration,
		Team:          cfg.Team,
		DerivedData:   cfg.DerivedData,
		Root:          cfg.Root,
	}, nil
}

func choose(what string, options []string, pick ui.Picker, hint string) (int, error) {
	if len(options) == 1 {
		return 0, nil
	}
	if pick == nil {
		return -1, fmt.Errorf("found %d %ss, choose one:\n  %s\n%s",
			len(options), what, strings.Join(options, "\n  "), hint)
	}
	i, err := pick("Choose a "+what, options)
	if err != nil {
		return -1, err
	}
	if i < 0 || i >= len(options) {
		return -1, fmt.Errorf("invalid %s choice %d", what, i)
	}
	return i, nil
}
