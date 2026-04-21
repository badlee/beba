package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type OAuth2Strategy struct {
	NameStr      string
	ClientID     string
	ClientSecret string
	RedirectURL  string
	Endpoint     string
	TokenURL     string
	UserinfoURL  string
	Scopes       []string
	FieldMap     map[string]string
}

func (s *OAuth2Strategy) Name() string { return s.NameStr }

func (s *OAuth2Strategy) AuthURL(state string) string {
	v := url.Values{}
	v.Set("response_type", "code")
	v.Set("client_id", s.ClientID)
	v.Set("redirect_uri", s.RedirectURL)
	v.Set("scope", strings.Join(s.Scopes, " "))
	v.Set("state", state)
	return s.Endpoint + "?" + v.Encode()
}

func (s *OAuth2Strategy) Authenticate(ctx context.Context, creds map[string]string) (*User, error) {
	code := creds["code"]
	if code == "" {
		return nil, fmt.Errorf("oauth2: missing authorization code")
	}

	accessToken, err := s.exchangeCode(ctx, code)
	if err != nil {
		return nil, err
	}

	return s.fetchUserinfo(ctx, accessToken)
}

func (s *OAuth2Strategy) exchangeCode(ctx context.Context, code string) (string, error) {
	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("redirect_uri", s.RedirectURL)
	data.Set("client_id", s.ClientID)
	data.Set("client_secret", s.ClientSecret)

	req, err := http.NewRequestWithContext(ctx, "POST", s.TokenURL, strings.NewReader(data.Encode()))
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

func (s *OAuth2Strategy) fetchUserinfo(ctx context.Context, accessToken string) (*User, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", s.UserinfoURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var info map[string]interface{}
	if err = json.Unmarshal(body, &info); err != nil {
		return nil, err
	}

	fm := s.FieldMap
	if fm == nil {
		fm = map[string]string{"sub": "id", "email": "email", "name": "username"}
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

	return &User{
		ID:       get("id"),
		Username: get("username"),
		Email:    get("email"),
		Metadata: info,
	}, nil
}
