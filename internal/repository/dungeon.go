package repository

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/reijiokito/jigsaw-backend/internal/models"
)

// Dungeon-specific errors, mapped to HTTP responses at the transport edge.
var (
	// ErrDungeonInactive is returned when acting on a dungeon that is not active.
	ErrDungeonInactive = errors.New("dungeon is inactive")
	// ErrDungeonCompleted is returned when acting on a dungeon already completed
	// by the caller.
	ErrDungeonCompleted = errors.New("dungeon already completed")
	// ErrInvalidMove is returned when a move target isn't on the path or isn't
	// 4-adjacent to the player's current cell.
	ErrInvalidMove = errors.New("invalid move")
	// ErrWrongCell is returned when an action requires the player to be at a
	// specific cell (a monster's cell, or the treasure) and they are not.
	ErrWrongCell = errors.New("player is not at the required cell")
	// ErrAlreadyDefeated is returned when a monster was already marked defeated.
	ErrAlreadyDefeated = errors.New("monster already defeated")
	// ErrMonstersRemain is returned when completing a dungeon before all of its
	// monsters have been defeated.
	ErrMonstersRemain = errors.New("not all monsters are defeated")
)

// SaveDungeonParams is the admin-authored shape of a dungeon, used for both
// create and full-replace update.
type SaveDungeonParams struct {
	Title               string
	XPReward            int
	Active              bool
	Start               models.Cell
	Treasure            models.Cell
	Path                []models.Cell
	Monsters            []models.DungeonMonsterInput
	TreasureImageID     uuid.UUID
	TreasureGridCols    int
	TreasureGridRows    int
	TreasurePlaySeconds int
}

// nullableUUID maps a nil UUID to a SQL NULL so nullable columns store NULL
// instead of the all-zero UUID.
func nullableUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

// dungeonRow is the raw dungeons table row.
type dungeonRow struct {
	ID                  uuid.UUID
	Title               string
	XPReward            int
	Start               models.Cell
	Treasure            models.Cell
	Path                []models.Cell
	Active              bool
	TreasureImageID     uuid.UUID
	TreasureImageURL    string
	TreasureGridCols    int
	TreasureGridRows    int
	TreasurePlaySeconds int
}

// getDungeonRow loads a dungeon's layout row, or ErrNotFound. The treasure
// image is left-joined so a dungeon with no treasure puzzle still loads (with
// a nil TreasureImageID and empty URL).
func (s *Store) getDungeonRow(ctx context.Context, id uuid.UUID) (dungeonRow, error) {
	log.Printf("[DEBUG] getDungeonRow: id=%v", id)
	var d dungeonRow
	d.ID = id
	var treasureImageID *uuid.UUID
	err := s.pool.QueryRow(ctx, `
SELECT d.title, d.xp_reward, d.start_row, d.start_col, d.treasure_row, d.treasure_col, d.path, d.active,
       d.treasure_image_id, COALESCE(i.url, ''), d.treasure_grid_cols, d.treasure_grid_rows, d.treasure_play_seconds
FROM dungeons d
LEFT JOIN images i ON i.id = d.treasure_image_id
WHERE d.id = $1`, id).Scan(
		&d.Title, &d.XPReward, &d.Start.Row, &d.Start.Col, &d.Treasure.Row, &d.Treasure.Col, &d.Path, &d.Active,
		&treasureImageID, &d.TreasureImageURL, &d.TreasureGridCols, &d.TreasureGridRows, &d.TreasurePlaySeconds)
	if errors.Is(err, pgx.ErrNoRows) {
		log.Printf("[DEBUG] getDungeonRow: not found id=%v", id)
		return dungeonRow{}, ErrNotFound
	}
	if err != nil {
		log.Printf("[DEBUG] getDungeonRow: query failed: %v", err)
		return dungeonRow{}, fmt.Errorf("get dungeon: %w", err)
	}
	if treasureImageID != nil {
		d.TreasureImageID = *treasureImageID
	}
	return d, nil
}

