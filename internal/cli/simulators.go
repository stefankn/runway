package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/stefankn/runway/internal/ui"
	"github.com/stefankn/runway/internal/xcode"
)

var platformNames = map[string]string{
	"ios":      "iOS",
	"watchos":  "watchOS",
	"tvos":     "tvOS",
	"visionos": "visionOS",
}

func newSimulatorsCommand(opts *options) *cobra.Command {
	var flags struct {
		json, booted      bool
		platform, runtime string
	}
	cmd := &cobra.Command{
		Use:   "simulators",
		Short: "List available simulators",
		Example: `  runway simulators
  runway simulators --booted
  runway simulators --platform ios --runtime 27`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			platform := ""
			if flags.platform != "" {
				var ok bool
				if platform, ok = platformNames[strings.ToLower(flags.platform)]; !ok {
					return fmt.Errorf("unknown platform %q (use ios, watchos, tvos or visionos)", flags.platform)
				}
			}

			sims, err := opts.client(cmd).ListSimulators(cmd.Context())
			if err != nil {
				return err
			}
			matched := []xcode.Simulator{}
			for _, s := range sims {
				if (flags.booted && !s.Booted()) ||
					(platform != "" && s.Platform != platform) ||
					(flags.runtime != "" && !xcode.MatchesVersion(s.OSVersion, flags.runtime)) {
					continue
				}
				matched = append(matched, s)
			}

			if flags.json {
				return writeJSON(cmd.OutOrStdout(), matched)
			}
			if len(matched) == 0 {
				msg := "No simulators found. Add one in Xcode (Window → Devices and Simulators)."
				if len(sims) > 0 {
					msg = "No simulators match the filters. Run `runway simulators` to see all of them."
				}
				fmt.Fprintln(cmd.ErrOrStderr(), msg)
				return nil
			}

			st := ui.NewStyler(cmd.OutOrStdout())
			t := ui.Table{Header: []string{"NAME", "RUNTIME", "STATE", "UDID"}, HeaderStyle: st.Bold}
			for _, s := range matched {
				row := ui.Row{Cells: []string{s.Name, orDash(s.Runtime), s.State, s.UDID}}
				if s.Booted() {
					row.Style = st.Green
				}
				t.Rows = append(t.Rows, row)
			}
			return t.Render(cmd.OutOrStdout())
		},
	}
	f := cmd.Flags()
	f.BoolVar(&flags.json, "json", false, "print runway's JSON instead of a table")
	f.BoolVar(&flags.booted, "booted", false, "only show booted simulators")
	f.StringVar(&flags.platform, "platform", "", "only show one platform: ios, watchos, tvos or visionos")
	f.StringVar(&flags.runtime, "runtime", "", "only show runtimes matching a version prefix, e.g. 27 or 27.2")
	return cmd
}
