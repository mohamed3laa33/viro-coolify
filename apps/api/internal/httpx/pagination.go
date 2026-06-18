package httpx

import (
	"net/http"
	"strconv"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/store"
)

// parsePage reads ?limit= and ?offset= from the request and returns a bounded
// store.Page (default store.DefaultPageLimit, capped at store.MaxPageLimit,
// offset >= 0). Invalid/absent values fall back to the defaults, so every
// growth-prone list endpoint is bounded even when the client sends nothing.
func parsePage(r *http.Request) store.Page {
	q := r.URL.Query()
	limit := 0
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	offset := 0
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			offset = n
		}
	}
	return store.Page{Limit: limit, Offset: offset}.Normalize()
}

// pageMeta builds the pagination block returned alongside a page of items. count
// is the number of items actually returned for this page; when it equals the
// requested limit there may be more, so it advertises the next offset. total, when
// known (>= 0), is included so a client can render exact counts; pass -1 to omit.
func pageMeta(p store.Page, count, total int) map[string]any {
	hasMore := count >= p.Limit
	if total >= 0 {
		hasMore = p.Offset+count < total
	}
	meta := map[string]any{
		"limit":   p.Limit,
		"offset":  p.Offset,
		"hasMore": hasMore,
	}
	if hasMore {
		meta["nextOffset"] = p.Offset + count
	}
	if total >= 0 {
		meta["total"] = total
	}
	return meta
}
