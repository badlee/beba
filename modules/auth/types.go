package auth

import (
	"context"
	"time"
)

type User struct {
	ID        string         `json:"id"`
	Username  string         `json:"username"`
	Email     string         `json:"email"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	Namespace string         `json:"namespace,omitempty"`
}

type Session struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

type Strategy interface {
	Name() string
	Authenticate(ctx context.Context, creds map[string]string) (*User, error)
}

type AuthType string

const (
	AuthFile   AuthType = "FILE"
	AuthCSV    AuthType = "CSV"
	AuthUser   AuthType = "USER"
	AuthScript AuthType = "SCRIPT"
)

type AuthResult struct {
	Username string
	Secret   string
	UseProto bool
}

func (a *AuthResult) User() string {
	return a.Username
}

func (a *AuthResult) Pwd() string {
	return a.Secret
}
func (a *AuthResult) Proto() bool {
	return a.UseProto
}

type AuthConfig struct {
	Type     AuthType
	Format   string            // JSON, YAML, TOML, ENV (when Type==AuthFile)
	Filepath string            // for FILE and CSV
	User     string            // for AuthUser
	Password string            // for AuthUser
	Handler  string            // JS code or filepath (for AuthScript)
	Inline   bool              // true when handler is inline JS
	Configs  map[string]string // passed to script as `config`
	BaseDir  string
}

type AuthConfigs []*AuthConfig

type AuthManagerConfig struct {
	Name       string
	Database   string
	Secret     string
	Strategies AuthConfigs
	Clients    []*OAuth2ClientConfig
	BaseDir    string
}

type OAuth2ClientConfig struct {
	ID           string
	Secret       string
	RedirectURIs []string
	Scopes       []string
}

type OAuth2Client struct {
	ID           string   `json:"id"`
	Secret       string   `json:"-"`
	RedirectURIs []string `json:"redirect_uris"`
	Scopes       []string `json:"scopes"`
}
