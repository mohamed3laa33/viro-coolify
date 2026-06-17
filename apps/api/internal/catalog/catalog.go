// Package catalog defines the kind taxonomy for Viro's one-click services /
// databases / apps catalog. The catalog entries themselves (templates) live in
// the control-plane store and are managed via the super-admin API; this package
// only holds the Kind classification the platform layer maps to Coolify calls.
package catalog

// Kind classifies a template so the platform layer knows how to provision it.
type Kind string

const (
	KindService  Kind = "service"  // managed application stacks (WordPress, Ghost, ...)
	KindDatabase Kind = "database" // standalone databases (Postgres, MySQL, ...)
	KindApp      Kind = "app"      // generic application (e.g. a docker image)
)
