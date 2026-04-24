package auth

import (
	"beba/modules"
	"fmt"
)

func Initialize(configs map[string]*AuthManagerConfig) error {
	for name, cfg := range configs {
		m := GetManager(name)
		if m == nil {
			m = NewManager(name, cfg.Secret)
		} else {
			m.mu.Lock()
			m.secret = cfg.Secret
			m.strategies = nil
			m.clients = make(map[string]*OAuth2Client)
			m.mu.Unlock()
		}
		
		if cfg.Server != nil {
			m.mu.Lock()
			m.serverConfig = cfg.Server
			m.mu.Unlock()
		}

		if cfg.Database != "" {
			err := m.initDB(cfg.Database)
			if err != nil {
				fmt.Printf("Warning: failed to initialize auth database for %s: %v\n", name, err)
			}
		} else {
			m.initDB("sqlite://:memory:")
		}

		// Add strategies
		for _, sCfg := range cfg.Strategies {
			m.AddStrategy(sCfg)
		}
		// Add OAuth2 clients
		for _, cCfg := range cfg.Clients {
			m.AddClient(&OAuth2Client{
				ID:           cCfg.ID,
				Secret:       cCfg.Secret,
				RedirectURIs: cCfg.RedirectURIs,
				Scopes:       cCfg.Scopes,
			})
		}
		fmt.Printf("Auth Manager [%s] initialized with %d strategies\n", name, len(cfg.Strategies))
	}
	return nil
}

func init() {
	modules.RegisterModule(&JSModule{})
}
