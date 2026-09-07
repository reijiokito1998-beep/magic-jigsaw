package api

import (
	"log"
	"net/http"

	"github.com/reijiokito/jigsaw-backend/internal/httpx"
	"github.com/reijiokito/jigsaw-backend/internal/models"
)

type leaderboardResponse struct {
	Entries []models.LeaderboardEntry `json:"entries"`
	Me      *models.LeaderboardEntry  `json:"me,omitempty"`
}

type historyResponse struct {
	Challenges []models.HistoryItem `json:"challenges"`
	pageMeta
}

// handleMyStats returns the aggregated progression for Profile / Hall of Fame.
//
// @Summary      My stats
// @Description  Level, XP, score, rank, completion rate and achievements for the current user.
// @Tags         me
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  models.UserStats
// @Failure      401  {object}  httpx.ErrorBody
// @Failure      500  {object}  httpx.ErrorBody
// @Router       /api/v1/me/stats [get]
func (s *Server) handleMyStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := httpx.UserIDFrom(ctx)
	log.Printf("[DEBUG] handleMyStats: start userID=%s", uid)

	user, err := s.store.GetUser(ctx, uid)
	if err != nil {
		log.Printf("[DEBUG] handleMyStats: error GetUser userID=%s: %v", uid, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load user")
		return
	}
	agg, err := s.store.GetAggregateStats(ctx, uid)
	if err != nil {
		log.Printf("[DEBUG] handleMyStats: error GetAggregateStats userID=%s: %v", uid, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load stats")
		return
	}
	rank, err := s.store.GetRank(ctx, uid)
	if err != nil {
		log.Printf("[DEBUG] handleMyStats: error GetRank userID=%s: %v", uid, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not compute rank")
		return
	}

	// Award (persist) achievements that are currently satisfied.
	if agg.Score > 0 {
		_ = s.store.AwardAchievement(ctx, uid, models.AchievementFirstPiece)
	}
	if agg.SpeedDemon {
		_ = s.store.AwardAchievement(ctx, uid, models.AchievementSpeedDemon)
	}
	if rank == 1 {
		_ = s.store.AwardAchievement(ctx, uid, models.AchievementJigsawKing)
	}
	unlocked, _ := s.store.GetAchievements(ctx, uid)

	level, into, next := models.LevelFromXP(user.XP)
	completion := 0
	if agg.Played > 0 {
		completion = agg.PuzzlesSolved * 100 / agg.Played
	}

	log.Printf("[DEBUG] handleMyStats: success userID=%s level=%d rank=%d", uid, level, rank)
	httpx.JSON(w, http.StatusOK, models.UserStats{
		Level:            level,
		Title:            models.LevelTitle(level),
		XP:               user.XP,
		XPIntoLevel:      into,
		XPForNextLevel:   next,
		Score:            agg.Score,
		Rank:             rank,
		PuzzlesSolved:    agg.PuzzlesSolved,
		SolvedThisWeek:   agg.SolvedThisWeek,
		TotalTimeSeconds: agg.TotalTime,
		HighestScore:     agg.HighestScore,
		CompletionRate:   completion,
		Achievements: models.Achievements{
			FirstPiece: unlocked[models.AchievementFirstPiece],
			SpeedDemon: unlocked[models.AchievementSpeedDemon],
			JigsawKing: unlocked[models.AchievementJigsawKing],
		},
	})
}

// handleLeaderboard returns the global ranking plus the caller's entry.
//
// @Summary      Global ranking
// @Description  Top players by score (total correctly-placed pieces) plus the caller's rank.
// @Tags         me
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  leaderboardResponse
// @Failure      401  {object}  httpx.ErrorBody
// @Router       /api/v1/leaderboard [get]
func (s *Server) handleLeaderboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := httpx.UserIDFrom(ctx)
	log.Printf("[DEBUG] handleLeaderboard: start userID=%s", uid)

	entries, err := s.store.GetLeaderboard(ctx, 10, uid)
	if err != nil {
		log.Printf("[DEBUG] handleLeaderboard: error GetLeaderboard userID=%s: %v", uid, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load leaderboard")
		return
	}

	// Persist Jigsaw King for the current #1.
	if len(entries) > 0 && entries[0].Score > 0 {
		_ = s.store.AwardAchievement(ctx, entries[0].UserID, models.AchievementJigsawKing)
	}

	var me *models.LeaderboardEntry
	for i := range entries {
		if entries[i].IsMe {
			me = &entries[i]
			break
		}
	}
	if me == nil {
		rank, _ := s.store.GetRank(ctx, uid)
		agg, _ := s.store.GetAggregateStats(ctx, uid)
		user, _ := s.store.GetUser(ctx, uid)
		level, _, _ := models.LevelFromXP(user.XP)
		me = &models.LeaderboardEntry{
			Rank:   rank,
			UserID: uid,
			Name:   user.Name,
			Score:  agg.Score,
			Level:  level,
			IsMe:   true,
		}
	}

	log.Printf("[DEBUG] handleLeaderboard: success userID=%s entries=%d", uid, len(entries))
	httpx.JSON(w, http.StatusOK, leaderboardResponse{Entries: entries, Me: me})
}

// handleMyHistory returns the caller's recently played challenges.
//
// @Summary      My challenge history
// @Description  Recently played challenges (for the "Recent Challenges" list).
// @Tags         me
// @Produce      json
// @Security     BearerAuth
// @Param        page   query  int  false  "Page number (1-based); needs limit or defaults to 8"
// @Param        limit  query  int  false  "Rows per page; omit for the whole list"
// @Success      200  {object}  historyResponse
// @Failure      401  {object}  httpx.ErrorBody
// @Router       /api/v1/me/history [get]
func (s *Server) handleMyHistory(w http.ResponseWriter, r *http.Request) {
	uid := httpx.UserIDFrom(r.Context())
	// History has always been capped rather than unbounded, so an absent limit
	// keeps the historical 20-row window instead of meaning "everything".
	page := httpx.ParsePage(r, maxHistoryLimit)
	limit := page.Limit
	if limit == 0 {
		limit = defaultHistoryLimit
	}
	log.Printf("[DEBUG] handleMyHistory: start userID=%s page=%d limit=%d", uid, page.Number, limit)
	items, total, err := s.store.ListHistory(r.Context(), uid, limit, page.Offset)
	if err != nil {
		log.Printf("[DEBUG] handleMyHistory: error ListHistory userID=%s: %v", uid, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load history")
		return
	}
	log.Printf("[DEBUG] handleMyHistory: success userID=%s count=%d total=%d", uid, len(items), total)
	httpx.JSON(w, http.StatusOK, historyResponse{Challenges: s.decorateHistory(items), pageMeta: metaFor(page, total)})
}
