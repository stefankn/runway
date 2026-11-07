package ui

import (
	"errors"
	"io"
	"os"

	"github.com/charmbracelet/huh"
)

// Picker asks the user to choose one of options and returns its index.
type Picker func(title string, options []string) (int, error)

// ErrCancelled is returned when the user dismisses a picker (Esc or Ctrl-C).
var ErrCancelled = errors.New("cancelled")

// Interactive reports whether runway may prompt: stdin and stderr (where
// prompts are drawn) must both be terminals.
func Interactive(stdin io.Reader, stderr io.Writer) bool {
	return IsTerminal(stdin) && IsTerminal(stderr)
}

// HuhPicker returns a Picker that shows an arrow-key selection list. It draws
// on out (stderr), so stdout stays clean for piping. Set ACCESSIBLE=1 for a
// screen-reader-friendly numbered prompt.
func HuhPicker(in io.Reader, out io.Writer) Picker {
	return func(title string, options []string) (int, error) {
		choice := 0
		opts := make([]huh.Option[int], len(options))
		for i, o := range options {
			opts[i] = huh.NewOption(o, i)
		}
		form := huh.NewForm(huh.NewGroup(
			huh.NewSelect[int]().Title(title).Options(opts...).Value(&choice),
		)).
			WithInput(in).
			WithOutput(out).
			WithShowHelp(true).
			WithAccessible(os.Getenv("ACCESSIBLE") != "")
		if err := form.Run(); err != nil {
			if errors.Is(err, huh.ErrUserAborted) {
				return -1, ErrCancelled
			}
			return -1, err
		}
		return choice, nil
	}
}
