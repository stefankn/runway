// Command runway builds, runs and streams logs for Xcode apps from the terminal.
package main

import (
	"os"

	"github.com/stefankn/runway/internal/cli"
)

// Set at release time by GoReleaser via -ldflags "-X main.version=… -X main.commit=… -X main.date=…".
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	os.Exit(cli.Execute(cli.BuildInfo{Version: version, Commit: commit, Date: date}))
}
