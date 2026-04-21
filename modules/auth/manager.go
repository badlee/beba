package auth

import (
	"context"
	"errors"
	"sync"

	"beba/types"
)

var (
	managers   = make(map[string]*Manager)
	managersMu sync.RWMutex
)

type Manager struct {
	name       string
	secret     string
	strategies []Strategy
	clients    map[string]*OAuth2Client
	mu         sync.RWMutex
}

func NewManager(name, secret string) *Manager {
	m := &Manager{
		name:    name,
		secret:  secret,
		clients: make(map[string]*OAuth2Client),
	}
	managersMu.Lock()
	managers[name] = m
	managersMu.Unlock()
	return m
}

func GetManager(name string) *Manager {
	managersMu.RLock()
	defer managersMu.RUnlock()
	return managers[name]
}

func (m *Manager) AddStrategy(s Strategy) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.strategies = append(m.strategies, s)
}

func (m *Manager) AddClient(c *OAuth2Client) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clients[c.ID] = c
}

// Authenticate attempts to authenticate using any available strategy.
func (m *Manager) Authenticate(ctx context.Context, strategyName string, creds map[string]string) (*User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, s := range m.strategies {
		if strategyName == "" || s.Name() == strategyName {
			user, err := s.Authenticate(ctx, creds)
			if err == nil {
				return user, nil
			}
		}
	}
	return nil, errors.New("authentication failed")
}

// Implement types.Authentification for protocol integration
func (m *Manager) Auth(username, password string, token ...string) error {
	creds := map[string]string{
		"username": username,
		"password": password,
	}
	if len(token) > 0 {
		creds["token"] = token[0]
	}
	_, err := m.Authenticate(context.Background(), "", creds)
	return err
}

func (m *Manager) UserInfo(username string) (types.UserInfo, error) {
	// This might need more thought on how to retrieve stored password/proto info
	// if we are doing Basic Auth.
	return nil, errors.New("UserInfo not implemented in unified manager yet")
}
