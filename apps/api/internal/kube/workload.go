package kube

import "strings"

func isWordPress(w Workload) bool {
	return strings.EqualFold(w.ServiceTemplateKey, "wordpress")
}

// defaultPort returns the container/service port for a workload.
func defaultPort(w Workload) int {
	switch strings.ToLower(w.ServiceTemplateKey) {
	case "wordpress":
		return 80
	case "redis":
		return 6379
	case "postgresql", "postgres":
		return 5432
	case "mysql", "mariadb":
		return 3306
	case "mongodb", "mongo":
		return 27017
	default:
		return 80
	}
}

// splitImage splits "repo:tag" into (repo, tag). When the image has no tag,
// tag defaults to "latest". A registry host port (e.g. registry:5000/img) is
// preserved by only splitting on the LAST colon when it follows the last slash.
func splitImage(image string) (repo, tag string) {
	image = strings.TrimSpace(image)
	if image == "" {
		return "nginx", "latest"
	}
	lastSlash := strings.LastIndex(image, "/")
	lastColon := strings.LastIndex(image, ":")
	if lastColon > lastSlash { // colon is part of the tag, not a registry port
		return image[:lastColon], image[lastColon+1:]
	}
	return image, "latest"
}

// mergeEnv builds the chart's deployment.env map. For WordPress it forces the
// resolved image and injects WORDPRESS_CONFIG_EXTRA so imports cannot overwrite
// the DB credentials / site URLs baked into wp-config.
func mergeEnv(w Workload) map[string]string {
	env := map[string]string{}
	for k, v := range w.Env {
		env[k] = v
	}
	if isWordPress(w) {
		env["WORDPRESS_CONFIG_EXTRA"] = wordpressConfigExtra(w.Env)
	}
	return env
}

// wordpressConfigExtra renders PHP appended to wp-config.php that hard-pins the
// DB connection and site URLs from the resolved Env, so a user import that
// rewrites the database can't break the managed wiring. Values come from the
// WORDPRESS_DB_* / WP_HOME / WP_SITEURL env the platform resolved.
func wordpressConfigExtra(env map[string]string) string {
	get := func(k string) string { return phpEscape(env[k]) }
	var b strings.Builder
	b.WriteString("if (!defined('DB_HOST')) define('DB_HOST', '" + get("WORDPRESS_DB_HOST") + "');\n")
	b.WriteString("define('DB_NAME', '" + get("WORDPRESS_DB_NAME") + "');\n")
	b.WriteString("define('DB_USER', '" + get("WORDPRESS_DB_USER") + "');\n")
	b.WriteString("define('DB_PASSWORD', '" + get("WORDPRESS_DB_PASSWORD") + "');\n")
	if home := env["WP_HOME"]; home != "" {
		b.WriteString("define('WP_HOME', '" + phpEscape(home) + "');\n")
		b.WriteString("define('WP_SITEURL', '" + phpEscape(home) + "');\n")
	}
	// Trust the reverse-proxy TLS termination at the shared Gateway.
	b.WriteString("if (isset($_SERVER['HTTP_X_FORWARDED_PROTO']) && $_SERVER['HTTP_X_FORWARDED_PROTO'] === 'https') { $_SERVER['HTTPS'] = 'on'; }\n")
	return b.String()
}

func phpEscape(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "'", "\\'")
	return s
}

// sanitizeDomains lowercases/trims custom hostnames and drops empties.
func sanitizeDomains(domains []string) []string {
	out := make([]string, 0, len(domains))
	for _, d := range domains {
		d = strings.ToLower(strings.TrimSpace(d))
		if d != "" {
			out = append(out, d)
		}
	}
	return out
}
