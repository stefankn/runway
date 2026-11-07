package xcode

// Client wraps Apple's developer tools. All its methods build the exact
// command lines and parse the tools' JSON into runway's domain types.
type Client struct {
	r Runner
}

// New returns a Client that runs commands with r.
func New(r Runner) *Client {
	return &Client{r: r}
}

func xcrun(args ...string) Cmd {
	return Cmd{Name: "xcrun", Args: args}
}
