package api

import (
	"github.com/reijiokito/jigsaw-backend/internal/httpx"
	"github.com/reijiokito/jigsaw-backend/internal/models"
)

// maxPageLimit caps what one request may pull from any list endpoint. Well
// above the app's page size, so a client can batch when it needs to, but low
// enough that a stray `?limit=100000` cannot turn into a full table scan plus
// a multi-megabyte response.
const maxPageLimit = 100

// Windows that existed before pagination did, kept as the default when a
// caller sends no limit — so an un-updated client sees no change at all.
const (
	defaultHistoryLimit       = 20
	maxHistoryLimit           = 50
	defaultChallengeListLimit = 30
)

// pageMeta travels alongside a paginated list.
//
// It is added to the existing response objects rather than wrapping them, so
// the array key each client already reads (`images`, `stories`, ...) stays put
// and a client that never sends `page`/`limit` sees exactly what it always did.
type pageMeta struct {
	// Page is 1-based; omitted when the caller asked for no page.
	Page int `json:"page,omitempty"`
	// Limit is the window size actually applied; omitted when unpaginated.
	Limit int `json:"limit,omitempty"`
	// Total is the row count across every page.
	Total int `json:"total"`
	// HasMore says whether another page exists — what an infinite-scrolling
	// client actually needs, so it never has to do the arithmetic.
	HasMore bool `json:"hasMore"`
}

// metaFor builds the metadata for a page of a list holding total rows.
func metaFor(p httpx.Page, total int) pageMeta {
	m := pageMeta{Total: total, HasMore: p.HasMore(total)}
	if p.Requested() {
		m.Page = p.Number
		m.Limit = p.Limit
	}
	return m
}

// collectionResponse is the payload of GET /collection.
type collectionResponse struct {
	Items []models.CollectionItem `json:"items"`
	pageMeta
}

// challengesResponse is the payload of the challenge list endpoints.
type challengesResponse struct {
	Challenges []models.DailyChallenge `json:"challenges"`
	pageMeta
}

// adminChallengesResponse is the manager-facing challenge list: same shape,
// plus each challenge's quiz answer key.
type adminChallengesResponse struct {
	Challenges []models.AdminChallengeView `json:"challenges"`
	pageMeta
}

// resultsResponse is the payload of GET /me/results.
type resultsResponse struct {
	Results []models.ChallengeResult `json:"results"`
}
