// Command vortex is the command-line interface for the Vortex hosting platform.
// It mirrors the fly.io / flyctl UX: auth, orgs, projects, apps, services,
// secrets, plans and version commands over the Vortex control-plane HTTP API.
package main

import (
	"os"

	"github.com/mohamed3laa33/viro-coolify/apps/cli/internal/cmd"
)

func main() {
	os.Exit(cmd.Execute())
}
