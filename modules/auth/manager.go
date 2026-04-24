package auth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"beba/types"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var (
	managers   = make(map[string]*Manager)
	managersMu sync.RWMutex
)

type Manager struct {
	name         string
	secret       string
	strategies   []Strategy
	clients      map[string]*OAuth2Client
	serverConfig *OAuth2ServerConfig
	db           *gorm.DB
	mu           sync.RWMutex
}

type AuthToken struct {
	JTI       string    `gorm:"primaryKey"`
	UserID    string    `gorm:"index"`
	ExpiresAt time.Time `gorm:"index"`
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

func (m *Manager) initDB(dsn string) error {
	var dialector gorm.Dialector
	if strings.HasPrefix(dsn, "sqlite://") {
		dialector = sqlite.Open(strings.TrimPrefix(dsn, "sqlite://"))
	} else if strings.HasPrefix(dsn, "mysql://") {
		dialector = mysql.Open(strings.TrimPrefix(dsn, "mysql://"))
	} else if strings.HasPrefix(dsn, "postgres://") {
		dialector = postgres.Open(strings.TrimPrefix(dsn, "postgres://"))
	} else {
		// Default to sqlite
		dialector = sqlite.Open(dsn)
	}

	db, err := gorm.Open(dialector, &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return err
	}
	m.db = db
	return db.AutoMigrate(&AuthToken{})
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
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, s := range m.strategies {
		if ac, ok := s.(interface {
			UserInfo(string) (types.UserInfo, error)
		}); ok {
			if info, err := ac.UserInfo(username); err == nil {
				return info, nil
			}
		}
	}
	return nil, errors.New("user not found")
}
