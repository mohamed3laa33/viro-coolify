package catalog

import "testing"

func TestKindValues(t *testing.T) {
	if KindService != "service" || KindDatabase != "database" || KindApp != "app" {
		t.Fatalf("unexpected kind values: %q %q %q", KindService, KindDatabase, KindApp)
	}
}
