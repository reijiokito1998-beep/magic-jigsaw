package models

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// DungeonGridCols and DungeonGridRows are the fixed grid dimensions for
// every dungeon (6 columns wide, 8 rows tall).
const DungeonGridCols = 6
const DungeonGridRows = 8

// DungeonMinMonsterCount and DungeonMaxMonsterCount bound how many monsters an
// admin may place in a dungeon. The real limit is structural (monsters need
// distinct path cells that are neither the start nor the treasure); the maximum
// is only a sanity cap against a degenerate admin input.
const DungeonMinMonsterCount = 1
const DungeonMaxMonsterCount = 20

// DungeonMinPuzzleGrid and DungeonMaxPuzzleGrid bound the puzzle grid an admin
// may configure for a monster or treasure encounter. Kept in sync with the
// Expedition config range so the same admin-facing grid presets (up to 8x12)
// are accepted everywhere.
const DungeonMinPuzzleGrid = 2
const DungeonMaxPuzzleGrid = 20

// Cell is a single coordinate on a dungeon's grid.
type Cell struct {
	Row int `json:"row"`
	Col int `json:"col"`
}

// Dungeon is a summary of a dungeon for listing, personalized with the
// caller's progress.
type Dungeon struct {
	ID           uuid.UUID `json:"id"`
	Title        string    `json:"title"`
	XPReward     int       `json:"xpReward"`
	MonsterCount int       `json:"monsterCount"`
	Active       bool      `json:"active"`
	Started      bool      `json:"started"`
	Completed    bool      `json:"completed"`
}

// DungeonMonster is a monster placed on a dungeon's road cell.
type DungeonMonster struct {
	ID          uuid.UUID `json:"id"`
	Row         int       `json:"row"`
	Col         int       `json:"col"`
	GridCols    int       `json:"gridCols"`
	GridRows    int       `json:"gridRows"`
	PlaySeconds int       `json:"playSeconds"`
	ImageID     uuid.UUID `json:"imageId"`
	ImageURL    string    `json:"imageUrl"`
	Defeated    bool      `json:"defeated"`
}

// DungeonProgress is a user's saved position/state within a dungeon.
type DungeonProgress struct {
	PosRow    int      `json:"posRow"`
	PosCol    int      `json:"posCol"`
	Defeated  []string `json:"defeated"`
	Visited   []Cell   `json:"visited"`
	Completed bool     `json:"completed"`
}

// DungeonDetail is a dungeon's full layout plus the caller's progress and
// current energy (unlock points).
type DungeonDetail struct {
	ID                  uuid.UUID        `json:"id"`
	Title               string           `json:"title"`
	XPReward            int              `json:"xpReward"`
	GridCols            int              `json:"gridCols"`
	GridRows            int              `json:"gridRows"`
	Start               Cell             `json:"start"`
	Treasure            Cell             `json:"treasure"`
	Path                []Cell           `json:"path"`
	Monsters            []DungeonMonster `json:"monsters"`
	Progress            DungeonProgress  `json:"progress"`
	Energy              int              `json:"energy"`
	TreasureImageID     uuid.UUID        `json:"treasureImageId"`
	TreasureImageURL    string           `json:"treasureImageUrl"`
	TreasureGridCols    int              `json:"treasureGridCols"`
	TreasureGridRows    int              `json:"treasureGridRows"`
	TreasurePlaySeconds int              `json:"treasurePlaySeconds"`
}

// AdminDungeon is a full dungeon layout for the admin editor (no per-user
// progress/energy).
type AdminDungeon struct {
	ID                  uuid.UUID        `json:"id"`
	Title               string           `json:"title"`
	XPReward            int              `json:"xpReward"`
	Active              bool             `json:"active"`
	GridCols            int              `json:"gridCols"`
	GridRows            int              `json:"gridRows"`
	Start               Cell             `json:"start"`
	Treasure            Cell             `json:"treasure"`
	Path                []Cell           `json:"path"`
	Monsters            []DungeonMonster `json:"monsters"`
	TreasureImageID     uuid.UUID        `json:"treasureImageId"`
	TreasureImageURL    string           `json:"treasureImageUrl"`
	TreasureGridCols    int              `json:"treasureGridCols"`
	TreasureGridRows    int              `json:"treasureGridRows"`
	TreasurePlaySeconds int              `json:"treasurePlaySeconds"`
}

// DungeonMonsterInput is the admin-supplied definition of one monster when
// creating/updating a dungeon.
type DungeonMonsterInput struct {
	Row         int       `json:"row"`
	Col         int       `json:"col"`
	ImageID     uuid.UUID `json:"imageId"`
	GridCols    int       `json:"gridCols"`
	GridRows    int       `json:"gridRows"`
	PlaySeconds int       `json:"playSeconds"`
}

// DungeonTreasureUnlock is one unlocked treasure image in a user's dungeon
// treasure gallery: the completed dungeon and the trophy image it awarded.
type DungeonTreasureUnlock struct {
	DungeonID    uuid.UUID `json:"dungeonId"`
	DungeonTitle string    `json:"dungeonTitle"`
	ImageID      uuid.UUID `json:"imageId"`
	ImageURL     string    `json:"imageUrl"`
	UnlockedAt   time.Time `json:"unlockedAt"`
}

