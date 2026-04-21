package auth

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"beba/types"

	"github.com/joho/godotenv"
	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

func (a *AuthConfigs) Append(c *AuthConfig) { *a = append(*a, c) }

func (a AuthConfigs) Auth(username, password string, token ...string) error {
	for _, ac := range a {
		if err := ac.Auth(username, password, token...); err == nil {
			return nil
		}
	}
	return errors.New("unauthorized")
}

func (auth *AuthConfig) Name() string {
	return string(auth.Type)
}

func (a AuthConfigs) UserInfo(username string) (types.UserInfo, error) {
	for _, ac := range a {
		if r, err := ac.UserInfo(username); err == nil {
			return r, nil
		}
	}
	return nil, errors.New("user not found")
}

func (auth *AuthConfig) Auth(username, password string, token ...string) error {
	creds := map[string]string{
		"username": username,
		"password": password,
	}
	if len(token) > 0 {
		creds["token"] = token[0]
	}
	_, err := auth.Authenticate(context.Background(), creds)
	return err
}

func (auth *AuthConfig) Authenticate(ctx context.Context, creds map[string]string) (*User, error) {
	username := creds["username"]
	password := creds["password"]

	switch auth.Type {
	case AuthUser:
		if auth.User == username && CheckPassword(auth.Password, password) {
			return &User{ID: username, Username: username}, nil
		}
	case AuthCSV:
		path := auth.Filepath
		if !filepath.IsAbs(path) && auth.BaseDir != "" {
			path = filepath.Join(auth.BaseDir, path)
		}
		f, err := os.Open(path)
		if err == nil {
			defer f.Close()
			r := csv.NewReader(f)
			r.Comma = ';'
			records, err := r.ReadAll()
			if err == nil {
				for _, rec := range records {
					if len(rec) >= 2 && rec[0] == username && CheckPassword(rec[1], password) {
						u := &User{ID: username, Username: username, Metadata: make(map[string]any)}
						if len(rec) >= 3 {
							u.Metadata["proto"] = rec[2] == "true"
						}
						return u, nil
					}
				}
			}
		}
	case AuthFile:
		path := auth.Filepath
		if !filepath.IsAbs(path) && auth.BaseDir != "" {
			path = filepath.Join(auth.BaseDir, path)
		}
		data, err := os.ReadFile(path)
		if err == nil {
			var users map[string]string
			switch strings.ToUpper(auth.Format) {
			case "JSON":
				json.Unmarshal(data, &users)
			case "YAML":
				yaml.Unmarshal(data, &users)
			case "TOML":
				toml.Unmarshal(data, &users)
			case "ENV":
				users, _ = godotenv.Unmarshal(string(data))
			}
			if pwd, ok := users[username]; ok && CheckPassword(pwd, password) {
				return &User{ID: username, Username: username}, nil
			}
		}
	case AuthScript:
		strategy := &ScriptStrategy{
			Code:    auth.Handler,
			IsFile:  !auth.Inline,
			BaseDir: auth.BaseDir,
			Configs: auth.Configs,
		}
		return strategy.Authenticate(ctx, creds)
	}
	return nil, errors.New("unauthorized")
}

func (auth *AuthConfig) UserInfo(username string) (types.UserInfo, error) {
	// Minimal implementation for legacy support
	// In a real scenario, we'd need to search for the user first.
	// For now, we'll just return a mock if it's AuthUser.
	if auth.Type == AuthUser && auth.User == username {
		return &AuthResult{Username: username, Secret: auth.Password}, nil
	}
	// For CSV/File, we'd need to read the file again.
	return nil, errors.New("not found")
}
