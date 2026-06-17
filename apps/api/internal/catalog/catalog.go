// Package catalog defines Viro's one-click services / databases / apps catalog.
// Templates are static metadata; provisioning is handled by the platform service
// (which maps a template's Kind to the appropriate Coolify call).
package catalog

// Kind classifies a template so the platform layer knows how to provision it.
type Kind string

const (
	KindService  Kind = "service"  // managed application stacks (WordPress, Ghost, ...)
	KindDatabase Kind = "database" // standalone databases (Postgres, MySQL, ...)
	KindApp      Kind = "app"      // generic application (e.g. a docker image)
)

// Template is a catalog entry users can launch in one click.
type Template struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Kind        Kind   `json:"kind"`
}

// Templates is the full catalog.
var Templates = []Template{
	// Services.
	{Key: "wordpress", Name: "WordPress", Description: "The world's most popular CMS.", Category: "CMS", Kind: KindService},
	{Key: "ghost", Name: "Ghost", Description: "Modern publishing platform.", Category: "CMS", Kind: KindService},
	{Key: "plausible", Name: "Plausible", Description: "Privacy-friendly web analytics.", Category: "Analytics", Kind: KindService},
	{Key: "n8n", Name: "n8n", Description: "Workflow automation.", Category: "Automation", Kind: KindService},

	// Databases.
	{Key: "postgresql", Name: "PostgreSQL", Description: "Relational database.", Category: "Database", Kind: KindDatabase},
	{Key: "mysql", Name: "MySQL", Description: "Relational database.", Category: "Database", Kind: KindDatabase},
	{Key: "mariadb", Name: "MariaDB", Description: "MySQL-compatible relational database.", Category: "Database", Kind: KindDatabase},
	{Key: "mongodb", Name: "MongoDB", Description: "Document database.", Category: "Database", Kind: KindDatabase},
	{Key: "redis", Name: "Redis", Description: "In-memory key-value store.", Category: "Database", Kind: KindDatabase},

	// Generic app.
	{Key: "docker-image", Name: "Docker Image", Description: "Deploy any public Docker image.", Category: "App", Kind: KindApp},
}

// TemplateByKey returns the template with the given key, if any.
func TemplateByKey(key string) (Template, bool) {
	for _, t := range Templates {
		if t.Key == key {
			return t, true
		}
	}
	return Template{}, false
}
