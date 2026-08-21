package auth

import (
	"testing"

	"digesterr/internal/store"
)

func TestHashPassword(t *testing.T) {
	hash, err := HashPassword("testpassword")
	if err != nil {
		t.Fatalf("HashPassword returned error: %v", err)
	}
	if hash == "" {
		t.Fatal("HashPassword returned empty hash")
	}
	if hash == "testpassword" {
		t.Fatal("HashPassword returned plaintext")
	}
}

func TestCheckPassword(t *testing.T) {
	hash, _ := HashPassword("testpassword")
	if !CheckPassword("testpassword", hash) {
		t.Fatal("CheckPassword should return true for correct password")
	}
	if CheckPassword("wrongpassword", hash) {
		t.Fatal("CheckPassword should return false for wrong password")
	}
}

func TestGenerateAndValidateAccessToken(t *testing.T) {
	svc := NewService("test-secret-key", nil, nil)
	user := &store.User{ID: 1, Email: "test@example.com"}

	token, err := svc.GenerateAccessToken(user)
	if err != nil {
		t.Fatalf("GenerateAccessToken returned error: %v", err)
	}
	if token == "" {
		t.Fatal("GenerateAccessToken returned empty token")
	}

	claims, err := svc.ValidateAccessToken(token)
	if err != nil {
		t.Fatalf("ValidateAccessToken returned error: %v", err)
	}
	if claims.UserID != 1 {
		t.Fatalf("expected userID 1, got %d", claims.UserID)
	}
	if claims.Email != "test@example.com" {
		t.Fatalf("expected email test@example.com, got %s", claims.Email)
	}
}

func TestValidateAccessTokenInvalid(t *testing.T) {
	svc := NewService("test-secret-key", nil, nil)
	_, err := svc.ValidateAccessToken("invalid-token")
	if err == nil {
		t.Fatal("expected error for invalid token")
	}
}

func TestValidateAccessTokenWrongSecret(t *testing.T) {
	svc1 := NewService("secret1", nil, nil)
	svc2 := NewService("secret2", nil, nil)
	user := &store.User{ID: 1, Email: "test@example.com"}

	token, _ := svc1.GenerateAccessToken(user)
	_, err := svc2.ValidateAccessToken(token)
	if err == nil {
		t.Fatal("expected error for token signed with different secret")
	}
}
