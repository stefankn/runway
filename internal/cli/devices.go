package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/stefankn/runway/internal/ui"
	"github.com/stefankn/runway/internal/xcode"
)

func newDevicesCommand(opts *options) *cobra.Command {
	var flags struct{ json, all bool }
	cmd := &cobra.Command{
		Use:   "devices",
		Short: "List connected and paired physical devices",
		Long: `List physical devices that are paired and reachable, wired or over the network.
Use --all to include devices that are unpaired or currently unavailable.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			devs, err := opts.client(cmd).ListDevices(cmd.Context())
			if err != nil {
				return err
			}
			shown := []xcode.Device{}
			for _, d := range devs {
				if flags.all || d.Available() {
					shown = append(shown, d)
				}
			}

			if flags.json {
				return writeJSON(cmd.OutOrStdout(), shown)
			}
			if len(shown) == 0 {
				msg := "No devices found. Connect a device, or pair one in Device Hub."
				if len(devs) > 0 {
					msg = "No devices are available right now. Use --all to list unavailable and unpaired devices."
				}
				fmt.Fprintln(cmd.ErrOrStderr(), msg)
				return nil
			}

			st := ui.NewStyler(cmd.OutOrStdout())
			t := ui.Table{Header: []string{"NAME", "MODEL", "OS", "CONNECTION", "UDID"}, HeaderStyle: st.Bold}
			for _, d := range shown {
				conn := d.Connection
				if !d.Paired {
					conn += " (unpaired)"
				}
				row := ui.Row{Cells: []string{d.Name, orDash(d.Model), orDash(d.OSVersion), conn, d.UDID}}
				if !d.Available() {
					row.Style = st.Dim
				}
				t.Rows = append(t.Rows, row)
			}
			return t.Render(cmd.OutOrStdout())
		},
	}
	cmd.Flags().BoolVar(&flags.json, "json", false, "print runway's JSON instead of a table")
	cmd.Flags().BoolVar(&flags.all, "all", false, "include unavailable and unpaired devices")
	return cmd
}
