package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/stefankn/runway/internal/config"
	"github.com/stefankn/runway/internal/destination"
)

// noValue marks --sim or --device given without a value.
const noValue = "\x00"

// destFlags choose where to build and run: --sim, --device or --destination.
// They are shared by build, run and logs.
type destFlags struct {
	sim, device, raw string
}

func (f *destFlags) register(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.StringVar(&f.sim, "sim", "", "use a simulator, by name or UDID (default: an iPhone on the newest iOS)")
	fl.Lookup("sim").NoOptDefVal = noValue
	fl.StringVar(&f.device, "device", "", "use a physical device, by name or UDID (default: the first connected one)")
	fl.Lookup("device").NoOptDefVal = noValue
	fl.StringVar(&f.raw, "destination", "", "raw xcodebuild -destination value, passed unchanged")
	cmd.MarkFlagsMutuallyExclusive("sim", "device", "destination")
}

// request builds the destination request. Because --sim and --device take an
// optional value, `--sim "iPhone 18 Pro"` leaves the name as a positional
// argument; a single positional argument after a bare --sim/--device is taken
// as its value. args are the command's positional arguments; anything after
// `--` is not looked at.
func (f *destFlags) request(cmd *cobra.Command, args []string, cfg *config.Resolved) (destination.Request, error) {
	positional := args
	if dash := cmd.ArgsLenAtDash(); dash >= 0 {
		positional = args[:dash]
	}
	req := destination.Request{
		Raw:        f.raw,
		Configured: cfg.Destination,
		Source:     cfg.Describe(config.KeyDestination),
	}
	take := func(name, value string) *string {
		if !cmd.Flags().Changed(name) {
			return nil
		}
		if value == noValue {
			value = ""
			if len(positional) == 1 {
				value, positional = positional[0], nil
			}
		}
		return &value
	}
	req.Sim = take("sim", f.sim)
	req.Device = take("device", f.device)
	if len(positional) > 0 {
		return destination.Request{}, fmt.Errorf("unexpected argument %q (put xcodebuild or app arguments after --)", positional[0])
	}
	return req, nil
}

// argsAfterDash returns the arguments after `--`.
func argsAfterDash(cmd *cobra.Command, args []string) []string {
	if dash := cmd.ArgsLenAtDash(); dash >= 0 {
		return args[dash:]
	}
	return nil
}
