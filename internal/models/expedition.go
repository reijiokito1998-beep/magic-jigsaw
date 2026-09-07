package models

// DefaultExpeditionLevelCount is how many levels the campaign is seeded with.
// It is only a starting point: an admin can append levels, so the live length
// is always read from the database (repository.ExpeditionLevelCount) and never
// assumed to be this number.
const DefaultExpeditionLevelCount = 100

// ExpeditionTier is the derived difficulty configuration for a level index.
type ExpeditionTier struct {
	Star        int    `json:"star"`
	GridCols    int    `json:"gridCols"`
	GridRows    int    `json:"gridRows"`
	PlaySeconds int    `json:"playSeconds"`
	Category    string `json:"category"`
	XPReward    int    `json:"xpReward"`

	// ShowOnMain places the level on the Expedition map. When false the level
	// only exists inside the themed collection named by [Category], where it
	// is always playable: no stars are needed to open it, but clearing it does
	// pay its stars into the same total the map spends.
	ShowOnMain bool `json:"showOnMain"`
}

// Pieces is the total number of pieces for the tier.
func (t ExpeditionTier) Pieces() int { return t.GridCols * t.GridRows }

// TierForLevel returns the tier config for a 1-based level index.
//
//	1-20   1★  4x6   "Abyss of Ruin"      2m
//	21-40  2★  6x10  "Shadow Vanguard"    5m
//	41-60  3★  8x8   "Eternal Dominion"   8m
//	61-80  4★  8x12  "Forsaken Citadel"  10m
//	81+    5★  10x16 "Crimson Eclipse"   12m
//
// Admin-created levels (index > 100) fall into the last branch and start from
// the 5★ defaults; the admin then edits them like any other level.
func TierForLevel(index int) ExpeditionTier {
	switch {
	case index <= 20:
		return ExpeditionTier{Star: 1, GridCols: 4, GridRows: 6, PlaySeconds: 120, Category: "Abyss of Ruin", XPReward: 100, ShowOnMain: true}
	case index <= 40:
		return ExpeditionTier{Star: 2, GridCols: 6, GridRows: 10, PlaySeconds: 300, Category: "Shadow Vanguard", XPReward: 200, ShowOnMain: true}
	case index <= 60:
		return ExpeditionTier{Star: 3, GridCols: 8, GridRows: 8, PlaySeconds: 480, Category: "Eternal Dominion", XPReward: 300, ShowOnMain: true}
	case index <= 80:
		return ExpeditionTier{Star: 4, GridCols: 8, GridRows: 12, PlaySeconds: 600, Category: "Forsaken Citadel", XPReward: 500, ShowOnMain: true}
	default:
		return ExpeditionTier{Star: 5, GridCols: 10, GridRows: 16, PlaySeconds: 720, Category: "Crimson Eclipse", XPReward: 1000, ShowOnMain: true}
	}
}

// RequiredStars is the minimum total stars needed to unlock a level: the
// cumulative tier stars of all preceding levels. Level 1 requires 0.
func RequiredStars(index int) int {
	total := 0
	for i := 1; i < index; i++ {
		total += TierForLevel(i).Star
	}
	return total
}

// LevelConfig is an admin override for a level's game config. A nil field
// means "keep the code-derived tier default" for that field.
type LevelConfig struct {
	Star        *int
	GridCols    *int
	GridRows    *int
	PlaySeconds *int
	Category    *string
	XPReward    *int
	ShowOnMain  *bool
}

// EffectiveTier applies an admin override (if any) on top of the index-derived
// tier default, yielding the tier actually used for the level.
func EffectiveTier(index int, o *LevelConfig) ExpeditionTier {
	t := TierForLevel(index)
	if o == nil {
		return t
	}
	if o.Star != nil {
		t.Star = *o.Star
	}
	if o.GridCols != nil {
		t.GridCols = *o.GridCols
	}
	if o.GridRows != nil {
		t.GridRows = *o.GridRows
	}
	if o.PlaySeconds != nil {
		t.PlaySeconds = *o.PlaySeconds
	}
	if o.Category != nil {
		t.Category = *o.Category
	}
	if o.XPReward != nil {
		t.XPReward = *o.XPReward
	}
	if o.ShowOnMain != nil {
		t.ShowOnMain = *o.ShowOnMain
	}
	return t
}

// RequiredStarsFromTiers is like RequiredStars but uses effective (possibly
// admin-edited) tier stars: the cumulative stars of all preceding map levels.
//
// Collection levels (ShowOnMain false) are skipped: they are not steps on the
// map, so they must not raise the bar for the map levels that follow them.
// Their stars still count on the earning side (repository.TotalExpeditionStars)
// — that asymmetry is the point, it is what lets a collection run get a player
// ahead on the campaign.
func RequiredStarsFromTiers(tiers map[int]ExpeditionTier, index int) int {
	total := 0
	for i := 1; i < index; i++ {
		if !tiers[i].ShowOnMain {
			continue
		}
		total += tiers[i].Star
	}
	return total
}

// ExpeditionLevel is one level in the campaign, personalized to a user.
type ExpeditionLevel struct {
	Index         int    `json:"index"`
	Star          int    `json:"star"`
	GridCols      int    `json:"gridCols"`
	GridRows      int    `json:"gridRows"`
	PlaySeconds   int    `json:"playSeconds"`
	Category      string `json:"category"`
	RequiredStars int    `json:"requiredStars"`
	ImageURL      string `json:"imageUrl"`
	// ThumbURL is [ImageURL] sized for a level card on the campaign map.
	ThumbURL    string `json:"thumbUrl,omitempty"`
	ImageID     string `json:"imageId"`
	Status      string `json:"status"` // locked | available | in_progress | completed | failed
	Unlocked    bool   `json:"unlocked"`
	StarsEarned int    `json:"starsEarned"`
	// XPReward is what a first clear of this level pays out, surfaced so the
	// campaign map can show the reward before the level is played.
	XPReward int `json:"xpReward"`

	// ShowOnMain is false for a level that lives only in a themed collection.
	// The client sends every level in one list and splits on this flag, so the
	// collections stay a pure client-side filter (as they already were).
	ShowOnMain bool `json:"showOnMain"`
}

// ExpeditionState is the full campaign view for a user.
type ExpeditionState struct {
	TotalStars     int               `json:"totalStars"`
	CompletedCount int               `json:"completedCount"`
	CurrentLevel   int               `json:"currentLevel"`
	Levels         []ExpeditionLevel `json:"levels"`
}
