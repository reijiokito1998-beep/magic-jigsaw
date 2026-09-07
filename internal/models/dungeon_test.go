package models

import (
	"fmt"
	"testing"
)

// validMonsters returns 3 well-formed monsters sitting on interior path cells
// (1,0), (2,0) and (3,0) for the straight-line path used by validLayout.
func validMonsters() []DungeonMonsterInput {
	return []DungeonMonsterInput{
		{Row: 1, Col: 0, GridCols: 3, GridRows: 3, PlaySeconds: 120},
		{Row: 2, Col: 0, GridCols: 3, GridRows: 3, PlaySeconds: 120},
		{Row: 3, Col: 0, GridCols: 3, GridRows: 3, PlaySeconds: 120},
	}
}

// validLayout is a straight vertical path from (0,0) to (4,0) with monsters on
// the 3 interior cells.
func validLayout() DungeonLayout {
	return DungeonLayout{
		Start:    Cell{Row: 0, Col: 0},
		Treasure: Cell{Row: 4, Col: 0},
		Path: []Cell{
			{Row: 0, Col: 0},
			{Row: 1, Col: 0},
			{Row: 2, Col: 0},
			{Row: 3, Col: 0},
			{Row: 4, Col: 0},
		},
		Monsters: validMonsters(),
	}
}

// snakeLayout returns a layout whose path snakes through every grid cell, with
// n monsters on the n path cells right after the start. It gives the count
// boundary tests enough interior cells to place DungeonMaxMonsterCount+1
// monsters without tripping the distinct-cell rule first.
func snakeLayout(n int) DungeonLayout {
	path := make([]Cell, 0, DungeonGridRows*DungeonGridCols)
	for row := 0; row < DungeonGridRows; row++ {
		for i := 0; i < DungeonGridCols; i++ {
			col := i
			if row%2 == 1 {
				col = DungeonGridCols - 1 - i // reverse every other row
			}
			path = append(path, Cell{Row: row, Col: col})
		}
	}
	monsters := make([]DungeonMonsterInput, 0, n)
	for _, c := range path[1 : n+1] {
		monsters = append(monsters, DungeonMonsterInput{
			Row: c.Row, Col: c.Col, GridCols: 3, GridRows: 3, PlaySeconds: 120,
		})
	}
	return DungeonLayout{
		Start:    path[0],
		Treasure: path[len(path)-1],
		Path:     path,
		Monsters: monsters,
	}
}

func TestValidateDungeonLayout_MonsterCountBounds(t *testing.T) {
	tests := []struct {
		name    string
		count   int
		wantErr bool
	}{
		{"below minimum", DungeonMinMonsterCount - 1, true},
		{"at minimum", DungeonMinMonsterCount, false},
		{"inside range", DungeonMinMonsterCount + 1, false},
		{"at maximum", DungeonMaxMonsterCount, false},
		{"above maximum", DungeonMaxMonsterCount + 1, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := snakeLayout(tt.count)
			if len(l.Monsters) != tt.count {
				t.Fatalf("snakeLayout(%d) built %d monsters", tt.count, len(l.Monsters))
			}
			err := ValidateDungeonLayout(l)
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("%d monsters: expected no error, got: %v", tt.count, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("%d monsters: expected an error, got nil", tt.count)
			}
			// Assert the count rule rejected it, not some other layout rule.
			want := fmt.Sprintf("between %d and %d monsters are required", DungeonMinMonsterCount, DungeonMaxMonsterCount)
			if err.Error() != want {
				t.Fatalf("%d monsters: got error %q, want %q", tt.count, err, want)
			}
		})
	}
}

func TestValidateDungeonLayout_Valid(t *testing.T) {
	if err := ValidateDungeonLayout(validLayout()); err != nil {
		t.Fatalf("expected valid layout to pass, got: %v", err)
	}
}

