package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// TokenType distinguishes access and refresh tokens.
type TokenType string

const (
	AccessToken  TokenType = "access"
	RefreshToken TokenType = "refresh"
)

// ErrInvalidToken is returned when a token fails verification.
var ErrInvalidToken = errors.New("auth: invalid token")

// Claims are the Viro JWT claims.
type Claims struct {
	Type TokenType `json:"typ"`
	jwt.RegisteredClaims
}

// TokenManager issues and verifies HS256 JWTs.
type TokenManager struct {
	secret     []byte
	accessTTL  time.Duration
	refreshTTL time.Duration
	now        func() time.Time
}

// NewTokenManager constructs a TokenManager with the given signing secret and TTLs.
func NewTokenManager(secret string, accessTTL, refreshTTL time.Duration) *TokenManager {
	return &TokenManager{
		secret:     []byte(secret),
		accessTTL:  accessTTL,
		refreshTTL: refreshTTL,
		now:        time.Now,
	}
}

func (m *TokenManager) ttl(t TokenType) time.Duration {
	if t == RefreshToken {
		return m.refreshTTL
	}
	return m.accessTTL
}

// Issue signs a token of the given type for the user. A unique token id (jti) is
// embedded so that refresh tokens can be tracked, rotated and revoked; callers
// that need the jti (e.g. to persist a refresh-token record) should use
// IssueWithID.
func (m *TokenManager) Issue(userID string, t TokenType) (string, error) {
	tok, _, err := m.IssueWithID(userID, t)
	return tok, err
}

// IssueWithID signs a token like Issue and additionally returns its jti claim.
func (m *TokenManager) IssueWithID(userID string, t TokenType) (token, jti string, err error) {
	now := m.now()
	jti = uuid.NewString()
	claims := Claims{
		Type: t,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        jti,
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(m.ttl(t))),
		},
	}
	token, err = jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
	return token, jti, err
}

// Verify parses and validates a token, requiring it to be of the expected type.
func (m *TokenManager) Verify(tokenStr string, expected TokenType) (*Claims, error) {
	claims := &Claims{}
	_, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return m.secret, nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil {
		return nil, ErrInvalidToken
	}
	if claims.Type != expected || claims.Subject == "" {
		return nil, ErrInvalidToken
	}
	return claims, nil
}
