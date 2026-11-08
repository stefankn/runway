package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/stefankn/runway/internal/destination"
)

// screenshotFormats maps file extensions to simctl's --type values.
var screenshotFormats = map[string]string{
	".png": "png", ".jpg": "jpeg", ".jpeg": "jpeg", ".tif": "tiff", ".tiff": "tiff", ".bmp": "bmp", ".gif": "gif",
}

// now is replaced in tests.
var now = time.Now

func newScreenshotCommand(opts *options) *cobra.Command {
	var (
		df     destFlags
		output string
	)
	cmd := &cobra.Command{
		Use:   "screenshot [flags]",
		Short: "Save a screenshot of a simulator's screen",
		Long: `Save a screenshot of a booted simulator's screen and print the file's path.

Without --sim, runway uses the configured simulator, or else the booted one
if exactly one is booted. The file type follows the extension of --output:
.png (the default), .jpg, .tiff, .bmp or .gif. An existing file is
overwritten, and missing folders are created. Physical devices are not
supported.`,
		Example: `  runway screenshot
  runway screenshot --sim "iPhone 18 Pro" -o screenshots/home.png`,
		Args: cobra.ArbitraryArgs, // for --sim "name"; see destFlags.request
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := opts.cwd()
			if err != nil {
				return err
			}
			path := output
			if path == "" {
				path = "screenshot-" + now().Format("20060102-150405") + ".png"
			}
			format, ok := screenshotFormats[strings.ToLower(filepath.Ext(path))]
			if !ok {
				return fmt.Errorf("can't save a screenshot as %q; use .png, .jpg, .tiff, .bmp or .gif", filepath.Base(path))
			}
			if !filepath.IsAbs(path) {
				// Also keeps a name like "-" from reaching simctl as an option.
				path = filepath.Join(cwd, path)
			}

			cfg, err := opts.resolveConfig(&projectFlags{})
			if err != nil {
				return err
			}
			req, err := df.request(cmd, args, &cfg)
			if err != nil {
				return err
			}
			errSimOnly := errors.New("screenshot works with simulators only (--sim)")
			if req.Raw != "" || req.Device != nil {
				return errSimOnly
			}
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(req.Configured)), "device") {
				req.Configured = "" // use the booted simulator instead
			}
			req.PreferBooted = true
			ctx, client := cmd.Context(), opts.client(cmd)
			dest, err := destination.Resolve(ctx, client, req, opts.picker(cmd))
			if err != nil {
				return err
			}
			sim := *dest.Simulator
			if !sim.Booted() {
				// simctl would wait for the screen and then time out.
				return fmt.Errorf("simulator %q is not booted; start the app with `runway run --sim` first", sim.Name)
			}

			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := client.SimScreenshot(ctx, sim.UDID, path, format); err != nil {
				return err
			}
			// Relative only inside the working folder, so the path is easy
			// to open for the user or an agent.
			shown := path
			if rel, err := filepath.Rel(cwd, path); err == nil && filepath.IsLocal(rel) {
				shown = rel
			}
			fmt.Fprintln(cmd.OutOrStdout(), shown)
			return nil
		},
	}
	df.register(cmd)
	cmd.Flags().StringVarP(&output, "output", "o", "", "file to write (default screenshot-<date>-<time>.png)")
	return cmd
}
