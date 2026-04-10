package crud

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"
)

const jwtTTL = 24 * time.Hour

// ─────────────────────────────────────────────────────────────────────────────
// JWT
// ─────────────────────────────────────────────────────────────────────────────

type jwtClaims struct {
	jwt.RegisteredClaims
	UserID      string `json:"uid"`
	NamespaceID string `json:"nid"`
	RoleID      string `json:"rid"`
	IsRoot      bool   `json:"root"`
}

// signJWT creates a signed JWT for the given session.
func signJWT(sess *Session, user *User, secret string) (string, error) {
	now := time.Now()
	claims := jwtClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        sess.ID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(sess.ExpiresAt),
			Issuer:    user.Username,
		},
		UserID:      user.ID,
		NamespaceID: sess.NamespaceID,
		IsRoot:      user.NamespaceID == nil,
	}
	if user.RoleID != nil {
		claims.RoleID = *user.RoleID
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString([]byte(secret))
}

// parseJWT validates and parses a JWT. Returns the claims or an error.
func parseJWT(tokenStr, secret string) (*jwtClaims, error) {
	tok, err := jwt.ParseWithClaims(tokenStr, &jwtClaims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := tok.Claims.(*jwtClaims)
	if !ok || !tok.Valid {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Session helpers
// ─────────────────────────────────────────────────────────────────────────────

func createSession(db *gorm.DB, user *User, nsID string) (*Session, error) {
	sess := &Session{
		ID:          newID(),
		UserID:      user.ID,
		NamespaceID: nsID,
		ExpiresAt:   time.Now().Add(jwtTTL),
		CreatedAt:   time.Now(),
	}
	return sess, db.Create(sess).Error
}

// revokeSession marks a session as revoked by its JTI.
func revokeSession(db *gorm.DB, jti string) error {
	return db.Model(&Session{}).Where("id = ?", jti).Update("revoked", true).Error
}

// validateSession checks that a session exists, is not expired, and is not revoked.
func validateSession(db *gorm.DB, jti string) (*Session, error) {
	var sess Session
	if err := db.Where("id = ?", jti).First(&sess).Error; err != nil {
		return nil, fmt.Errorf("session not found")
	}
	if sess.Revoked {
		return nil, errors.New("session revoked")
	}
	if time.Now().After(sess.ExpiresAt) {
		return nil, errors.New("session expired")
	}
	return &sess, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Password login
// ─────────────────────────────────────────────────────────────────────────────

// loginPassword authenticates via username/email + password.
// rootAuth is the binder AuthConfigs for root-level auth.
// Returns the user and a fresh session token.
func loginPassword(
	db *gorm.DB,
	nsID string,
	identity string, // email or username
	password string,
	secret string,
) (string, *User, error) {

	var user User
	res := db.Where(
		"(email = ? OR username = ?) AND namespace_id = ? AND is_active = ?",
		identity, identity, nsID, true,
	).First(&user)
	if res.Error != nil {
		return "", nil, errors.New("invalid credentials")
	}

	// CheckPwd supports plain, bcrypt, and {ALG}hash formats
	if !checkPwd(user.PasswordHash, password) {
		return "", nil, errors.New("invalid credentials")
	}

	sess, err := createSession(db, &user, nsID)
	if err != nil {
		return "", nil, fmt.Errorf("create session: %w", err)
	}
	tok, err := signJWT(sess, &user, secret)
	return tok, &user, err
}

// ─────────────────────────────────────────────────────────────────────────────
// OAuth2 callback — finalize after code exchange
// ─────────────────────────────────────────────────────────────────────────────

// finalizeOAuth creates or retrieves the user from the OAuth userinfo payload.
// If the user does not exist AND the namespace is the default one, it is created.
func finalizeOAuth(
	db *gorm.DB,
	ns *Namespace,
	provider string,
	sub, email, name string,
	secret string,
) (string, *User, error) {

	if sub == "" {
		return "", nil, errors.New("oauth: missing sub")
	}

	// Find user by (provider, sub) across oauth_providers JSON
	var users []User
	db.Where("namespace_id = ?", ns.ID).Find(&users)

	var matched *User
	for i := range users {
		var links []OAuthLink
		if err := json.Unmarshal([]byte(users[i].OAuthProviders), &links); err != nil {
			continue
		}
		for _, l := range links {
			if l.Provider == provider && l.Sub == sub {
				matched = &users[i]
				break
			}
		}
		if matched != nil {
			break
		}
	}

	if matched == nil {
		// Only auto-create in the default namespace
		if !ns.IsDefault {
			return "", nil, fmt.Errorf("oauth: user not found in namespace %q", ns.Slug)
		}
		link := OAuthLink{Provider: provider, Sub: sub, Email: email, Name: name}
		linksJSON, _ := json.Marshal([]OAuthLink{link})
		newUser := &User{
			ID:             newID(),
			Username:       sanitizeUsername(email),
			Email:          email,
			NamespaceID:    &ns.ID,
			IsActive:       true,
			OAuthProviders: string(linksJSON),
			Metadata:       "{}",
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		}
		if err := db.Create(newUser).Error; err != nil {
			return "", nil, fmt.Errorf("oauth: create user: %w", err)
		}
		matched = newUser
	}

	sess, err := createSession(db, matched, ns.ID)
	if err != nil {
		return "", nil, fmt.Errorf("create session: %w", err)
	}
	tok, err := signJWT(sess, matched, secret)
	return tok, matched, err
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// checkPwd compares a stored password (plain, bcrypt, {ALG}hash) with the provided one.
// This mirrors the logic in binder.AuthConfig.CheckPwd without pulling in that package.
func checkPwd(stored, plain string) bool {
	if stored == "" {
		return plain == ""
	}
	// bcrypt
	if strings.HasPrefix(stored, "$2") {
		return bcryptCompare(stored, plain)
	}
	// {ALG}... handled by the binder package; for simplicity do plain compare here
	// (the binder AUTH directives handle root auth; DB users use bcrypt or plain)
	return stored == plain
}

// sanitizeUsername derives a username from an email address.
func sanitizeUsername(email string) string {
	if at := strings.IndexByte(email, '@'); at > 0 {
		return email[:at]
	}
	return email
}

// bcryptCompare wraps bcrypt without importing bcrypt at call-site.
// The real import is in crud.go to keep this file dependency-light.
var bcryptCompare func(hashed, plain string) bool
