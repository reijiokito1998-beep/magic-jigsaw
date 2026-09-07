package httpx

import (
	"net/http"
	"strconv"
)

// DefaultPageLimit matches the page size the app's lists use, so a client that
// asks to be paginated without naming a size gets a screenful.
const DefaultPageLimit = 8

// Page is a requested window over a list endpoint's results.
//
// A zero Limit means "no window": the endpoint returns the whole list. That is
// deliberate — it keeps every list endpoint answering exactly as it did before
// pagination existed for clients that have not been updated yet.
type Page struct {
	// Number is 1-based, and 1 whenever the caller named no page.
	Number int
	// Limit is rows per page; 0 means unlimited.
	Limit int
	// Offset is the row to start at, derived from Number and Limit.
	Offset int
}

// Requested reports whether the caller actually asked to be paginated.
func (p Page) Requested() bool { return p.Limit > 0 }

// HasMore reports whether rows remain after this window, given the full count.
func (p Page) HasMore(total int) bool {
	if !p.Requested() {
		return false
	}
	return p.Offset+p.Limit < total
}

// ParsePage reads ?page= and ?limit= off a list request.
//
// Both are optional. A missing, unparseable or non-positive limit means no
// pagination at all; anything above maxLimit is clamped rather than rejected,
// so a client asking for too much still gets a useful answer instead of a 400.
// `page` without `limit` falls back to [DefaultPageLimit], since asking for a
// page only makes sense once pages exist.
func ParsePage(r *http.Request, maxLimit int) Page {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	number, _ := strconv.Atoi(q.Get("page"))

	if limit <= 0 && number > 0 {
		limit = DefaultPageLimit
	}
	if limit <= 0 {
		return Page{Number: 1}
	}
	if maxLimit > 0 && limit > maxLimit {
		limit = maxLimit
	}
	if number < 1 {
		number = 1
	}
	return Page{Number: number, Limit: limit, Offset: (number - 1) * limit}
}
