package crud

import (
	"encoding/json"
	"errors"
	"http-server/types"

	"gorm.io/gorm"
)

// ─────────────────────────────────────────────────────────────────────────────
// Permission check
// ─────────────────────────────────────────────────────────────────────────────

// requestCtx is the resolved identity for one HTTP request.
// It is built by the auth middleware from the JWT and stored in fiber.Locals.
type requestCtx struct {
	User      *User
	Namespace *Namespace
	Role      *Role
	IsRoot    bool
	IsNSAdmin bool // role_id = NULL && namespace_id != NULL
}

// canAccess returns nil when the requestCtx is allowed to perform action on resource.
// action ∈ {list, read, create, update, delete}
// resource is a schema slug or "*".
func (rc *requestCtx) canAccess(resource, action string) error {
	if rc.IsRoot {
		return nil
	}
	if rc.IsNSAdmin {
		return nil
	}
	if rc.Role == nil {
		return errors.New("forbidden: no role assigned")
	}

	var perms []Permission
	if err := json.Unmarshal([]byte(rc.Role.Permissions), &perms); err != nil {
		return errors.New("forbidden: cannot parse role permissions")
	}

	for _, p := range perms {
		if p.Resource != "*" && p.Resource != resource {
			continue
		}
		for _, a := range p.Actions {
			if a == "*" || a == action {
				return nil
			}
		}
	}
	return errors.New("forbidden")
}

// ─────────────────────────────────────────────────────────────────────────────
// Auth middleware
// ─────────────────────────────────────────────────────────────────────────────

// resolveRequestCtx builds a requestCtx from a JWT token string.
// secret is the namespace-specific (or global) signing secret.
func resolveRequestCtx(db *gorm.DB, tokenStr, secret string, authentications ...types.Authentification) (*requestCtx, error) {
	claims, err := parseJWT(tokenStr, secret)
	if err != nil {
		return nil, errors.New("unauthorized: " + err.Error())
	}

	// Validate session in DB
	if _, err := validateSession(db, claims.ID); err != nil {
		return nil, errors.New("unauthorized: " + err.Error())
	}
	if len(authentications) > 0 {
		authentication := authentications[0]
		if u, err := authentication.UserInfo(claims.Issuer); err == nil {
			user := User{
				Username:    u.User(),
				Email:       "",
				NamespaceID: nil,
				RoleID:      nil,
			}
			return &requestCtx{
				User:      &user,
				Namespace: nil,
				IsRoot:    claims.IsRoot,
				IsNSAdmin: !claims.IsRoot && user.NamespaceID != nil && user.RoleID == nil,
			}, nil
		}
	}
	var user User
	if err := db.First(&user, "id = ?", claims.UserID).Error; err != nil {
		return nil, errors.New("unauthorized: user not found")
	}
	if !user.IsActive {
		return nil, errors.New("unauthorized: user inactive")
	}

	var ns *Namespace
	if claims.NamespaceID != "" {
		var n Namespace
		if err := db.First(&n, "id = ?", claims.NamespaceID).Error; err != nil {
			return nil, errors.New("unauthorized: namespace not found")
		}
		ns = &n
	}

	rc := &requestCtx{
		User:      &user,
		Namespace: ns,
		IsRoot:    claims.IsRoot,
		IsNSAdmin: !claims.IsRoot && user.NamespaceID != nil && user.RoleID == nil,
	}

	if claims.RoleID != "" {
		var role Role
		if err := db.First(&role, "id = ?", claims.RoleID).Error; err == nil {
			rc.Role = &role
		}
	}

	return rc, nil
}
