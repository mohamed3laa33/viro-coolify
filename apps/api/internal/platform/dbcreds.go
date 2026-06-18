package platform

import (
	"crypto/rand"
	"encoding/hex"
	"regexp"
	"strings"
)

// defaultDBStorageGB is the built-in fallback persistent-volume size for a
// managed database when neither the request nor platform settings/config supply
// one. Kept small (1Gi) so a hobby DB is cheap; admin-tunable via
// VORTEX_DB_DEFAULT_STORAGE_GB / WithDBStorageDefault.
const defaultDBStorageGB = 1

// dbIdentSafe keeps a generated SQL identifier (db/user name) to a portable,
// engine-agnostic charset: lowercase alphanumerics and underscore, never
// leading with a digit.
var dbIdentSafe = regexp.MustCompile(`[^a-z0-9_]+`)

// dbIdent derives a safe SQL identifier from a database name, falling back to
// the given default when the name has no usable characters. The result always
// starts with a letter so it is valid across postgres/mysql/mongo.
func dbIdent(name, fallback string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = dbIdentSafe.ReplaceAllString(s, "_")
	s = strings.Trim(s, "_")
	for strings.Contains(s, "__") {
		s = strings.ReplaceAll(s, "__", "_")
	}
	if s == "" {
		return fallback
	}
	// SQL identifiers must not start with a digit.
	if s[0] >= '0' && s[0] <= '9' {
		s = "db_" + s
	}
	if len(s) > 48 {
		s = s[:48]
	}
	return s
}

// randomPassword returns a strong, URL-safe random password (hex of n bytes).
// crypto/rand failure is fatal to the caller (it returns the error), never a
// weak fallback.
func randomPassword(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// generateDBCredentials produces deterministic-by-shape credentials for a new
// managed database: a SQL-safe database name + user derived from the requested
// name, and a strong random password. The engine-appropriate env is wired
// separately by kube.DatabaseEnv.
func generateDBCredentials(name string) (dbName, user, password string, err error) {
	dbName = dbIdent(name, "app")
	user = dbIdent(name+"_user", "app_user")
	password, err = randomPassword(24) // 48 hex chars
	if err != nil {
		return "", "", "", err
	}
	return dbName, user, password, nil
}
