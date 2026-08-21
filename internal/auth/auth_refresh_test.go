package auth

import (
	"context"
	"database/sql"
	"testing"

	"digesterr/internal/store"
	"digesterr/internal/testutil"
)

func setupAuthTestDB(t *testing.T) (*sql.DB, *store.Queries) {
	t.Helper()
	db, err := testutil.OpenTestDB()
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db, store.New(db)
}

func TestCreateAndValidateRefreshToken(t *testing.T) {
	db, queries := setupAuthTestDB(t)
	svc := NewService("test-secret", queries, db)
	user, err := queries.CreateUser(context.Background(), store.CreateUserParams{
		Email:        "refresh@example.com",
		PasswordHash: "hash",
		ApiKey:       "",
	})
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	raw, err := svc.CreateRefreshToken(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("CreateRefreshToken returned error: %v", err)
	}
	if raw == "" {
		t.Fatal("CreateRefreshToken returned empty token")
	}

	rt, err := svc.ValidateRefreshToken(context.Background(), raw)
	if err != nil {
		t.Fatalf("ValidateRefreshToken returned error: %v", err)
	}
	if rt.UserID != user.ID {
		t.Fatalf("expected userID %d, got %d", user.ID, rt.UserID)
	}
}

func TestValidateRefreshTokenInvalid(t *testing.T) {
	db, queries := setupAuthTestDB(t)
	svc := NewService("test-secret", queries, db)
	_, err := svc.ValidateRefreshToken(context.Background(), "totally-invalid-token")
	if err == nil {
		t.Fatal("expected error for invalid refresh token")
	}
	if err != ErrTokenInvalid {
		t.Fatalf("expected ErrTokenInvalid, got %v", err)
	}
}

func TestDeleteRefreshToken(t *testing.T) {
	db, queries := setupAuthTestDB(t)
	svc := NewService("test-secret", queries, db)
	user, err := queries.CreateUser(context.Background(), store.CreateUserParams{
		Email:        "delete-rt@example.com",
		PasswordHash: "hash",
		ApiKey:       "",
	})
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	raw, err := svc.CreateRefreshToken(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("CreateRefreshToken returned error: %v", err)
	}

	err = svc.DeleteRefreshToken(context.Background(), raw)
	if err != nil {
		t.Fatalf("DeleteRefreshToken returned error: %v", err)
	}

	_, err = svc.ValidateRefreshToken(context.Background(), raw)
	if err == nil {
		t.Fatal("expected error for deleted refresh token")
	}
}

func TestDeleteAllUserRefreshTokens(t *testing.T) {
	db, queries := setupAuthTestDB(t)
	svc := NewService("test-secret", queries, db)
	user, err := queries.CreateUser(context.Background(), store.CreateUserParams{
		Email:        "multi-rt@example.com",
		PasswordHash: "hash",
		ApiKey:       "",
	})
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	token1, _ := svc.CreateRefreshToken(context.Background(), user.ID)
	token2, _ := svc.CreateRefreshToken(context.Background(), user.ID)

	err = svc.DeleteAllUserRefreshTokens(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("DeleteAllUserRefreshTokens returned error: %v", err)
	}

	if _, err := svc.ValidateRefreshToken(context.Background(), token1); err == nil {
		t.Fatal("expected token1 to be invalidated")
	}
	if _, err := svc.ValidateRefreshToken(context.Background(), token2); err == nil {
		t.Fatal("expected token2 to be invalidated")
	}
}

func TestRefreshTokenRotation(t *testing.T) {
	db, queries := setupAuthTestDB(t)
	svc := NewService("test-secret", queries, db)
	user, err := queries.CreateUser(context.Background(), store.CreateUserParams{
		Email:        "rotation@example.com",
		PasswordHash: "hash",
		ApiKey:       "",
	})
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	raw1, _ := svc.CreateRefreshToken(context.Background(), user.ID)
	svc.DeleteRefreshToken(context.Background(), raw1)
	raw2, _ := svc.CreateRefreshToken(context.Background(), user.ID)

	_, err = svc.ValidateRefreshToken(context.Background(), raw1)
	if err == nil {
		t.Fatal("old refresh token should be invalid after rotation")
	}

	rt2, err := svc.ValidateRefreshToken(context.Background(), raw2)
	if err != nil {
		t.Fatalf("new refresh token should be valid: %v", err)
	}
	if rt2.UserID != user.ID {
		t.Fatalf("expected userID %d, got %d", user.ID, rt2.UserID)
	}
}
