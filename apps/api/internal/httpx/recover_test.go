package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// TestRecovererTurnsPanicInto500 asserts that a handler panic is caught by
// chi's Recoverer (installed in routes()) and surfaced as a 500 response rather
// than a dropped connection / crashed process. This guards the invariant that no
// request handler can take down the API.
func TestRecovererTurnsPanicInto500(t *testing.T) {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Get("/boom", func(http.ResponseWriter, *http.Request) {
		panic("handler exploded")
	})

	req := httptest.NewRequest(http.MethodGet, "/boom", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (panic must be recovered, not dropped)", rec.Code)
	}
}

// TestDecodeJSONRejectsMalformedBody asserts decodeJSON never panics on bad
// input and instead returns false + a 400, regardless of the garbage supplied.
func TestDecodeJSONRejectsMalformedBody(t *testing.T) {
	cases := map[string]string{
		"not json":         "}{",
		"truncated":        `{"email":`,
		"unknown field":    `{"surprise":1}`,
		"trailing garbage": `{} trailing`,
		"empty":            "",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
			rec := httptest.NewRecorder()
			var dst struct {
				Email string `json:"email"`
			}
			if ok := decodeJSON(rec, req, &dst); ok {
				t.Fatalf("decodeJSON accepted malformed body %q", body)
			}
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 for malformed body %q", rec.Code, body)
			}
		})
	}
}
