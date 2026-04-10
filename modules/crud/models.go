package crud

import (
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Namespace
// ─────────────────────────────────────────────────────────────────────────────

// Namespace groups schemas and users. The slug "global" is always created.
type Namespace struct {
	ID          string    `gorm:"primaryKey"          json:"id"`
	Name        string    `gorm:"not null"            json:"name"`
	Slug        string    `gorm:"uniqueIndex;not null" json:"slug"`
	Description string    `json:"description"`
	// Comma-separated list of active auth providers: "password,google,github"
	AuthProviders string  `json:"auth_providers"`
	// Optional JWT secret override (falls back to AppConfig.Secret)
	JWTSecret   string    `json:"-"`
	// JSON-encoded HookSet
	Hooks       string    `gorm:"type:text" json:"-"`
	IsDefault   bool      `json:"is_default"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ─────────────────────────────────────────────────────────────────────────────
// OAuth2Provider — configured in the DSL
// ─────────────────────────────────────────────────────────────────────────────

type OAuth2Provider struct {
	ID           string    `gorm:"primaryKey"          json:"id"`
	Name         string    `gorm:"uniqueIndex;not null" json:"name"` // "google","github"…
	ClientID     string    `gorm:"not null"            json:"-"`
	ClientSecret string    `gorm:"not null"            json:"-"`
	RedirectURL  string    `gorm:"not null"            json:"redirect_url"`
	Endpoint     string    `gorm:"not null"            json:"endpoint"`
	TokenURL     string    `gorm:"not null"            json:"token_url"`
	UserinfoURL  string    `gorm:"not null"            json:"userinfo_url"`
	// Comma-separated scopes
	Scopes       string    `json:"scopes"`
	// JSON map: {"sub":"id","email":"email","name":"name"}
	FieldMap     string    `gorm:"type:text" json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Role
// ─────────────────────────────────────────────────────────────────────────────

type Role struct {
	ID          string    `gorm:"primaryKey"  json:"id"`
	Name        string    `gorm:"not null"    json:"name"`
	NamespaceID string    `gorm:"not null;index" json:"namespace_id"`
	// JSON-encoded []Permission
	Permissions string    `gorm:"type:text"   json:"-"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Permission describes what actions a role may perform on a resource.
// Resource "*" means all schemas. Actions "*" means all operations.
type Permission struct {
	Resource string   `json:"resource"` // schema slug or "*"
	Actions  []string `json:"actions"`  // list|read|create|update|delete or ["*"]
}

// ─────────────────────────────────────────────────────────────────────────────
// User
// ─────────────────────────────────────────────────────────────────────────────

type User struct {
	ID           string    `gorm:"primaryKey"     json:"id"`
	Username     string    `gorm:"index"          json:"username"`
	Email        string    `gorm:"index"          json:"email"`
	PasswordHash string    `json:"-"`
	// NULL → root (access to all namespaces)
	NamespaceID  *string   `gorm:"index"          json:"namespace_id"`
	// NULL + NamespaceID != NULL → namespace admin
	RoleID       *string   `gorm:"index"          json:"role_id"`
	IsActive     bool      `gorm:"default:true"   json:"is_active"`
	// JSON-encoded []OAuthLink
	OAuthProviders string  `gorm:"type:text"      json:"-"`
	// JSON arbitrary user metadata
	Metadata     string    `gorm:"type:text"      json:"metadata"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// OAuthLink links a user to an external provider identity.
type OAuthLink struct {
	Provider string `json:"provider"` // "google","github"…
	Sub      string `json:"sub"`
	Email    string `json:"email"`
	Name     string `json:"name"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Session
// ─────────────────────────────────────────────────────────────────────────────

type Session struct {
	// jti claim of the JWT
	ID          string    `gorm:"primaryKey"  json:"id"`
	UserID      string    `gorm:"index;not null" json:"user_id"`
	NamespaceID string    `gorm:"index"       json:"namespace_id"`
	ExpiresAt   time.Time `json:"expires_at"`
	Revoked     bool      `gorm:"default:false" json:"revoked"`
	CreatedAt   time.Time `json:"created_at"`
}

// ─────────────────────────────────────────────────────────────────────────────
// CrudSchema  (prefix "Crud" to avoid clash with db.Schema)
// ─────────────────────────────────────────────────────────────────────────────

type CrudSchema struct {
	ID          string    `gorm:"primaryKey"           json:"id"`
	Name        string    `gorm:"not null"             json:"name"`
	Slug        string    `gorm:"index;not null"       json:"slug"`
	Description string    `json:"description"`
	Icon        string    `json:"icon"`
	Color       string    `json:"color"`
	NamespaceID string    `gorm:"index;not null"       json:"namespace_id"`
	SoftDelete  bool      `gorm:"default:false"        json:"soft_delete"`
	AllowRawSQL bool      `gorm:"default:false"        json:"allow_raw_sql"`
	// JSON-encoded []FieldDef
	Fields      string    `gorm:"type:text"            json:"-"`
	// JSON-encoded HookSet
	Hooks       string    `gorm:"type:text"            json:"-"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// FieldDef is one field inside a CrudSchema.
type FieldDef struct {
	Name     string      `json:"name"`
	Type     string      `json:"type"` // string|number|boolean|date|geo|array|object
	Required bool        `json:"required,omitempty"`
	Default  interface{} `json:"default,omitempty"`
	Index    bool        `json:"index,omitempty"`
	Unique   bool        `json:"unique,omitempty"`
}

// HookSet holds optional JS code or file paths for each lifecycle event.
// The *File variant is true when the corresponding value is a file path;
// false means the value is inline code.
type HookSet struct {
	OnList            string `json:"onList,omitempty"`
	OnListFile        bool   `json:"onListFile,omitempty"`
	OnRead            string `json:"onRead,omitempty"`
	OnReadFile        bool   `json:"onReadFile,omitempty"`
	OnCreate          string `json:"onCreate,omitempty"`
	OnCreateFile      bool   `json:"onCreateFile,omitempty"`
	OnUpdate          string `json:"onUpdate,omitempty"`
	OnUpdateFile      bool   `json:"onUpdateFile,omitempty"`
	OnDelete          string `json:"onDelete,omitempty"`
	OnDeleteFile      bool   `json:"onDeleteFile,omitempty"`
	OnListTrash       string `json:"onListTrash,omitempty"`
	OnListTrashFile   bool   `json:"onListTrashFile,omitempty"`
	OnReadTrash       string `json:"onReadTrash,omitempty"`
	OnReadTrashFile   bool   `json:"onReadTrashFile,omitempty"`
	OnDeleteTrash     string `json:"onDeleteTrash,omitempty"`
	OnDeleteTrashFile bool   `json:"onDeleteTrashFile,omitempty"`
}

// ─────────────────────────────────────────────────────────────────────────────
// CrudDocument
// ─────────────────────────────────────────────────────────────────────────────

type CrudDocument struct {
	ID          string     `gorm:"primaryKey"      json:"id"`
	SchemaID    string     `gorm:"index;not null"  json:"schema_id"`
	NamespaceID string     `gorm:"index;not null"  json:"namespace_id"`
	// JSON arbitrary payload
	Data        string     `gorm:"type:text"       json:"data"`
	// JSON arbitrary metadata
	Meta        string     `gorm:"type:text"       json:"meta"`
	// GeoJSON point: {"type":"Point","coordinates":[lng,lat]}
	Geo         string     `gorm:"type:text"       json:"geo"`
	// NULL = not deleted (soft delete)
	DeletedAt   *time.Time `gorm:"index"           json:"deleted_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// OAuthState tracks in-flight OAuth2 state parameters (CSRF protection).
type OAuthState struct {
	State       string    `gorm:"primaryKey"`
	Provider    string
	NamespaceID string
	CreatedAt   time.Time
}
