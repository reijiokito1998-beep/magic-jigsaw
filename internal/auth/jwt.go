package auth

import (
	"fmt"
	"log"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Issuer is the "iss" claim set on every session token we issue, and the
// only issuer we accept when parsing (rejects tokens forged for another
// service that happens to share the signing secret).
const Issuer = "jigsaw-backend"

// Claims is the payload of our session token.
type Claims struct {
	Role string `json:"role"`
	jwt.RegisteredClaims
}

// JWTManager issues and parses HMAC-signed session tokens.
type JWTManager struct {
	secret []byte
	ttl    time.Duration
}

// NewJWTManager creates a manager with the given secret and token lifetime.
func NewJWTManager(secret string, ttl time.Duration) *JWTManager {
	return &JWTManager{secret: []byte(secret), ttl: ttl}
}

// Issue creates a signed token for the given user.
func (m *JWTManager) Issue(userID uuid.UUID, role string) (string, error) {
	now := time.Now()
	claims := Claims{
		Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.String(),
			Issuer:    Issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(m.ttl)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(m.secret)
	if err != nil {
		log.Printf("auth: token generation failed for subject=%s: %v", userID, err)
		return "", fmt.Errorf("sign token: %w", err)
	}
	log.Printf("auth: token issued for subject=%s role=%s expires_at=%s", userID, role, claims.ExpiresAt.Time)
	return signed, nil
}

// Parse validates a token string and returns its claims.
func (m *JWTManager) Parse(tokenStr string) (*Claims, error) {
	claims := &Claims{}
	_, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return m.secret, nil
	}, jwt.WithIssuer(Issuer))
	if err != nil {
		log.Printf("auth: token validation failed: %v", err)
		return nil, fmt.Errorf("parse token: %w", err)
	}
	log.Printf("auth: token validated for subject=%s", claims.Subject)
	return claims, nil
}

// UserID returns the subject as a UUID.
func (c *Claims) UserID() (uuid.UUID, error) {
	return uuid.Parse(c.Subject)
}
