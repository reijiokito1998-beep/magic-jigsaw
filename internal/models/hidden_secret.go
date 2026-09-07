package models

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// HiddenSecretTileCols and HiddenSecretTileRows are the fixed dimensions of the
// shutter wall covering a hidden picture: 3 columns by 3 rows.
const HiddenSecretTileCols = 3
const HiddenSecretTileRows = 3

// HiddenSecretTileCount is how many shutters hide a picture, and so how many
// puzzles must be solved to reveal it.
const HiddenSecretTileCount = HiddenSecretTileCols * HiddenSecretTileRows

// HiddenSecretPointsReward is the unlock points uncovering a whole picture pays,
// once. Unlike the XP reward it is not admin-authored: it is the same for every
// picture, and reported to clients so they can advertise it up front.
const HiddenSecretPointsReward = 5

// HiddenSecretMinPuzzleGrid and HiddenSecretMaxPuzzleGrid bound the per-tile
// puzzle grid an admin may configure. Kept in sync with the dungeon range so
// the same admin-facing grid presets are accepted everywhere.
const HiddenSecretMinPuzzleGrid = 2
const HiddenSecretMaxPuzzleGrid = 20

// TileIndexInRange reports whether i addresses one of the 9 shutters.
func TileIndexInRange(i int) bool {
	return i >= 0 && i < HiddenSecretTileCount
}

// TileRowCol splits a row-major tile index into its grid position.
func TileRowCol(i int) (row, col int) {
	return i / HiddenSecretTileCols, i % HiddenSecretTileCols
}

// HiddenSecret is a summary of a hidden picture for listing, personalized with
// the caller's reveal progress. The image URL is included so the list can show
// the already-revealed tiles as a teaser; a caller that has revealed nothing
// simply renders no crops.
type HiddenSecret struct {
	ID       uuid.UUID `json:"id"`
	Title    string    `json:"title"`
	Subtitle string    `json:"subtitle"`
	ImageID  uuid.UUID `json:"imageId"`
	ImageURL string    `json:"imageUrl"`
	// ThumbURL is [ImageURL] sized for the list row.
	ThumbURL      string `json:"thumbUrl,omitempty"`
	XPReward      int    `json:"xpReward"`
	PointsReward  int    `json:"pointsReward"`
	TileCount     int    `json:"tileCount"`
	RevealedCount int    `json:"revealedCount"`
	Active        bool   `json:"active"`
	Completed     bool   `json:"completed"`
}

// HiddenSecretProgress is a user's reveal state for one hidden picture.
type HiddenSecretProgress struct {
	Revealed  []int `json:"revealed"`
	Completed bool  `json:"completed"`
}

// HiddenSecretDetail is everything the reveal board needs: the picture, the
// per-tile puzzle settings shared by all 9 shutters, and the caller's progress.
type HiddenSecretDetail struct {
	ID              uuid.UUID            `json:"id"`
	Title           string               `json:"title"`
	Subtitle        string               `json:"subtitle"`
	ImageID         uuid.UUID            `json:"imageId"`
	ImageURL        string               `json:"imageUrl"`
	XPReward        int                  `json:"xpReward"`
	PointsReward    int                  `json:"pointsReward"`
	TileCols        int                  `json:"tileCols"`
	TileRows        int                  `json:"tileRows"`
	TileGridCols    int                  `json:"tileGridCols"`
	TileGridRows    int                  `json:"tileGridRows"`
	TilePlaySeconds int                  `json:"tilePlaySeconds"`
	Progress        HiddenSecretProgress `json:"progress"`
}

// AdminHiddenSecret is a hidden picture as the admin editor sees it — no
// per-user progress.
type AdminHiddenSecret struct {
	ID       uuid.UUID `json:"id"`
	Title    string    `json:"title"`
	Subtitle string    `json:"subtitle"`
	ImageID  uuid.UUID `json:"imageId"`
	ImageURL string    `json:"imageUrl"`
	// ThumbURL is [ImageURL] sized for the admin list row.
	ThumbURL        string `json:"thumbUrl,omitempty"`
	XPReward        int    `json:"xpReward"`
	TileGridCols    int    `json:"tileGridCols"`
	TileGridRows    int    `json:"tileGridRows"`
	TilePlaySeconds int    `json:"tilePlaySeconds"`
	Active          bool   `json:"active"`
}

// HiddenSecretUnlock is one fully revealed picture in a user's gallery.
type HiddenSecretUnlock struct {
	SecretID   uuid.UUID `json:"secretId"`
	Title      string    `json:"title"`
	ImageID    uuid.UUID `json:"imageId"`
	ImageURL   string    `json:"imageUrl"`
	UnlockedAt time.Time `json:"unlockedAt"`
}

// HiddenSecretConfig is the admin-authored shape of a hidden picture, validated
// by ValidateHiddenSecret before being persisted.
type HiddenSecretConfig struct {
	Title           string
	Subtitle        string
	ImageID         uuid.UUID
	XPReward        int
	TileGridCols    int
	TileGridRows    int
	TilePlaySeconds int
}

// ValidateHiddenSecret checks an admin-authored hidden picture: a title, a
// picture to hide, a positive XP reward, and a per-tile puzzle grid and timer
// within range. It does not check image existence (a DB concern left to the
// caller).
func ValidateHiddenSecret(c HiddenSecretConfig) error {
	if c.Title == "" {
		return errors.New("title is required")
	}
	if c.ImageID == uuid.Nil {
		return errors.New("imageId is required")
	}
	if c.XPReward <= 0 {
		return errors.New("xpReward must be positive")
	}
	if c.TileGridCols < HiddenSecretMinPuzzleGrid || c.TileGridCols > HiddenSecretMaxPuzzleGrid ||
		c.TileGridRows < HiddenSecretMinPuzzleGrid || c.TileGridRows > HiddenSecretMaxPuzzleGrid {
		return fmt.Errorf("tileGridCols/tileGridRows must be between %d and %d",
			HiddenSecretMinPuzzleGrid, HiddenSecretMaxPuzzleGrid)
	}
	if c.TilePlaySeconds <= 0 {
		return errors.New("tilePlaySeconds must be positive")
	}
	return nil
}
