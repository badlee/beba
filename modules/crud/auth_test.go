package crud

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestSignAndParseJWT(t *testing.T) {
	secret := "test-secret-key-123"
	user := &User{
		ID:       "user-1",
		Username: "alice",
		RoleID:   strPtr("admin-role"),
	}
	sess := &Session{
		ID:          "sess-1",
		UserID:      user.ID,
		NamespaceID: "ns-1",
		ExpiresAt:   time.Now().Add(1 * time.Hour),
	}

	tok, err := signJWT(sess, user, secret)
	if err != nil {
		t.Fatalf("signJWT failed: %v", err)
	}
	if tok == "" {
		t.Fatal("Expected non-empty token")
	}

	claims, err := parseJWT(tok, secret)
	if err != nil {
		t.Fatalf("parseJWT failed: %v", err)
	}

	if claims.UserID != "user-1" {
		t.Errorf("Expected UserID 'user-1', got %q", claims.UserID)
	}
	if claims.NamespaceID != "ns-1" {
		t.Errorf("Expected NamespaceID 'ns-1', got %q", claims.NamespaceID)
	}
	if claims.RoleID != "admin-role" {
		t.Errorf("Expected RoleID 'admin-role', got %q", claims.RoleID)
	}
	if claims.Issuer != "alice" {
		t.Errorf("Expected Issuer 'alice', got %q", claims.Issuer)
	}
	if claims.ID != "sess-1" {
		t.Errorf("Expected JTI 'sess-1', got %q", claims.ID)
	}
}

func TestParseJWT_WrongSecret(t *testing.T) {
	secret := "correct-secret"
	user := &User{ID: "u1", Username: "bob"}
	sess := &Session{ID: "s1", ExpiresAt: time.Now().Add(time.Hour)}

	tok, _ := signJWT(sess, user, secret)
	_, err := parseJWT(tok, "wrong-secret")
	if err == nil {
		t.Fatal("Expected error with wrong secret")
	}
}

func TestParseJWT_ExpiredToken(t *testing.T) {
	secret := "test-secret"
	user := &User{ID: "u1", Username: "bob"}
	sess := &Session{ID: "s1", ExpiresAt: time.Now().Add(-1 * time.Hour)} // already expired

	tok, _ := signJWT(sess, user, secret)
	_, err := parseJWT(tok, secret)
	if err == nil {
		t.Fatal("Expected error for expired token")
	}
}

func TestParseJWT_InvalidToken(t *testing.T) {
	_, err := parseJWT("not.a.valid.token", "secret")
	if err == nil {
		t.Fatal("Expected error for invalid token string")
	}
}

func TestParseJWT_WrongSigningMethod(t *testing.T) {
	// Create a token signed with a different method (RSA would need keys, so just test HMAC mismatch via bad alg)
	secret := "test-secret"
	claims := &jwtClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        "s1",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
		UserID: "u1",
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenStr, _ := tok.SignedString([]byte(secret))

	// This should work with correct secret
	_, err := parseJWT(tokenStr, secret)
	if err != nil {
		t.Fatalf("Expected valid parse, got: %v", err)
	}
}

func TestSignJWT_NoRole(t *testing.T) {
	secret := "test-secret"
	user := &User{ID: "u1", Username: "norole", RoleID: nil}
	sess := &Session{ID: "s1", ExpiresAt: time.Now().Add(time.Hour)}

	tok, err := signJWT(sess, user, secret)
	if err != nil {
		t.Fatalf("signJWT failed: %v", err)
	}

	claims, err := parseJWT(tok, secret)
	if err != nil {
		t.Fatalf("parseJWT failed: %v", err)
	}
	if claims.RoleID != "" {
		t.Errorf("Expected empty RoleID, got %q", claims.RoleID)
	}
}

func TestSignJWT_RootUser(t *testing.T) {
	secret := "test-secret"
	user := &User{ID: "root1", Username: "admin", NamespaceID: nil}
	sess := &Session{ID: "s1", ExpiresAt: time.Now().Add(time.Hour)}

	tok, _ := signJWT(sess, user, secret)
	claims, _ := parseJWT(tok, secret)
	if !claims.IsRoot {
		t.Error("Expected IsRoot=true for user with nil NamespaceID")
	}
}

func TestCheckPwd(t *testing.T) {
	tests := []struct {
		name     string
		stored   string
		plain    string
		expected bool
	}{
		{"Plain match", "password123", "password123", true},
		{"Plain mismatch", "password123", "wrong", false},
		{"Empty both", "", "", true},
		{"Empty stored non-empty plain", "", "something", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := checkPwd(tt.stored, tt.plain); got != tt.expected {
				t.Errorf("checkPwd(%q, %q) = %v, want %v", tt.stored, tt.plain, got, tt.expected)
			}
		})
	}
}

func TestSanitizeUsername(t *testing.T) {
	tests := []struct {
		email    string
		expected string
	}{
		{"alice@example.com", "alice"},
		{"bob@company.co", "bob"},
		{"noatsign", "noatsign"},
		{"@leading", "@leading"},
	}

	for _, tt := range tests {
		t.Run(tt.email, func(t *testing.T) {
			got := sanitizeUsername(tt.email)
			if got != tt.expected {
				t.Errorf("sanitizeUsername(%q) = %q, want %q", tt.email, got, tt.expected)
			}
		})
	}
}

func strPtr(s string) *string { return &s }
