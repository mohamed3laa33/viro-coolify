package catalog

import "testing"

func TestTemplateByKey(t *testing.T) {
	tmpl, ok := TemplateByKey("wordpress")
	if !ok {
		t.Fatal("expected wordpress in catalog")
	}
	if tmpl.Kind != KindService {
		t.Fatalf("wordpress kind = %q, want service", tmpl.Kind)
	}

	db, ok := TemplateByKey("postgresql")
	if !ok || db.Kind != KindDatabase {
		t.Fatalf("postgresql lookup: ok=%v kind=%q", ok, db.Kind)
	}

	app, ok := TemplateByKey("docker-image")
	if !ok || app.Kind != KindApp {
		t.Fatalf("docker-image lookup: ok=%v kind=%q", ok, app.Kind)
	}

	if _, ok := TemplateByKey("does-not-exist"); ok {
		t.Fatal("expected unknown key to return ok=false")
	}
}

func TestCatalogNonEmpty(t *testing.T) {
	if len(Templates) < 10 {
		t.Fatalf("expected a fuller catalog, got %d", len(Templates))
	}
}
