package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"digesterr/internal/store"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrTokenExpired       = errors.New("token expired")
	ErrTokenInvalid       = errors.New("token invalid")
)

const (
	AccessTokenDuration  = 15 * time.Minute
	RefreshTokenDuration = 7 * 24 * time.Hour
	BcryptCost           = 12
)

type Claims struct {
	UserID int64  `json:"user_id"`
	Email  string `json:"email"`
	jwt.RegisteredClaims
}

type Service struct {
	secret  []byte
	queries *store.Queries
	db      *sql.DB
}

func NewService(secret string, queries *store.Queries, db *sql.DB) *Service {
	return &Service{
		secret:  []byte(secret),
		queries: queries,
		db:      db,
	}
}

func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), BcryptCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func CheckPassword(password, hash string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func (s *Service) GenerateAccessToken(user *store.User) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID: user.ID,
		Email:  user.Email,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(AccessTokenDuration)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(s.secret)
}

func (s *Service) ValidateAccessToken(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		return s.secret, nil
	})
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrTokenExpired
		}
		return nil, ErrTokenInvalid
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrTokenInvalid
	}
	return claims, nil
}

func (s *Service) CreateRefreshToken(ctx context.Context, userID int64) (string, error) {
	raw := generateToken()
	hash := hashToken(raw)
	_, err := s.queries.CreateRefreshToken(ctx, store.CreateRefreshTokenParams{
		UserID:    userID,
		TokenHash: hash,
		ExpiresAt: time.Now().Add(RefreshTokenDuration),
	})
	if err != nil {
		return "", err
	}
	return raw, nil
}

func (s *Service) ValidateRefreshToken(ctx context.Context, rawToken string) (*store.RefreshToken, error) {
	hash := hashToken(rawToken)
	rt, err := s.queries.GetRefreshTokenByHash(ctx, hash)
	if err != nil {
		return nil, ErrTokenInvalid
	}
	if rt.ExpiresAt.Before(time.Now()) {
		return nil, ErrTokenExpired
	}
	return &rt, nil
}

func (s *Service) DeleteRefreshToken(ctx context.Context, rawToken string) error {
	hash := hashToken(rawToken)
	return s.queries.DeleteRefreshToken(ctx, hash)
}

func (s *Service) DeleteAllUserRefreshTokens(ctx context.Context, userID int64) error {
	return s.queries.DeleteRefreshTokensByUser(ctx, userID)
}

// RotateRefreshToken atomically replaces an old refresh token with a new one:
// the replacement is created before the old token is deleted, inside a
// transaction. If creation fails, the old token survives — the client's
// session is not destroyed. Returns the new raw token.
func (s *Service) RotateRefreshToken(ctx context.Context, oldRawToken string, userID int64) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	qtx := s.queries.WithTx(tx)

	raw := generateToken()
	if _, err := qtx.CreateRefreshToken(ctx, store.CreateRefreshTokenParams{
		UserID:    userID,
		TokenHash: hashToken(raw),
		ExpiresAt: time.Now().Add(RefreshTokenDuration),
	}); err != nil {
		return "", err
	}
	if err := qtx.DeleteRefreshToken(ctx, hashToken(oldRawToken)); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return raw, nil
}

func generateToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