// getDungeonMonsters returns every monster placed in a dungeon (Defeated left
// false; callers set it from the caller's progress where relevant).
func (s *Store) getDungeonMonsters(ctx context.Context, dungeonID uuid.UUID) ([]models.DungeonMonster, error) {
	log.Printf("[DEBUG] getDungeonMonsters: dungeonID=%v", dungeonID)
	rows, err := s.pool.Query(ctx, `
SELECT dm.id, dm.cell_row, dm.cell_col, dm.grid_cols, dm.grid_rows, dm.play_seconds, dm.image_id, COALESCE(i.url, '')
FROM dungeon_monsters dm
JOIN images i ON i.id = dm.image_id
WHERE dm.dungeon_id = $1
ORDER BY dm.cell_row, dm.cell_col`, dungeonID)
	if err != nil {
		log.Printf("[DEBUG] getDungeonMonsters: query failed: %v", err)
		return nil, fmt.Errorf("list dungeon monsters: %w", err)
	}
	defer rows.Close()

	out := make([]models.DungeonMonster, 0)
	for rows.Next() {
		var m models.DungeonMonster
		if err := rows.Scan(&m.ID, &m.Row, &m.Col, &m.GridCols, &m.GridRows, &m.PlaySeconds, &m.ImageID, &m.ImageURL); err != nil {
			log.Printf("[DEBUG] getDungeonMonsters: scan failed: %v", err)
			return nil, fmt.Errorf("scan dungeon monster: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		log.Printf("[DEBUG] getDungeonMonsters: rows error: %v", err)
		return nil, err
	}
	log.Printf("[DEBUG] getDungeonMonsters: dungeonID=%v found %d monsters", dungeonID, len(out))
	return out, nil
}

// getMonsterAtCell returns the monster at a cell (nil, nil if none).
func (s *Store) getMonsterAtCell(ctx context.Context, dungeonID uuid.UUID, cell models.Cell) (*models.DungeonMonster, error) {
	log.Printf("[DEBUG] getMonsterAtCell: dungeonID=%v cell=%+v", dungeonID, cell)
	var m models.DungeonMonster
	err := s.pool.QueryRow(ctx, `
SELECT dm.id, dm.cell_row, dm.cell_col, dm.grid_cols, dm.grid_rows, dm.play_seconds, dm.image_id, COALESCE(i.url, '')
FROM dungeon_monsters dm
JOIN images i ON i.id = dm.image_id
WHERE dm.dungeon_id = $1 AND dm.cell_row = $2 AND dm.cell_col = $3`, dungeonID, cell.Row, cell.Col).Scan(
		&m.ID, &m.Row, &m.Col, &m.GridCols, &m.GridRows, &m.PlaySeconds, &m.ImageID, &m.ImageURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		log.Printf("[DEBUG] getMonsterAtCell: query failed: %v", err)
		return nil, fmt.Errorf("monster at cell: %w", err)
	}
	return &m, nil
}

// manhattan is the grid (taxicab) distance between two cells.
func manhattan(a, b models.Cell) int {
	dr := a.Row - b.Row
	if dr < 0 {
		dr = -dr
	}
	dc := a.Col - b.Col
	if dc < 0 {
		dc = -dc
	}
	dist := dr + dc
	log.Printf("[DEBUG] manhattan: a=%+v b=%+v distance=%d", a, b, dist)
	return dist
}

// appendCellDedup appends c to visited unless it's already present.
func appendCellDedup(visited []models.Cell, c models.Cell) []models.Cell {
	for _, v := range visited {
		if v == c {
			return visited
		}
	}
	return append(visited, c)
}

// ListDungeons returns every active dungeon with the caller's progress.
func (s *Store) ListDungeons(ctx context.Context, userID uuid.UUID, limit, offset int) ([]models.Dungeon, int, error) {
	const q = `
SELECT d.id, d.title, d.xp_reward, d.active,
       (SELECT count(*) FROM dungeon_monsters dm WHERE dm.dungeon_id = d.id),
       EXISTS(SELECT 1 FROM dungeon_progress dp WHERE dp.dungeon_id = d.id AND dp.user_id = $1),
       COALESCE((SELECT dp.completed FROM dungeon_progress dp WHERE dp.dungeon_id = d.id AND dp.user_id = $1), false),
       count(*) OVER ()
FROM dungeons d
WHERE d.active = true
ORDER BY d.created_at ASC
LIMIT NULLIF($2, 0) OFFSET $3`

	log.Printf("[DEBUG] ListDungeons: userID=%v limit=%d offset=%d", userID, limit, offset)
	rows, err := s.pool.Query(ctx, q, userID, limit, offset)
	if err != nil {
		log.Printf("[DEBUG] ListDungeons: query failed: %v", err)
		return nil, 0, fmt.Errorf("list dungeons: %w", err)
	}
	defer rows.Close()

	out := make([]models.Dungeon, 0)
	total := 0
	for rows.Next() {
		var d models.Dungeon
		if err := rows.Scan(&d.ID, &d.Title, &d.XPReward, &d.Active, &d.MonsterCount, &d.Started, &d.Completed, &total); err != nil {
			log.Printf("[DEBUG] ListDungeons: scan failed: %v", err)
			return nil, 0, fmt.Errorf("scan dungeon: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		log.Printf("[DEBUG] ListDungeons: rows error: %v", err)
		return nil, 0, err
	}
	log.Printf("[DEBUG] ListDungeons: returned %d of %d dungeons", len(out), total)
	return out, total, nil
}

// GetDungeon returns a dungeon's layout plus the caller's progress and
// current energy. If the caller has no saved progress, it is synthesized at
// the start cell (not persisted — the first move lazily creates the row).
func (s *Store) GetDungeon(ctx context.Context, id, userID uuid.UUID) (models.DungeonDetail, error) {
	log.Printf("[DEBUG] GetDungeon: id=%v userID=%v", id, userID)
	d, err := s.getDungeonRow(ctx, id)
	if err != nil {
		return models.DungeonDetail{}, err
	}
	monsters, err := s.getDungeonMonsters(ctx, id)
	if err != nil {
		return models.DungeonDetail{}, err
	}

	var progress models.DungeonProgress
	var pRow, pCol int
	var defeated []string
	var visited []models.Cell
	var completed bool
	err = s.pool.QueryRow(ctx, `
SELECT pos_row, pos_col, defeated, visited, completed
FROM dungeon_progress WHERE user_id = $1 AND dungeon_id = $2`, userID, id).Scan(
		&pRow, &pCol, &defeated, &visited, &completed)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		log.Printf("[DEBUG] GetDungeon: no saved progress for userID=%v dungeonID=%v, synthesizing at start", userID, id)
		progress = models.DungeonProgress{
			PosRow: d.Start.Row, PosCol: d.Start.Col,
			Defeated: []string{}, Visited: []models.Cell{d.Start}, Completed: false,
		}
	case err != nil:
		log.Printf("[DEBUG] GetDungeon: load progress failed: %v", err)
		return models.DungeonDetail{}, fmt.Errorf("get dungeon progress: %w", err)
	default:
		progress = models.DungeonProgress{PosRow: pRow, PosCol: pCol, Defeated: defeated, Visited: visited, Completed: completed}
	}

	defeatedSet := make(map[string]bool, len(progress.Defeated))
	for _, mid := range progress.Defeated {
		defeatedSet[mid] = true
	}
	for i := range monsters {
		monsters[i].Defeated = defeatedSet[monsters[i].ID.String()]
	}

	energy, err := s.GetUnlockPoints(ctx, userID)
	if err != nil {
		return models.DungeonDetail{}, err
	}

	return models.DungeonDetail{
		ID: d.ID, Title: d.Title, XPReward: d.XPReward,
		GridCols: models.DungeonGridCols, GridRows: models.DungeonGridRows,
		Start: d.Start, Treasure: d.Treasure, Path: d.Path, Monsters: monsters,
		Progress: progress, Energy: energy,
		TreasureImageID: d.TreasureImageID, TreasureImageURL: d.TreasureImageURL,
		TreasureGridCols: d.TreasureGridCols, TreasureGridRows: d.TreasureGridRows,
		TreasurePlaySeconds: d.TreasurePlaySeconds,
	}, nil
}

// DungeonMoveResult is the outcome of a successful move.
type DungeonMoveResult struct {
	PosRow  int
	PosCol  int
	Energy  int
	Monster *models.DungeonMonster
}

// MoveDungeon moves the player one step onto an adjacent path cell, spending
// 1 unlock point. If the caller has no saved progress yet, a row is lazily
// created at the dungeon's start cell first. ok=false (with err=nil) means
// the balance was insufficient and no move was made.
func (s *Store) MoveDungeon(ctx context.Context, userID, dungeonID uuid.UUID, target models.Cell) (DungeonMoveResult, bool, error) {
	log.Printf("[DEBUG] MoveDungeon: userID=%v dungeonID=%v target=%+v", userID, dungeonID, target)
	d, err := s.getDungeonRow(ctx, dungeonID)
	if err != nil {
		return DungeonMoveResult{}, false, err
	}
	if !d.Active {
		log.Printf("[DEBUG] MoveDungeon: dungeonID=%v is inactive", dungeonID)
		return DungeonMoveResult{}, false, ErrDungeonInactive
	}
	onPath := false
	for _, c := range d.Path {
		if c == target {
			onPath = true
			break
		}
	}
	if !onPath {
		log.Printf("[DEBUG] MoveDungeon: target=%+v is not on path", target)
		return DungeonMoveResult{}, false, ErrInvalidMove
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		log.Printf("[DEBUG] MoveDungeon: begin tx failed: %v", err)
		return DungeonMoveResult{}, false, fmt.Errorf("begin move tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var pos models.Cell
	var defeated []string
	var visited []models.Cell
	var completed bool
	err = tx.QueryRow(ctx, `
SELECT pos_row, pos_col, defeated, visited, completed
FROM dungeon_progress WHERE user_id = $1 AND dungeon_id = $2 FOR UPDATE`, userID, dungeonID).Scan(
		&pos.Row, &pos.Col, &defeated, &visited, &completed)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		pos = d.Start
		defeated = []string{}
		visited = []models.Cell{d.Start}
		completed = false
		log.Printf("[DEBUG] MoveDungeon: no saved progress, initializing at start for userID=%v dungeonID=%v", userID, dungeonID)
		if _, err := tx.Exec(ctx, `
INSERT INTO dungeon_progress (user_id, dungeon_id, pos_row, pos_col, defeated, visited, completed)
VALUES ($1, $2, $3, $4, $5, $6, false)`,
			userID, dungeonID, pos.Row, pos.Col, defeated, visited); err != nil {
			log.Printf("[DEBUG] MoveDungeon: init progress failed: %v", err)
			return DungeonMoveResult{}, false, fmt.Errorf("init dungeon progress: %w", err)
		}
	case err != nil:
		log.Printf("[DEBUG] MoveDungeon: load progress failed: %v", err)
		return DungeonMoveResult{}, false, fmt.Errorf("load dungeon progress: %w", err)
	}

	if completed {
		log.Printf("[DEBUG] MoveDungeon: dungeonID=%v already completed by userID=%v", dungeonID, userID)
		return DungeonMoveResult{}, false, ErrDungeonCompleted
	}
	if manhattan(pos, target) != 1 {
		log.Printf("[DEBUG] MoveDungeon: target=%+v not adjacent to pos=%+v", target, pos)
		return DungeonMoveResult{}, false, ErrInvalidMove
	}

	var balance int
	err = tx.QueryRow(ctx, `
UPDATE users SET unlock_points = unlock_points - 1, updated_at = now()
WHERE id = $1 AND unlock_points >= 1
RETURNING unlock_points`, userID).Scan(&balance)
	if errors.Is(err, pgx.ErrNoRows) {
		log.Printf("[DEBUG] MoveDungeon: userID=%v insufficient unlock points", userID)
		return DungeonMoveResult{}, false, nil // insufficient points; move not applied
	}
	if err != nil {
		log.Printf("[DEBUG] MoveDungeon: spend point failed: %v", err)
		return DungeonMoveResult{}, false, fmt.Errorf("spend point: %w", err)
	}

	newVisited := appendCellDedup(visited, target)
	if _, err := tx.Exec(ctx, `
UPDATE dungeon_progress
SET prev_row = $3, prev_col = $4, pos_row = $5, pos_col = $6, visited = $7
WHERE user_id = $1 AND dungeon_id = $2`,
		userID, dungeonID, pos.Row, pos.Col, target.Row, target.Col, newVisited); err != nil {
		log.Printf("[DEBUG] MoveDungeon: update progress failed: %v", err)
		return DungeonMoveResult{}, false, fmt.Errorf("update dungeon progress: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		log.Printf("[DEBUG] MoveDungeon: commit failed: %v", err)
		return DungeonMoveResult{}, false, fmt.Errorf("commit move: %w", err)
	}

	res := DungeonMoveResult{PosRow: target.Row, PosCol: target.Col, Energy: balance}
	log.Printf("[DEBUG] MoveDungeon: userID=%v moved to %+v, energy=%d", userID, target, balance)

	mon, err := s.getMonsterAtCell(ctx, dungeonID, target)
	if err != nil {
		return res, true, err
	}
	if mon != nil {
		defeatedSet := make(map[string]bool, len(defeated))
		for _, mid := range defeated {
			defeatedSet[mid] = true
		}
		if !defeatedSet[mon.ID.String()] {
			res.Monster = mon
			log.Printf("[DEBUG] MoveDungeon: encountered undefeated monster id=%v at target", mon.ID)
		}
	}
	return res, true, nil
}

// RetreatDungeon moves the player back to their previous cell for free. If
// there is no previous cell (or no saved progress at all), it is a no-op that
// returns the player's current cell.
func (s *Store) RetreatDungeon(ctx context.Context, userID, dungeonID uuid.UUID) (posRow, posCol, energy int, err error) {
	log.Printf("[DEBUG] RetreatDungeon: userID=%v dungeonID=%v", userID, dungeonID)
	d, err := s.getDungeonRow(ctx, dungeonID)
	if err != nil {
		return 0, 0, 0, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		log.Printf("[DEBUG] RetreatDungeon: begin tx failed: %v", err)
		return 0, 0, 0, fmt.Errorf("begin retreat tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var curRow, curCol int
	var prevRow, prevCol *int
	exists := true
	err = tx.QueryRow(ctx, `
SELECT pos_row, pos_col, prev_row, prev_col
FROM dungeon_progress WHERE user_id = $1 AND dungeon_id = $2 FOR UPDATE`, userID, dungeonID).Scan(
		&curRow, &curCol, &prevRow, &prevCol)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		exists = false
		curRow, curCol = d.Start.Row, d.Start.Col
	case err != nil:
		log.Printf("[DEBUG] RetreatDungeon: load progress failed: %v", err)
		return 0, 0, 0, fmt.Errorf("load dungeon progress: %w", err)
	}

	if prevRow == nil || prevCol == nil {
		// Nothing to retreat to: no-op at the current (or start) cell.
		log.Printf("[DEBUG] RetreatDungeon: nothing to retreat to, staying at (%d,%d)", curRow, curCol)
		energy, err = s.GetUnlockPoints(ctx, userID)
		return curRow, curCol, energy, err
	}

	if exists {
		if _, err := tx.Exec(ctx, `
UPDATE dungeon_progress
SET pos_row = $3, pos_col = $4, prev_row = NULL, prev_col = NULL
WHERE user_id = $1 AND dungeon_id = $2`, userID, dungeonID, *prevRow, *prevCol); err != nil {
			log.Printf("[DEBUG] RetreatDungeon: update failed: %v", err)
			return 0, 0, 0, fmt.Errorf("update retreat: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		log.Printf("[DEBUG] RetreatDungeon: commit failed: %v", err)
		return 0, 0, 0, fmt.Errorf("commit retreat: %w", err)
	}

	energy, err = s.GetUnlockPoints(ctx, userID)
	log.Printf("[DEBUG] RetreatDungeon: userID=%v retreated to (%d,%d)", userID, *prevRow, *prevCol)
	return *prevRow, *prevCol, energy, err
}

// CompleteDungeonMonster marks a monster defeated for the caller, requiring
// their current position to be that monster's cell and it not already
// defeated. Returns the updated defeated-id list.
func (s *Store) CompleteDungeonMonster(ctx context.Context, userID, dungeonID, monsterID uuid.UUID) ([]string, error) {
	log.Printf("[DEBUG] CompleteDungeonMonster: userID=%v dungeonID=%v monsterID=%v", userID, dungeonID, monsterID)
	var cell models.Cell
	err := s.pool.QueryRow(ctx, `
SELECT cell_row, cell_col FROM dungeon_monsters WHERE id = $1 AND dungeon_id = $2`, monsterID, dungeonID).Scan(&cell.Row, &cell.Col)
	if errors.Is(err, pgx.ErrNoRows) {
		log.Printf("[DEBUG] CompleteDungeonMonster: monster not found id=%v", monsterID)
		return nil, ErrNotFound
	}
	if err != nil {
		log.Printf("[DEBUG] CompleteDungeonMonster: load monster failed: %v", err)
		return nil, fmt.Errorf("load monster: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		log.Printf("[DEBUG] CompleteDungeonMonster: begin tx failed: %v", err)
		return nil, fmt.Errorf("begin defeat tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var pos models.Cell
	var defeated []string
	err = tx.QueryRow(ctx, `
SELECT pos_row, pos_col, defeated
FROM dungeon_progress WHERE user_id = $1 AND dungeon_id = $2 FOR UPDATE`, userID, dungeonID).Scan(
		&pos.Row, &pos.Col, &defeated)
	if errors.Is(err, pgx.ErrNoRows) {
		// No progress yet means the player is still at the start cell, which
		// can never be a monster's cell (enforced at layout validation).
		log.Printf("[DEBUG] CompleteDungeonMonster: no progress, player still at start")
		return nil, ErrWrongCell
	}
	if err != nil {
		log.Printf("[DEBUG] CompleteDungeonMonster: load progress failed: %v", err)
		return nil, fmt.Errorf("load dungeon progress: %w", err)
	}

	if pos != cell {
		log.Printf("[DEBUG] CompleteDungeonMonster: player at %+v, monster at %+v", pos, cell)
		return nil, ErrWrongCell
	}
	for _, mid := range defeated {
		if mid == monsterID.String() {
			log.Printf("[DEBUG] CompleteDungeonMonster: monsterID=%v already defeated", monsterID)
			return nil, ErrAlreadyDefeated
		}
	}
	defeated = append(defeated, monsterID.String())

	if _, err := tx.Exec(ctx, `
UPDATE dungeon_progress SET defeated = $3 WHERE user_id = $1 AND dungeon_id = $2`,
		userID, dungeonID, defeated); err != nil {
		log.Printf("[DEBUG] CompleteDungeonMonster: update defeated failed: %v", err)
		return nil, fmt.Errorf("update defeated: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		log.Printf("[DEBUG] CompleteDungeonMonster: commit failed: %v", err)
		return nil, fmt.Errorf("commit defeat: %w", err)
	}
	log.Printf("[DEBUG] CompleteDungeonMonster: monsterID=%v defeated, total defeated=%d", monsterID, len(defeated))
	return defeated, nil
}

// CompleteDungeon marks the dungeon completed for the caller, requiring them
// to be at the treasure cell with every monster defeated and not already
// completed. When the dungeon has a treasure puzzle image, the treasure image
// is also unlocked into the caller's treasure gallery in the same transaction.
// Returns the XP reward to award (the caller is responsible for actually
// crediting it, mirroring CompleteStoryPage).
func (s *Store) CompleteDungeon(ctx context.Context, userID, dungeonID uuid.UUID) (int, error) {
	log.Printf("[DEBUG] CompleteDungeon: userID=%v dungeonID=%v", userID, dungeonID)
	d, err := s.getDungeonRow(ctx, dungeonID)
	if err != nil {
		return 0, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		log.Printf("[DEBUG] CompleteDungeon: begin tx failed: %v", err)
		return 0, fmt.Errorf("begin complete tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var pos models.Cell
	var defeated []string
	var completed bool
	err = tx.QueryRow(ctx, `
SELECT pos_row, pos_col, defeated, completed
FROM dungeon_progress WHERE user_id = $1 AND dungeon_id = $2 FOR UPDATE`, userID, dungeonID).Scan(
		&pos.Row, &pos.Col, &defeated, &completed)
	if errors.Is(err, pgx.ErrNoRows) {
		// Never left the start cell, which can't be the treasure cell.
		log.Printf("[DEBUG] CompleteDungeon: no progress, player never left start")
		return 0, ErrWrongCell
	}
	if err != nil {
		log.Printf("[DEBUG] CompleteDungeon: load progress failed: %v", err)
		return 0, fmt.Errorf("load dungeon progress: %w", err)
	}

	if completed {
		log.Printf("[DEBUG] CompleteDungeon: dungeonID=%v already completed by userID=%v", dungeonID, userID)
		return 0, ErrDungeonCompleted
	}
	if pos != d.Treasure {
		log.Printf("[DEBUG] CompleteDungeon: player at %+v, treasure at %+v", pos, d.Treasure)
		return 0, ErrWrongCell
	}
	// Compare against this dungeon's own monster count: dungeons hold a variable
	// number of monsters, so a global constant would let players of a large
	// dungeon finish early and make a small dungeon impossible to finish.
	var monsterCount int
	if err := tx.QueryRow(ctx, `
SELECT count(*) FROM dungeon_monsters WHERE dungeon_id = $1`, dungeonID).Scan(&monsterCount); err != nil {
		log.Printf("[DEBUG] CompleteDungeon: count monsters failed: %v", err)
		return 0, fmt.Errorf("count dungeon monsters: %w", err)
	}
	if len(defeated) < monsterCount {
		log.Printf("[DEBUG] CompleteDungeon: only %d/%d monsters defeated", len(defeated), monsterCount)
		return 0, ErrMonstersRemain
	}

	if _, err := tx.Exec(ctx, `
UPDATE dungeon_progress SET completed = true, completed_at = now()
WHERE user_id = $1 AND dungeon_id = $2`, userID, dungeonID); err != nil {
		log.Printf("[DEBUG] CompleteDungeon: mark completed failed: %v", err)
		return 0, fmt.Errorf("mark dungeon completed: %w", err)
	}

	if d.TreasureImageID != uuid.Nil {
		if _, err := tx.Exec(ctx, `
INSERT INTO dungeon_treasure_unlocks (user_id, dungeon_id, image_id)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, dungeon_id) DO NOTHING`, userID, dungeonID, d.TreasureImageID); err != nil {
			log.Printf("[DEBUG] CompleteDungeon: unlock treasure failed: %v", err)
			return 0, fmt.Errorf("unlock dungeon treasure: %w", err)
		}
		log.Printf("[DEBUG] CompleteDungeon: unlocked treasure imageID=%v for userID=%v dungeonID=%v", d.TreasureImageID, userID, dungeonID)
	}

	if err := tx.Commit(ctx); err != nil {
		log.Printf("[DEBUG] CompleteDungeon: commit failed: %v", err)
		return 0, fmt.Errorf("commit complete: %w", err)
	}
	log.Printf("[DEBUG] CompleteDungeon: userID=%v completed dungeonID=%v, xpReward=%d", userID, dungeonID, d.XPReward)
	return d.XPReward, nil
}

// ListUnlockedTreasures returns every treasure image the caller has unlocked,
// newest first, joined to the dungeon (title) and image (url) that produced it.
func (s *Store) ListUnlockedTreasures(ctx context.Context, userID uuid.UUID) ([]models.DungeonTreasureUnlock, error) {
	log.Printf("[DEBUG] ListUnlockedTreasures: userID=%v", userID)
	rows, err := s.pool.Query(ctx, `
SELECT tu.dungeon_id, d.title, tu.image_id, COALESCE(i.url, ''), tu.unlocked_at
FROM dungeon_treasure_unlocks tu
JOIN dungeons d ON d.id = tu.dungeon_id
JOIN images i ON i.id = tu.image_id
WHERE tu.user_id = $1
ORDER BY tu.unlocked_at DESC`, userID)
	if err != nil {
		log.Printf("[DEBUG] ListUnlockedTreasures: query failed: %v", err)
		return nil, fmt.Errorf("list unlocked treasures: %w", err)
	}
	defer rows.Close()

	out := make([]models.DungeonTreasureUnlock, 0)
	for rows.Next() {
		var t models.DungeonTreasureUnlock
		if err := rows.Scan(&t.DungeonID, &t.DungeonTitle, &t.ImageID, &t.ImageURL, &t.UnlockedAt); err != nil {
			log.Printf("[DEBUG] ListUnlockedTreasures: scan failed: %v", err)
			return nil, fmt.Errorf("scan unlocked treasure: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		log.Printf("[DEBUG] ListUnlockedTreasures: rows error: %v", err)
		return nil, err
	}
	log.Printf("[DEBUG] ListUnlockedTreasures: userID=%v returned %d treasures", userID, len(out))
	return out, nil
}

// --- Admin -------------------------------------------------------------

// AdminListDungeons returns every dungeon (including inactive ones) with its
// full layout and monsters, for the admin editor.
func (s *Store) AdminListDungeons(ctx context.Context, limit, offset int) ([]models.AdminDungeon, int, error) {
	log.Printf("[DEBUG] AdminListDungeons: querying limit=%d offset=%d", limit, offset)
	rows, err := s.pool.Query(ctx, `
SELECT d.id, d.title, d.xp_reward, d.start_row, d.start_col, d.treasure_row, d.treasure_col, d.path, d.active,
       d.treasure_image_id, COALESCE(i.url, ''), d.treasure_grid_cols, d.treasure_grid_rows, d.treasure_play_seconds,
       count(*) OVER ()
FROM dungeons d
LEFT JOIN images i ON i.id = d.treasure_image_id
ORDER BY d.created_at ASC
LIMIT NULLIF($1, 0) OFFSET $2`, limit, offset)
	if err != nil {
		log.Printf("[DEBUG] AdminListDungeons: query failed: %v", err)
		return nil, 0, fmt.Errorf("admin list dungeons: %w", err)
	}
	defer rows.Close()

	out := make([]models.AdminDungeon, 0)
	total := 0
	for rows.Next() {
		var d models.AdminDungeon
		d.GridCols = models.DungeonGridCols
		d.GridRows = models.DungeonGridRows
		var treasureImageID *uuid.UUID
		if err := rows.Scan(&d.ID, &d.Title, &d.XPReward, &d.Start.Row, &d.Start.Col,
			&d.Treasure.Row, &d.Treasure.Col, &d.Path, &d.Active,
			&treasureImageID, &d.TreasureImageURL, &d.TreasureGridCols, &d.TreasureGridRows, &d.TreasurePlaySeconds,
			&total); err != nil {
			log.Printf("[DEBUG] AdminListDungeons: scan failed: %v", err)
			return nil, 0, fmt.Errorf("scan admin dungeon: %w", err)
		}
		if treasureImageID != nil {
			d.TreasureImageID = *treasureImageID
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		log.Printf("[DEBUG] AdminListDungeons: rows error: %v", err)
		return nil, 0, err
	}

	// One extra query per dungeon for its monsters keeps the code simple. That
	// is only affordable because the caller pages: before pagination this ran
	// once per dungeon in the whole table on every admin refresh.
	for i := range out {
		monsters, err := s.getDungeonMonsters(ctx, out[i].ID)
		if err != nil {
			return nil, 0, err
		}
		out[i].Monsters = monsters
	}
	log.Printf("[DEBUG] AdminListDungeons: returned %d of %d dungeons", len(out), total)
	return out, total, nil
}

// GetAdminDungeon returns a single dungeon's full layout for the admin editor.
func (s *Store) GetAdminDungeon(ctx context.Context, id uuid.UUID) (models.AdminDungeon, error) {
	log.Printf("[DEBUG] GetAdminDungeon: id=%v", id)
	d, err := s.getDungeonRow(ctx, id)
	if err != nil {
		return models.AdminDungeon{}, err
	}
	monsters, err := s.getDungeonMonsters(ctx, id)
	if err != nil {
		return models.AdminDungeon{}, err
	}
	return models.AdminDungeon{
		ID: d.ID, Title: d.Title, XPReward: d.XPReward, Active: d.Active,
		GridCols: models.DungeonGridCols, GridRows: models.DungeonGridRows,
		Start: d.Start, Treasure: d.Treasure, Path: d.Path, Monsters: monsters,
		TreasureImageID: d.TreasureImageID, TreasureImageURL: d.TreasureImageURL,
		TreasureGridCols: d.TreasureGridCols, TreasureGridRows: d.TreasureGridRows,
		TreasurePlaySeconds: d.TreasurePlaySeconds,
	}, nil
}

// CreateDungeon inserts a dungeon and its monsters, returning the new id.
func (s *Store) CreateDungeon(ctx context.Context, p SaveDungeonParams) (uuid.UUID, error) {
	log.Printf("[DEBUG] CreateDungeon: title=%s monsters=%d", p.Title, len(p.Monsters))
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		log.Printf("[DEBUG] CreateDungeon: begin tx failed: %v", err)
		return uuid.Nil, fmt.Errorf("begin create dungeon tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var id uuid.UUID
	err = tx.QueryRow(ctx, `
INSERT INTO dungeons (title, xp_reward, start_row, start_col, treasure_row, treasure_col, path, active,
                      treasure_image_id, treasure_grid_cols, treasure_grid_rows, treasure_play_seconds)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING id`,
		p.Title, p.XPReward, p.Start.Row, p.Start.Col, p.Treasure.Row, p.Treasure.Col, p.Path, p.Active,
		nullableUUID(p.TreasureImageID), p.TreasureGridCols, p.TreasureGridRows, p.TreasurePlaySeconds).Scan(&id)
	if err != nil {
		log.Printf("[DEBUG] CreateDungeon: insert failed: %v", err)
		return uuid.Nil, fmt.Errorf("create dungeon: %w", err)
	}

	if err := insertDungeonMonsters(ctx, tx, id, p.Monsters); err != nil {
		return uuid.Nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		log.Printf("[DEBUG] CreateDungeon: commit failed: %v", err)
		return uuid.Nil, fmt.Errorf("commit create dungeon: %w", err)
	}
	log.Printf("[DEBUG] CreateDungeon: created dungeon id=%v", id)
	return id, nil
}

// UpdateDungeon fully replaces a dungeon's layout and monsters (delete +
// reinsert), and drops any saved player progress since old positions are no
// longer valid against the new map.
func (s *Store) UpdateDungeon(ctx context.Context, id uuid.UUID, p SaveDungeonParams) error {
	log.Printf("[DEBUG] UpdateDungeon: id=%v title=%s monsters=%d", id, p.Title, len(p.Monsters))
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		log.Printf("[DEBUG] UpdateDungeon: begin tx failed: %v", err)
		return fmt.Errorf("begin update dungeon tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
UPDATE dungeons
SET title = $2, xp_reward = $3, start_row = $4, start_col = $5,
    treasure_row = $6, treasure_col = $7, path = $8, active = $9,
    treasure_image_id = $10, treasure_grid_cols = $11, treasure_grid_rows = $12, treasure_play_seconds = $13,
    updated_at = now()
WHERE id = $1`,
		id, p.Title, p.XPReward, p.Start.Row, p.Start.Col, p.Treasure.Row, p.Treasure.Col, p.Path, p.Active,
		nullableUUID(p.TreasureImageID), p.TreasureGridCols, p.TreasureGridRows, p.TreasurePlaySeconds)
	if err != nil {
		log.Printf("[DEBUG] UpdateDungeon: update failed: %v", err)
		return fmt.Errorf("update dungeon: %w", err)
	}
	if tag.RowsAffected() == 0 {
		log.Printf("[DEBUG] UpdateDungeon: not found id=%v", id)
		return ErrNotFound
	}

	if _, err := tx.Exec(ctx, `DELETE FROM dungeon_monsters WHERE dungeon_id = $1`, id); err != nil {
		log.Printf("[DEBUG] UpdateDungeon: clear monsters failed: %v", err)
		return fmt.Errorf("clear dungeon monsters: %w", err)
	}
	if err := insertDungeonMonsters(ctx, tx, id, p.Monsters); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `DELETE FROM dungeon_progress WHERE dungeon_id = $1`, id); err != nil {
		log.Printf("[DEBUG] UpdateDungeon: clear progress failed: %v", err)
		return fmt.Errorf("clear dungeon progress: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		log.Printf("[DEBUG] UpdateDungeon: commit failed: %v", err)
		return fmt.Errorf("commit update dungeon: %w", err)
	}
	log.Printf("[DEBUG] UpdateDungeon: updated id=%v, rows affected=%d", id, tag.RowsAffected())
	return nil
}

// insertDungeonMonsters inserts each monster row for a dungeon within tx.
func insertDungeonMonsters(ctx context.Context, tx pgx.Tx, dungeonID uuid.UUID, monsters []models.DungeonMonsterInput) error {
	log.Printf("[DEBUG] insertDungeonMonsters: dungeonID=%v count=%d", dungeonID, len(monsters))
	for _, m := range monsters {
		if _, err := tx.Exec(ctx, `
INSERT INTO dungeon_monsters (dungeon_id, cell_row, cell_col, image_id, grid_cols, grid_rows, play_seconds)
VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			dungeonID, m.Row, m.Col, m.ImageID, m.GridCols, m.GridRows, m.PlaySeconds); err != nil {
			log.Printf("[DEBUG] insertDungeonMonsters: insert failed: %v", err)
			return fmt.Errorf("insert dungeon monster: %w", err)
		}
	}
	return nil
}

// DeleteDungeon removes a dungeon (monsters/progress cascade).
func (s *Store) DeleteDungeon(ctx context.Context, id uuid.UUID) error {
	log.Printf("[DEBUG] DeleteDungeon: id=%v", id)
	tag, err := s.pool.Exec(ctx, `DELETE FROM dungeons WHERE id = $1`, id)
	if err != nil {
		log.Printf("[DEBUG] DeleteDungeon: query failed: %v", err)
		return fmt.Errorf("delete dungeon: %w", err)
	}
	if tag.RowsAffected() == 0 {
		log.Printf("[DEBUG] DeleteDungeon: not found id=%v", id)
		return ErrNotFound
	}
	log.Printf("[DEBUG] DeleteDungeon: deleted id=%v", id)
	return nil
}
