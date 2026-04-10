package crud

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// Migrate runs AutoMigrate for all CRUD tables.
func Migrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&Namespace{},
		&OAuth2Provider{},
		&Role{},
		&User{},
		&Session{},
		&CrudSchema{},
		&CrudDocument{},
		&OAuthState{},
	)
}

// Seed ensures the "global" namespace and default admin role exist.
// Called once after Migrate.
func Seed(db *gorm.DB) error {
	// ── Global namespace ──────────────────────────────────────────────────────
	var ns Namespace
	res := db.Where("slug = ?", "global").First(&ns)
	if res.Error != nil && res.Error != gorm.ErrRecordNotFound {
		return fmt.Errorf("crud seed: query namespace: %w", res.Error)
	}
	if res.Error == gorm.ErrRecordNotFound {
		ns = Namespace{
			ID:            newID(),
			Name:          "Global",
			Slug:          "global",
			Description:   "Default global namespace",
			AuthProviders: "password",
			IsDefault:     true,
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
		}
		if err := db.Create(&ns).Error; err != nil {
			return fmt.Errorf("crud seed: create global namespace: %w", err)
		}
	}

	// ── Default admin role ────────────────────────────────────────────────────
	var adminRole Role
	res2 := db.Where("name = ? AND namespace_id = ?", "admin", ns.ID).First(&adminRole)
	if res2.Error != nil && res2.Error != gorm.ErrRecordNotFound {
		return fmt.Errorf("crud seed: query admin role: %w", res2.Error)
	}
	if res2.Error == gorm.ErrRecordNotFound {
		perms, _ := json.Marshal([]Permission{{Resource: "*", Actions: []string{"*"}}})
		adminRole = Role{
			ID:          newID(),
			Name:        "admin",
			NamespaceID: ns.ID,
			Permissions: string(perms),
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		if err := db.Create(&adminRole).Error; err != nil {
			return fmt.Errorf("crud seed: create admin role: %w", err)
		}
	}

	// ── Default viewer role ───────────────────────────────────────────────────
	var viewerRole Role
	res3 := db.Where("name = ? AND namespace_id = ?", "viewer", ns.ID).First(&viewerRole)
	if res3.Error != nil && res3.Error != gorm.ErrRecordNotFound {
		return fmt.Errorf("crud seed: query viewer role: %w", res3.Error)
	}
	if res3.Error == gorm.ErrRecordNotFound {
		perms, _ := json.Marshal([]Permission{
			{Resource: "*", Actions: []string{"list", "read"}},
		})
		if err := db.Create(&Role{
			ID:          newID(),
			Name:        "viewer",
			NamespaceID: ns.ID,
			Permissions: string(perms),
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}).Error; err != nil {
			return fmt.Errorf("crud seed: create viewer role: %w", err)
		}
	}

	return nil
}

// newID returns a 16-byte random hex string used as primary key.
func newID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
