package auth

import (
	"fmt"
)

func Initialize(configs map[string]*AuthManagerConfig) error {
	for name, cfg := range configs {
		m := NewManager(name, cfg.Secret)
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
