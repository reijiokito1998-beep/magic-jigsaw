package purchases

// Reward is what one store product pays out. A product grants unlock points,
// expedition stars, or (in principle) both — the client never says how much of
// anything it bought, it only names a product.
type Reward struct {
	// Points is added to users.unlock_points.
	Points int
	// ExpeditionStars is added to users.expedition_bonus_stars and counts
	// towards unlocking expedition levels.
	ExpeditionStars int
}

// productRewards is the server-side source of truth for what each store
// product grants. The client sends only a product ID; how much that is worth is
// decided here and nowhere else.
//
// Price tiers are configured in App Store Connect / Google Play Console, not
// here; the Vietnam prices below are documentation only.
//
// TODO(product): every additional product ID created in App Store Connect /
// Google Play Console needs its own entry here before it can be credited.
var productRewards = map[string]Reward{
	// 100.000 ₫
	"unlock_points_50": {Points: 50},
	// 50.000 ₫
	"unlock_points_20": {Points: 20},
	// 100.000 ₫
	"expedition_stars_50": {ExpeditionStars: 50},
}

// RewardFor returns what productID grants. ok is false for a product this
// server does not know about, which must never be credited.
func RewardFor(productID string) (reward Reward, ok bool) {
	reward, ok = productRewards[productID]
	return reward, ok
}
