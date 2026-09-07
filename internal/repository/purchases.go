package repository

import (
	"context"
	"fmt"
	"log"

	"github.com/google/uuid"
)

// PurchaseBalances is what a user holds after a purchase was (or was not)
// credited: their unlock-point balance and their purchased expedition stars.
type PurchaseBalances struct {
	Points int
	// BonusStars is only the bought stars, not the user's total star count —
	// the total also counts completed levels and daily challenges and is
	// computed by TotalExpeditionStars.
	BonusStars int
}

// CreditPurchase records a store-verified purchase and grants its reward
// (unlock points and/or expedition stars), exactly once per
// (platform, transactionID).
//
// Both statements run in one transaction so the ledger row and the balances can
// never disagree. A transaction id that was already processed is a legitimate
// replay (client retry after a crash, or restorePurchases), not an error: it
// returns credited=false with the current balances so the caller can still tell
// the client where it stands.
func (s *Store) CreditPurchase(
	ctx context.Context,
	userID uuid.UUID,
	productID, platform, transactionID string,
	points, stars int,
) (balances PurchaseBalances, credited bool, err error) {
	log.Printf("[DEBUG] CreditPurchase: userID=%v product=%s platform=%s points=%d stars=%d",
		userID, productID, platform, points, stars)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		log.Printf("[DEBUG] CreditPurchase: begin tx failed: %v", err)
		return PurchaseBalances{}, false, fmt.Errorf("begin purchase tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// The unique index on (platform, transaction_id) is the idempotency gate:
	// concurrent duplicates block here and the loser sees RowsAffected() == 0.
	tag, err := tx.Exec(ctx, `
INSERT INTO purchases (user_id, product_id, platform, transaction_id, points_credited, stars_credited)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (platform, transaction_id) DO NOTHING`,
		userID, productID, platform, transactionID, points, stars)
	if err != nil {
		log.Printf("[DEBUG] CreditPurchase: insert purchase failed: %v", err)
		return PurchaseBalances{}, false, fmt.Errorf("record purchase: %w", err)
	}

	if tag.RowsAffected() == 0 {
		if err := tx.QueryRow(ctx,
			`SELECT unlock_points, expedition_bonus_stars FROM users WHERE id = $1`,
			userID).Scan(&balances.Points, &balances.BonusStars); err != nil {
			log.Printf("[DEBUG] CreditPurchase: load balances for replay failed: %v", err)
			return PurchaseBalances{}, false, fmt.Errorf("load balances for replayed purchase: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			log.Printf("[DEBUG] CreditPurchase: commit replay failed: %v", err)
			return PurchaseBalances{}, false, fmt.Errorf("commit replayed purchase: %w", err)
		}
		log.Printf("[DEBUG] CreditPurchase: userID=%v transaction already credited (platform=%s), points=%d stars=%d",
			userID, platform, balances.Points, balances.BonusStars)
		return balances, false, nil
	}

	if err := tx.QueryRow(ctx, `
UPDATE users
SET unlock_points = unlock_points + $2,
    expedition_bonus_stars = expedition_bonus_stars + $3,
    updated_at = now()
WHERE id = $1
RETURNING unlock_points, expedition_bonus_stars`,
		userID, points, stars).Scan(&balances.Points, &balances.BonusStars); err != nil {
		log.Printf("[DEBUG] CreditPurchase: credit reward failed: %v", err)
		return PurchaseBalances{}, false, fmt.Errorf("credit purchase reward: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		log.Printf("[DEBUG] CreditPurchase: commit failed: %v", err)
		return PurchaseBalances{}, false, fmt.Errorf("commit purchase credit: %w", err)
	}

	log.Printf("[DEBUG] CreditPurchase: userID=%v credited +%d points +%d stars, balances points=%d stars=%d",
		userID, points, stars, balances.Points, balances.BonusStars)
	return balances, true, nil
}
