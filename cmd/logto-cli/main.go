// Command logto-cli administers a self-hosted Logto instance headlessly.
package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/reapermaga/logto-cli/internal/cli"
)

// version is set at build time: -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() {
	cli.Version = version
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := cli.Run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}
