package api

import (
	"log"
	"net/http"
	"time"

	"github.com/reijiokito/jigsaw-backend/internal/httpx"
	"github.com/reijiokito/jigsaw-backend/internal/metrics"
	"github.com/reijiokito/jigsaw-backend/internal/models"
)

const dateLayout = "2006-01-02"

// clientDay returns the calendar day the request is anchored to. Clients send
// their local day via ?date=YYYY-MM-DD so a check-in isn't attributed to the
// wrong day when the device is in a non-UTC timezone. Falls back to UTC today
// when the param is missing or malformed. The client-supplied date is only
// trusted for today or yesterday (UTC) - anything further away (e.g. a
// forged past/future date used to farm extra check-ins) is clamped back to
// today, since the date is otherwise unauthenticated client input.
func clientDay(r *http.Request) time.Time {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	if v := r.URL.Query().Get("date"); v != "" {
		if d, err := time.Parse(dateLayout, v); err == nil {
			d = d.UTC()
			yesterday := today.AddDate(0, 0, -1)
			if d.Equal(today) || d.Equal(yesterday) {
				return d
			}
		}
	}
	return today
}

func (s *Server) buildCheckinState(w http.ResponseWriter, r *http.Request) (*models.CheckinState, bool) {
	ctx := r.Context()
	uid := httpx.UserIDFrom(ctx)

	now := clientDay(r)
	today := now.Format(dateLayout)
	since := now.AddDate(0, 0, -180).Format(dateLayout)
	log.Printf("[DEBUG] buildCheckinState: start userID=%s today=%s", uid, today)

	points, err := s.store.GetUnlockPoints(ctx, uid)
	if err != nil {
		log.Printf("[DEBUG] buildCheckinState: error GetUnlockPoints userID=%s: %v", uid, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load points")
		return nil, false
	}
	checkins, err := s.store.GetCheckinDates(ctx, uid, since)
	if err != nil {
		log.Printf("[DEBUG] buildCheckinState: error GetCheckinDates userID=%s: %v", uid, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load check-ins")
		return nil, false
	}
	challenges, err := s.store.GetChallengeCompletionDates(ctx, uid, since)
	if err != nil {
		log.Printf("[DEBUG] buildCheckinState: error GetChallengeCompletionDates userID=%s: %v", uid, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load challenges")
		return nil, false
	}

	set := make(map[string]bool, len(checkins))
	for _, d := range checkins {
		set[d] = true
	}

	// Current streak: consecutive days ending today (or yesterday if not yet
	// checked in today).
	streak := 0
	cursor := now
	if !set[today] {
		cursor = cursor.AddDate(0, 0, -1)
	}
	for set[cursor.Format(dateLayout)] {
		streak++
		cursor = cursor.AddDate(0, 0, -1)
	}

	// Check-ins in the current week (Mon..Sun).
	weekday := int(now.Weekday()) // Sun=0..Sat=6
	offsetToMonday := (weekday + 6) % 7
	monday := now.AddDate(0, 0, -offsetToMonday)
	weekCount := 0
	for i := 0; i < 7; i++ {
		if set[monday.AddDate(0, 0, i).Format(dateLayout)] {
			weekCount++
		}
	}

	log.Printf("[DEBUG] buildCheckinState: success userID=%s points=%d streak=%d checkedInToday=%t", uid, points, streak, set[today])
	return &models.CheckinState{
		UnlockPoints:   points,
		Streak:         streak,
		CheckedInToday: set[today],
		WeekCheckedIn:  weekCount,
		WeekTotal:      7,
		CheckinDates:   checkins,
		ChallengeDates: challenges,
	}, true
}

// handleGetCheckin returns the check-in calendar state.
//
// @Summary      Check-in calendar
// @Description  Unlock points, streak and check-in/challenge dates for the calendar.
// @Tags         checkin
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  models.CheckinState
// @Failure      401  {object}  httpx.ErrorBody
// @Router       /api/v1/checkin [get]
func (s *Server) handleGetCheckin(w http.ResponseWriter, r *http.Request) {
	log.Printf("[DEBUG] handleGetCheckin: start userID=%s", httpx.UserIDFrom(r.Context()))
	state, ok := s.buildCheckinState(w, r)
	if !ok {
		return
	}
	httpx.JSON(w, http.StatusOK, state)
}

// handleCheckin performs today's check-in (idempotent) and grants +1 unlock
// point on the first check-in of the day.
//
// @Summary      Check in today
// @Tags         checkin
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  models.CheckinState
// @Failure      401  {object}  httpx.ErrorBody
// @Router       /api/v1/checkin [post]
func (s *Server) handleCheckin(w http.ResponseWriter, r *http.Request) {
	uid := httpx.UserIDFrom(r.Context())
	today := clientDay(r).Format(dateLayout)
	log.Printf("[DEBUG] handleCheckin: start userID=%s date=%s", uid, today)
	if _, err := s.store.CheckIn(r.Context(), uid, today); err != nil {
		log.Printf("[DEBUG] handleCheckin: error CheckIn userID=%s date=%s: %v", uid, today, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not check in")
		return
	}
	metrics.GameEventsTotal.WithLabelValues("checkin", "complete").Inc()
	log.Printf("[DEBUG] handleCheckin: success userID=%s date=%s", uid, today)
	state, ok := s.buildCheckinState(w, r)
	if !ok {
		return
	}
	httpx.JSON(w, http.StatusOK, state)
}