func TestValidateDungeonLayout(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(l *DungeonLayout)
		wantErr bool
	}{
		{
			name:    "valid layout",
			mutate:  func(l *DungeonLayout) {},
			wantErr: false,
		},
		{
			name: "start out of bounds",
			mutate: func(l *DungeonLayout) {
				l.Start = Cell{Row: -1, Col: 0}
			},
			wantErr: true,
		},
		{
			name: "treasure out of bounds",
			mutate: func(l *DungeonLayout) {
				l.Treasure = Cell{Row: 8, Col: 0}
			},
			wantErr: true,
		},
		{
			name: "path too short",
			mutate: func(l *DungeonLayout) {
				l.Path = []Cell{{Row: 0, Col: 0}}
			},
			wantErr: true,
		},
		{
			name: "path cell out of bounds",
			mutate: func(l *DungeonLayout) {
				l.Path = append(l.Path, Cell{Row: 8, Col: 0})
			},
			wantErr: true,
		},
		{
			name: "path contains duplicate cell",
			mutate: func(l *DungeonLayout) {
				l.Path = []Cell{
					{Row: 0, Col: 0}, {Row: 1, Col: 0}, {Row: 0, Col: 0},
					{Row: 1, Col: 0}, {Row: 2, Col: 0},
				}
			},
			wantErr: true,
		},
		{
			name: "path not connected (non-adjacent step)",
			mutate: func(l *DungeonLayout) {
				l.Path = []Cell{
					{Row: 0, Col: 0}, {Row: 2, Col: 0}, {Row: 3, Col: 0}, {Row: 4, Col: 0},
				}
			},
			wantErr: true,
		},
		{
			name: "path does not start at start cell",
			mutate: func(l *DungeonLayout) {
				l.Path[0] = Cell{Row: 0, Col: 1}
				l.Path[1] = Cell{Row: 0, Col: 0}
			},
			wantErr: true,
		},
		{
			name: "path does not end at treasure cell",
			mutate: func(l *DungeonLayout) {
				l.Path[len(l.Path)-1] = Cell{Row: 4, Col: 1}
			},
			wantErr: true,
		},
		{
			name: "no monsters at all",
			mutate: func(l *DungeonLayout) {
				l.Monsters = nil
			},
			wantErr: true,
		},
		{
			name: "fewer monsters than path allows is fine",
			mutate: func(l *DungeonLayout) {
				l.Monsters = l.Monsters[:2]
			},
			wantErr: false,
		},
		{
			name: "monster off the path",
			mutate: func(l *DungeonLayout) {
				l.Monsters[0].Row, l.Monsters[0].Col = 1, 5
			},
			wantErr: true,
		},
		{
			name: "monster on the start cell",
			mutate: func(l *DungeonLayout) {
				l.Monsters[0].Row, l.Monsters[0].Col = 0, 0
			},
			wantErr: true,
		},
		{
			name: "monster on the treasure cell",
			mutate: func(l *DungeonLayout) {
				l.Monsters[0].Row, l.Monsters[0].Col = 4, 0
			},
			wantErr: true,
		},
		{
			name: "two monsters share a cell",
			mutate: func(l *DungeonLayout) {
				l.Monsters[1].Row, l.Monsters[1].Col = l.Monsters[0].Row, l.Monsters[0].Col
			},
			wantErr: true,
		},
		{
			name: "monster gridCols too small",
			mutate: func(l *DungeonLayout) {
				l.Monsters[0].GridCols = 1
			},
			wantErr: true,
		},
		{
			name: "monster gridRows too large",
			mutate: func(l *DungeonLayout) {
				l.Monsters[0].GridRows = DungeonMaxPuzzleGrid + 1
			},
			wantErr: true,
		},
		{
			name: "monster grid at the admin preset maximum",
			mutate: func(l *DungeonLayout) {
				l.Monsters[0].GridCols, l.Monsters[0].GridRows = 8, 12
			},
			wantErr: false,
		},
		{
			name: "monster non-positive playSeconds",
			mutate: func(l *DungeonLayout) {
				l.Monsters[0].PlaySeconds = 0
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := validLayout()
			tt.mutate(&l)
			err := ValidateDungeonLayout(l)
			if tt.wantErr && err == nil {
				t.Fatalf("expected an error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("expected no error, got: %v", err)
			}
		})
	}
}