// DungeonLayout is the admin-authored shape of a dungeon, validated by
// ValidateDungeonLayout before being persisted.
type DungeonLayout struct {
	Start               Cell
	Treasure            Cell
	Path                []Cell
	Monsters            []DungeonMonsterInput
	TreasureImageID     uuid.UUID
	TreasureGridCols    int
	TreasureGridRows    int
	TreasurePlaySeconds int
}

// cellInBounds reports whether c lies within the fixed dungeon grid.
func cellInBounds(c Cell) bool {
	return c.Row >= 0 && c.Row < DungeonGridRows && c.Col >= 0 && c.Col < DungeonGridCols
}

// cellsAdjacent reports whether a and b are 4-adjacent (Manhattan distance 1).
func cellsAdjacent(a, b Cell) bool {
	dr := a.Row - b.Row
	if dr < 0 {
		dr = -dr
	}
	dc := a.Col - b.Col
	if dc < 0 {
		dc = -dc
	}
	return dr+dc == 1
}

// puzzleGridInRange reports whether an encounter's puzzle grid is within the
// admin-configurable range.
func puzzleGridInRange(cols, rows int) bool {
	return cols >= DungeonMinPuzzleGrid && cols <= DungeonMaxPuzzleGrid &&
		rows >= DungeonMinPuzzleGrid && rows <= DungeonMaxPuzzleGrid
}

// ValidateDungeonLayout checks the structural rules for an admin-authored
// dungeon: cells within bounds, a connected duplicate-free path from start to
// treasure, and between DungeonMinMonsterCount and DungeonMaxMonsterCount
// monsters (inclusive) placed on distinct, non-endpoint path cells with sane
// grid/time settings. When a treasure puzzle
// image is set, its grid/time settings are validated with the same rules as
// monsters; a nil treasure image is left unchecked so legacy dungeons stay
// valid. It does not check image existence (a DB concern left to the caller).
func ValidateDungeonLayout(l DungeonLayout) error {
	if !cellInBounds(l.Start) {
		return errors.New("start cell is out of bounds")
	}
	if !cellInBounds(l.Treasure) {
		return errors.New("treasure cell is out of bounds")
	}
	if len(l.Path) < 2 {
		return errors.New("path must contain at least 2 cells")
	}

	seen := make(map[Cell]bool, len(l.Path))
	for i, c := range l.Path {
		if !cellInBounds(c) {
			return fmt.Errorf("path cell %d is out of bounds", i)
		}
		if seen[c] {
			return fmt.Errorf("path contains duplicate cell (%d,%d)", c.Row, c.Col)
		}
		seen[c] = true
		if i > 0 && !cellsAdjacent(l.Path[i-1], c) {
			return fmt.Errorf("path cells %d and %d are not adjacent", i-1, i)
		}
	}
	if l.Path[0] != l.Start {
		return errors.New("path must start at the start cell")
	}
	if l.Path[len(l.Path)-1] != l.Treasure {
		return errors.New("path must end at the treasure cell")
	}

	if len(l.Monsters) < DungeonMinMonsterCount || len(l.Monsters) > DungeonMaxMonsterCount {
		return fmt.Errorf("between %d and %d monsters are required", DungeonMinMonsterCount, DungeonMaxMonsterCount)
	}
	monsterCells := make(map[Cell]bool, len(l.Monsters))
	for i, m := range l.Monsters {
		c := Cell{Row: m.Row, Col: m.Col}
		if !cellInBounds(c) {
			return fmt.Errorf("monster %d cell is out of bounds", i)
		}
		if !seen[c] {
			return fmt.Errorf("monster %d must be placed on the path", i)
		}
		if c == l.Start || c == l.Treasure {
			return fmt.Errorf("monster %d cannot be on the start or treasure cell", i)
		}
		if monsterCells[c] {
			return fmt.Errorf("monster %d shares a cell with another monster", i)
		}
		monsterCells[c] = true
		if !puzzleGridInRange(m.GridCols, m.GridRows) {
			return fmt.Errorf("monster %d: gridCols/gridRows must be between %d and %d",
				i, DungeonMinPuzzleGrid, DungeonMaxPuzzleGrid)
		}
		if m.PlaySeconds <= 0 {
			return fmt.Errorf("monster %d: playSeconds must be positive", i)
		}
	}

	// Treasure puzzle settings are only validated when an image is set;
	// legacy dungeons without a treasure puzzle remain structurally valid.
	if l.TreasureImageID != uuid.Nil {
		if !puzzleGridInRange(l.TreasureGridCols, l.TreasureGridRows) {
			return fmt.Errorf("treasure: gridCols/gridRows must be between %d and %d",
				DungeonMinPuzzleGrid, DungeonMaxPuzzleGrid)
		}
		if l.TreasurePlaySeconds <= 0 {
			return errors.New("treasure: playSeconds must be positive")
		}
	}
	return nil
}
