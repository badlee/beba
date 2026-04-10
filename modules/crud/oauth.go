package crud

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// oauth2Config is the runtime representation of one OAUTH2 DEFINE block.
type oauth2Config struct {
	Name         string
	ClientID     string
	ClientSecret string
	RedirectURL  string
	Endpoint     string // authorization endpoint
	TokenURL     string
	UserinfoURL  string
	Scopes       []string
	// fieldMap maps provider JSON keys → our standard keys
	// e.g. {"sub":"id","email":"email","name":"login"} for GitHub
	FieldMap     map[string]string
}

// authURL builds the provider authorization URL with a random state param.
func (p *oauth2Config) authURL(state string) string {
	v := url.Values{}
	v.Set("response_type", "code")
	v.Set("client_id", p.ClientID)
	v.Set("redirect_uri", p.RedirectURL)
	v.Set("scope", strings.Join(p.Scopes, " "))
	v.Set("state", state)
	return p.Endpoint + "?" + v.Encode()
}

// exchangeCode exchanges the authorization code for an access token.
func (p *oauth2Config) exchangeCode(ctx context.Context, code string) (string, error) {
	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("redirect_uri", p.RedirectURL)
	data.Set("client_id", p.ClientID)
	data.Set("client_secret", p.ClientSecret)

	req, err := http.NewRequestWithContext(ctx, "POST", p.TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("token exchange: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("token exchange: bad JSON: %w", err)
	}
	tok, ok := result["access_token"].(string)
	if !ok || tok == "" {
		return "", fmt.Errorf("token exchange: no access_token in response")
	}
	return tok, nil
}

// userinfo fetches user profile from the provider and returns (sub, email, name).
func (p *oauth2Config) userinfo(ctx context.Context, accessToken string) (sub, email, name string, err error) {
	req, err := http.NewRequestWithContext(ctx, "GET", p.UserinfoURL, nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var info map[string]interface{}
	if err = json.Unmarshal(body, &info); err != nil {
		return
	}

	// Apply field map (defaults: sub=sub, email=email, name=name)
	fm := p.FieldMap
	if fm == nil {
		fm = map[string]string{"sub": "sub", "email": "email", "name": "name"}
	}
	get := func(key string) string {
		mapped := fm[key]
		if mapped == "" {
			mapped = key
		}
		if v, ok := info[mapped]; ok {
			return fmt.Sprint(v)
		}
		return ""
	}
	sub   = get("sub")
	email = get("email")
	name  = get("name")
	return
}

// ─────────────────────────────────────────────────────────────────────────────
// HTTP handlers
// ─────────────────────────────────────────────────────────────────────────────

// registerOAuthRoutes mounts the OAuth2 routes on the fiber app.
// prefix is e.g. "/crud" or "/crud/acme" (namespace-scoped).
func registerOAuthRoutes(
	app *fiber.App,
	prefix string,
	ns *Namespace,
	providers map[string]*oauth2Config,
	db *gorm.DB,
	secret string,
) {
	for pName, pCfg := range providers {
		p := pCfg // capture
		n := ns   // capture

		// GET /prefix/auth/{provider}  → redirect to provider
		app.Get(prefix+"/auth/"+pName, func(c fiber.Ctx) error {
			state := newID()
			db.Create(&OAuthState{
				State:       state,
				Provider:    pName,
				NamespaceID: n.ID,
				CreatedAt:   time.Now(),
			})
			return c.Redirect().To(p.authURL(state))
		})

		// GET /prefix/auth/{provider}/callback  → exchange code
		app.Get(prefix+"/auth/"+pName+"/callback", func(c fiber.Ctx) error {
			code  := c.Query("code")
			state := c.Query("state")

			// Validate state
			var oas OAuthState
			if err := db.Where("state = ? AND provider = ?", state, pName).
				First(&oas).Error; err != nil {
				return c.Status(400).JSON(fiber.Map{"error": "invalid state"})
			}
			db.Delete(&oas)

			// Exchange code for access token
			accessToken, err := p.exchangeCode(c.Context(), code)
			if err != nil {
				return c.Status(500).JSON(fiber.Map{"error": err.Error()})
			}

			// Fetch user info
			sub, email, name, err := p.userinfo(c.Context(), accessToken)
			if err != nil {
				return c.Status(500).JSON(fiber.Map{"error": err.Error()})
			}

			// Create/find user and issue JWT
			token, user, err := finalizeOAuth(db, n, pName, sub, email, name, secret)
			if err != nil {
				return c.Status(401).JSON(fiber.Map{"error": err.Error()})
			}
			return c.JSON(fiber.Map{"token": token, "user": publicUser(user)})
		})
	}
}

// publicUser strips sensitive fields from a User for API responses.
func publicUser(u *User) fiber.Map {
	return fiber.Map{
		"id":           u.ID,
		"username":     u.Username,
		"email":        u.Email,
		"namespace_id": u.NamespaceID,
		"role_id":      u.RoleID,
		"is_active":    u.IsActive,
		"created_at":   u.CreatedAt,
	}
}
