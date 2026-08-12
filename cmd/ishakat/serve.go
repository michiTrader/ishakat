// serve.go is the `ishakat serve` subcommand (docs/PLAN.md §11 Step 23):
// flag parsing and wiring into app.Serve, mirroring cmdModels/cmdPurge's own
// shape for a self-contained flag.FlagSet per subcommand.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/MichiTrader/ishakat/internal/app"
)

// cmdServe parses `ishakat serve`'s own flags and runs the WebSocket door
// until it is cancelled (Ctrl+C or a signal), returning the process exit
// code.
func cmdServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	addr := fs.String("addr", "", "listen address, overriding [serve].addr")
	token := fs.String("token", "", "bearer token clients must present, overriding [serve].token")
	allowToolCreate := fs.Bool("allow-tool-create", false, "let tool_create appear over this door (§19.7); creation still requires a permission_response")
	cfgPath := fs.String("config", "", "alternate config.toml path")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `usage: ishakat serve [--addr host:port] [--token secret] [--allow-tool-create] [--config path]

Opens an NDJSON-over-WebSocket socket another program (a voice model, n8n,
an editor plugin, cron) can drive the same agent loop through. See
docs/PLAN.md §11 Step 23 and §13 for the wire protocol.
`)
	}
	if err := fs.Parse(args); err != nil {
		return app.ExitUsage
	}

	var allowPtr *bool
	if *allowToolCreate {
		v := true
		allowPtr = &v
	}

	return app.Serve(app.ServeOptions{
		Version:         version,
		ConfigPath:      *cfgPath,
		Addr:            *addr,
		Token:           *token,
		AllowToolCreate: allowPtr,
	})
}
