package models

import (
	"time"

	"github.com/google/uuid"
)

// Roles.
const (
	RoleUser  = "user"
	RoleAdmin = "admin"
)

// Challenge result statuses ("Danh vọng").
const (
	StatusInProgress = "in_progress"
	StatusCompleted  = "completed"
	StatusFailed     = "failed"
)

// User is an application account.
type User struct {
	ID        uuid.UUID `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	Role      string    `json:"role"`
	XP        int       `json:"xp"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Achievement keys.
const (
	AchievementFirstPiece = "first_piece"
	AchievementSpeedDemon = "speed_demon"
	AchievementJigsawKing = "jigsaw_king"
)

// MaxLevel caps progression.
const MaxLevel = 100

// xpBase is the XP needed for the first level-up (level 1 → 2).
const xpBase = 100

// XPForLevelUp is the XP required to advance FROM the given level to the next,
// following XP(level) = Base × level² (Base = 100). Returns 0 at MaxLevel.
func XPForLevelUp(level int) int {
	if level >= MaxLevel {
		return 0
	}
	return xpBase * level * level
}

// LevelFromXP derives the current level, XP accumulated into the current level,
// and the XP needed to reach the next level, from a total XP amount.
func LevelFromXP(xp int) (level, xpIntoLevel, xpForNext int) {
	level = 1
	remaining := xp
	for level < MaxLevel {
		cost := XPForLevelUp(level)
		if remaining < cost {
			break
		}
		remaining -= cost
		level++
	}
	return level, remaining, XPForLevelUp(level)
}

// LevelTitle maps a level to a flavour title.
func LevelTitle(level int) string {
	switch {
	case level >= 100:
		return "Legend"
	case level >= 70:
		return "Grandmaster"
	case level >= 40:
		return "Master Puzzler"
	case level >= 20:
		return "Expert"
	case level >= 10:
		return "Skilled"
	case level >= 5:
		return "Apprentice"
	default:
		return "Novice"
	}
}

// Image is a Cloudinary-hosted picture used for puzzles.
type Image struct {
	ID       uuid.UUID `json:"id"`
	PublicID string    `json:"publicId"`
	URL      string    `json:"url"`
	// ThumbURL delivers the same picture resized and re-encoded for a list
	// row or grid tile. Clients showing a small copy should read this instead
	// of URL: it is typically a twentieth of the bytes.
	ThumbURL    string        `json:"thumbUrl,omitempty"`
	Width       int           `json:"width"`
	Height      int           `json:"height"`
	Title       string        `json:"title"`
	UploadedBy  uuid.NullUUID `json:"-"`
	IsChallenge bool          `json:"isChallenge"`
	CreatedAt   time.Time     `json:"createdAt"`
}

// CollectionItem is an image saved in a user's collection.
type CollectionItem struct {
	ID      uuid.UUID `json:"id"`
	AddedAt time.Time `json:"addedAt"`
	Image   Image     `json:"image"`
}

// DailyChallenge is the admin-provided puzzle for a given day.
type DailyChallenge struct {
	ID            uuid.UUID `json:"id"`
	ChallengeDate time.Time `json:"challengeDate"`
	PlaySeconds   int       `json:"playSeconds"`
	GridCols      int       `json:"gridCols"`
	GridRows      int       `json:"gridRows"`
	Category      string    `json:"category"`
	Title         string    `json:"title"`
	XPReward      int       `json:"xpReward"`

	// ChallengeEventsEnabled turns on the in-game disruption events (ink splash,
	// fog, earthquake, quick countdown) for this challenge. Same value for every
	// player attempting the day, so the shared leaderboard stays comparable.
	ChallengeEventsEnabled bool `json:"challengeEventsEnabled"`

	// The bonus quiz asked after the puzzle is solved: one question, two
	// options, one of them right. Optional — an empty question means the
	// manager configured no quiz for this challenge.
	QuizQuestion string `json:"quizQuestion"`
	QuizOptionA  string `json:"quizOptionA"`
	QuizOptionB  string `json:"quizOptionB"`

	// QuizCorrectOption is 0 (A) or 1 (B). Never serialized on the
	// player-facing payload — handing the client the answer key would make the
	// bonus point free. Admin responses use AdminChallengeView to expose it.
	QuizCorrectOption int `json:"-"`

	CreatedAt time.Time `json:"createdAt"`
	Image     Image     `json:"image"`
}

// Pieces is the total number of pieces in the challenge grid.
func (c DailyChallenge) Pieces() int { return c.GridCols * c.GridRows }

// HasQuiz reports whether this challenge carries a usable bonus question.
func (c DailyChallenge) HasQuiz() bool {
	return c.QuizQuestion != "" && c.QuizOptionA != "" && c.QuizOptionB != ""
}

// AdminChallengeView is a challenge as shown to a manager: the same payload
// plus the quiz answer key that players must not receive.
type AdminChallengeView struct {
	DailyChallenge
	QuizCorrectOption int `json:"quizCorrectOption"`
}

// AdminView wraps the challenge for the admin screens.
func (c DailyChallenge) AdminView() AdminChallengeView {
	return AdminChallengeView{DailyChallenge: c, QuizCorrectOption: c.QuizCorrectOption}
}

// ChallengeResult is a user's reputation entry for a daily challenge.
type ChallengeResult struct {
	ID             uuid.UUID  `json:"id"`
	ChallengeID    uuid.UUID  `json:"challengeId"`
	Status         string     `json:"status"`
	StartedAt      time.Time  `json:"startedAt"`
	FinishedAt     *time.Time `json:"finishedAt,omitempty"`
	ElapsedSeconds int        `json:"elapsedSeconds"`
	CorrectPieces  int        `json:"correctPieces"`

	// QuizAnswer is the option the user picked for the bonus question (0 or 1),
	// nil while unanswered. The bonus point is granted on the single
	// transition from nil to a value.
	QuizAnswer *int `json:"quizAnswer,omitempty"`
}

// Achievements is the unlocked state of each achievement.
type Achievements struct {
	FirstPiece bool `json:"firstPiece"`
	SpeedDemon bool `json:"speedDemon"`
	JigsawKing bool `json:"jigsawKing"`
}

// UserStats is the aggregated progression shown on Profile / Hall of Fame.
type UserStats struct {
	Level            int          `json:"level"`
	Title            string       `json:"title"`
	XP               int          `json:"xp"`
	XPIntoLevel      int          `json:"xpIntoLevel"`
	XPForNextLevel   int          `json:"xpForNextLevel"`
	Score            int          `json:"score"` // total correct pieces (ranking)
	Rank             int          `json:"rank"`
	PuzzlesSolved    int          `json:"puzzlesSolved"`
	SolvedThisWeek   int          `json:"solvedThisWeek"`
	TotalTimeSeconds int          `json:"totalTimeSeconds"`
	HighestScore     int          `json:"highestScore"` // total correct pieces (cumulative)
	CompletionRate   int          `json:"completionRate"`
	Achievements     Achievements `json:"achievements"`
}

// LeaderboardEntry is one row of the global ranking.
type LeaderboardEntry struct {
	Rank   int       `json:"rank"`
	UserID uuid.UUID `json:"userId"`
	Name   string    `json:"name"`
	Score  int       `json:"score"`
	Level  int       `json:"level"`
	IsMe   bool      `json:"isMe"`
}

// CheckinState is the daily check-in calendar view for a user.
type CheckinState struct {
	UnlockPoints   int      `json:"unlockPoints"`
	Streak         int      `json:"streak"`
	CheckedInToday bool     `json:"checkedInToday"`
	WeekCheckedIn  int      `json:"weekCheckedIn"`
	WeekTotal      int      `json:"weekTotal"`
	CheckinDates   []string `json:"checkinDates"`   // "YYYY-MM-DD"
	ChallengeDates []string `json:"challengeDates"` // days a daily challenge was completed
}

// HistoryItem is a played challenge for the "Recent Challenges" list.
type HistoryItem struct {
	ChallengeID uuid.UUID `json:"challengeId"`
	Title       string    `json:"title"`
	ImageURL    string    `json:"imageUrl"`
	// ThumbURL is [ImageURL] sized for the Recent Challenges strip.
	ThumbURL       string     `json:"thumbUrl,omitempty"`
	ImageID        uuid.UUID  `json:"imageId"`
	Pieces         int        `json:"pieces"`
	GridCols       int        `json:"gridCols"`
	GridRows       int        `json:"gridRows"`
	Status         string     `json:"status"`
	ElapsedSeconds int        `json:"elapsedSeconds"`
	CorrectPieces  int        `json:"correctPieces"`
	FinishedAt     *time.Time `json:"finishedAt,omitempty"`
}

// ChallengeView bundles a challenge with the requesting user's result (if any).
type ChallengeView struct {
	DailyChallenge
	Result *ChallengeResult `json:"result,omitempty"`
}

// Story is a themed collection of ordered image "pages" (a narrative book).
type Story struct {
	ID          uuid.UUID `json:"id"`
	Title       string    `json:"title"`
	Category    string    `json:"category"`
	Author      string    `json:"author"`
	Description string    `json:"description"`
	CoverURL    string    `json:"coverUrl"`
	// CoverThumbURL is [CoverURL] sized for the library's cover cards.
	CoverThumbURL string `json:"coverThumbUrl,omitempty"`
	// LevelRequired is the player level needed before the story can be played.
	// 1 means open to everyone.
	LevelRequired  int       `json:"levelRequired"`
	Unlocked       bool      `json:"unlocked"` // for the requesting user
	TotalPages     int       `json:"totalPages"`
	CompletedPages int       `json:"completedPages"` // for the requesting user
	EarnedXP       int       `json:"earnedXp"`       // xp earned from this story
	CreatedAt      time.Time `json:"createdAt"`
}

// StoryPage is a single page (puzzle) inside a story.
type StoryPage struct {
	ID          uuid.UUID `json:"id"`
	StoryID     uuid.UUID `json:"storyId"`
	Index       int       `json:"index"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Image       Image     `json:"image"`
	GridCols    int       `json:"gridCols"`
	GridRows    int       `json:"gridRows"`
	PlaySeconds int       `json:"playSeconds"`
	XPReward    int       `json:"xpReward"`
	Unlocked    bool      `json:"unlocked"`  // for the requesting user
	Completed   bool      `json:"completed"` // for the requesting user
}

// StoryDetail is a story plus its ordered pages (with per-user unlock state).
type StoryDetail struct {
	Story
	Pages []StoryPage `json:"pages"`
}
