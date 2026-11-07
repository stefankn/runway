package xcode

import (
	"context"
	"encoding/json"
	"fmt"
)

// List returns the schemes (and, for projects, targets and configurations)
// of a workspace or project.
func (c *Client) List(ctx context.Context, container Container) (ProjectInfo, error) {
	args := append([]string{"-list", "-json"}, container.Args()...)
	out, err := c.r.Output(ctx, Cmd{Name: "xcodebuild", Args: args})
	if err != nil {
		return ProjectInfo{}, fmt.Errorf("reading schemes from %s: %w", container.Path, err)
	}
	info, err := parseList(out)
	if err != nil {
		return ProjectInfo{}, fmt.Errorf("reading schemes from %s: %w", container.Path, err)
	}
	return info, nil
}

// Output of `xcodebuild -list -json`: {"project": {…}} or {"workspace": {…}}.
type rawList struct {
	Project   *ProjectInfo `json:"project"`
	Workspace *ProjectInfo `json:"workspace"`
}

func parseList(data []byte) (ProjectInfo, error) {
	var raw rawList
	if err := json.Unmarshal(data, &raw); err != nil {
		return ProjectInfo{}, fmt.Errorf("parsing xcodebuild -list JSON: %w", err)
	}
	switch {
	case raw.Workspace != nil:
		return *raw.Workspace, nil
	case raw.Project != nil:
		return *raw.Project, nil
	default:
		return ProjectInfo{}, fmt.Errorf("xcodebuild -list JSON has neither a project nor a workspace")
	}
}
