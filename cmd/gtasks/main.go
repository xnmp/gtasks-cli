// Command gtasks is a command-line client for Google Tasks.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/xnmp/gtasks-cli/internal/app"
	"github.com/xnmp/gtasks-cli/internal/auth"
	"github.com/xnmp/gtasks-cli/internal/cli"
	"github.com/xnmp/gtasks-cli/internal/domain"
	"github.com/xnmp/gtasks-cli/internal/google"
	"github.com/xnmp/gtasks-cli/internal/render"
	"github.com/xnmp/gtasks-cli/internal/state"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// Without a cache dir the zero store disables numbering; `ls` warns.
	numbers, _ := state.DefaultNumbers()
	env := cli.Env{
		In:    os.Stdin,
		Out:   os.Stdout,
		Err:   os.Stderr,
		Today: func() domain.Date { return domain.DateOf(time.Now()) },
		NewStore: func(ctx context.Context) (app.Store, error) {
			paths, err := auth.DefaultPaths()
			if err != nil {
				return nil, err
			}
			client, err := auth.Client(ctx, paths, func(err error) {
				fmt.Fprintf(os.Stderr, "gtasks: warning: could not save refreshed token: %v\n", err)
			})
			if err != nil {
				return nil, err
			}
			return google.NewStore(ctx, client)
		},
		IsTerminal: func() bool {
			fi, err := os.Stdin.Stat()
			return err == nil && fi.Mode()&os.ModeCharDevice != 0
		},
		Numbers: numbers,
		Auth:    cli.AuthEnv{Paths: auth.DefaultPaths},
		Version: version,
	}
	if err := cli.NewRoot(env).ExecuteContext(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "gtasks: %s\n", render.Sanitize(err.Error()))
		os.Exit(1)
	}
}
