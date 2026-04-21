package auth

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/dop251/goja"
	"github.com/joho/godotenv"
	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

// StaticUserStrategy
type StaticUserStrategy struct {
	Username string
	Password string
}

func (s *StaticUserStrategy) Name() string { return "static" }
func (s *StaticUserStrategy) Authenticate(ctx context.Context, creds map[string]string) (*User, error) {
	u := creds["username"]
	p := creds["password"]
	if u == s.Username && CheckPassword(s.Password, p) {
		return &User{ID: u, Username: u}, nil
	}
	return nil, errors.New("invalid static credentials")
}

// FileStrategy handles JSON, YAML, TOML, ENV
type FileStrategy struct {
	Path   string
	Format string
}

func (s *FileStrategy) Name() string { return "file" }
func (s *FileStrategy) Authenticate(ctx context.Context, creds map[string]string) (*User, error) {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return nil, err
	}

	var users map[string]string
	switch strings.ToUpper(s.Format) {
	case "JSON":
		err = json.Unmarshal(data, &users)
	case "YAML":
		err = yaml.Unmarshal(data, &users)
	case "TOML":
		err = toml.Unmarshal(data, &users)
	case "ENV":
		users, err = godotenv.Unmarshal(string(data))
	default:
		return nil, fmt.Errorf("unsupported format: %s", s.Format)
	}

	if err != nil {
		return nil, err
	}

	u := creds["username"]
	p := creds["password"]
	if pwd, ok := users[u]; ok && CheckPassword(pwd, p) {
		return &User{ID: u, Username: u}, nil
	}
	return nil, errors.New("invalid file credentials")
}

// CSVStrategy
type CSVStrategy struct {
	Path string
}

func (s *CSVStrategy) Name() string { return "csv" }
func (s *CSVStrategy) Authenticate(ctx context.Context, creds map[string]string) (*User, error) {
	f, err := os.Open(s.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.Comma = ';'
	records, err := r.ReadAll()
	if err != nil {
		return nil, err
	}

	u := creds["username"]
	p := creds["password"]

	for _, rec := range records {
		if len(rec) < 2 {
			continue
		}
		if rec[0] == u && CheckPassword(rec[1], p) {
			metadata := make(map[string]any)
			if len(rec) >= 3 {
				metadata["proto"] = rec[2] == "true"
			}
			return &User{ID: u, Username: u, Metadata: metadata}, nil
		}
	}
	return nil, errors.New("invalid csv credentials")
}

// ScriptStrategy
type ScriptStrategy struct {
	Code    string
	IsFile  bool
	BaseDir string
	Configs map[string]string
}

func (s *ScriptStrategy) Name() string { return "script" }
func (s *ScriptStrategy) Authenticate(ctx context.Context, creds map[string]string) (*User, error) {
	var code []byte
	var err error
	if s.IsFile {
		code, err = os.ReadFile(s.Code)
		if err != nil {
			return nil, err
		}
	} else {
		code = []byte(s.Code)
	}

	// We need a way to run JS. We'll use beba/processor but we need a fiber context or mock it.
	// Since Authenticate might not have a fiber context (e.g. DTP), we need a minimal VM.
	vm := goja.New()

	// Inject credentials
	vm.Set("username", creds["username"])
	vm.Set("user", creds["username"])
	vm.Set("password", creds["password"])
	vm.Set("pwd", creds["password"])
	vm.Set("config", s.Configs)

	// Result channel
	type result struct {
		user *User
		err  error
	}
	resChan := make(chan result, 1)

	vm.Set("allow", func(call goja.FunctionCall) goja.Value {
		u := &User{ID: creds["username"], Username: creds["username"]}
		if len(call.Arguments) > 0 {
			// allow(secret, proto_bool)
			if u.Metadata == nil {
				u.Metadata = make(map[string]any)
			}
			u.Metadata["secret"] = call.Arguments[0].Export()
			if len(call.Arguments) > 1 {
				u.Metadata["proto"] = call.Arguments[1].Export()
			}
		}
		resChan <- result{user: u}
		return goja.Undefined()
	})

	vm.Set("reject", func(msg string) goja.Value {
		resChan <- result{err: errors.New(msg)}
		return goja.Undefined()
	})

	// Run script
	_, err = vm.RunString(string(code))
	if err != nil {
		return nil, err
	}

	select {
	case res := <-resChan:
		return res.user, res.err
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return nil, errors.New("script did not call allow() or reject()")
	}
}
