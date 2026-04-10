package config

// LoadConfig charge la configuration dans l'ordre de priorité :
//
//  1. Défauts (DefaultConfig)
//  2. Fichiers config (JSON / YAML / TOML)
//  3. Variables d'environnement (auto-mapping via tags `env`)
//  4. Flags CLI (priorité maximale)
//
// La config résultante est validée avant d'être retournée.
func LoadConfig() (*AppConfig, error) {
	// 0. Défauts
	cfg := DefaultConfig()

	// 1. Flags — on les parse en premier pour connaître les chemins des fichiers
	flags, err := ParseFlags()
	if err != nil {
		return cfg, err
	}

	// Résoudre les chemins depuis les flags ou les défauts
	configFiles := cfg.ConfigFiles
	if len(flags.ConfigFiles) > 0 {
		configFiles = flags.ConfigFiles
	}
	envFiles := cfg.EnvFiles
	if len(flags.EnvFiles) > 0 {
		envFiles = flags.EnvFiles
	}
	envPrefix := cfg.EnvPrefix
	if flags.EnvPrefix != "" {
		envPrefix = flags.EnvPrefix
	}

	// 2. Fichiers de configuration
	fileCfg, _ := LoadConfigFiles(configFiles)
	MergeInto(cfg, fileCfg)

	// 3. Variables d'environnement
	LoadEnvFiles(envFiles)
	envCfg := LoadEnv(envPrefix)
	MergeInto(cfg, envCfg)

	// 4. Flags CLI (priorité maximale)
	MergeInto(cfg, flags)

	// 5. Validation
	if err := Validate(cfg); err != nil {
		return cfg, err
	}

	return cfg, nil
}

// LoadConfigWithWatcher charge la config et démarre un watcher pour le hot-reload.
// Les fichiers surveillés sont déterminés par la config initiale.
//
// Usage :
//
//	w, cfg, err := LoadConfigWithWatcher()
//	defer w.Close()
//	w.OnChange(func(c *config.AppConfig) {
//	    // appliquer la nouvelle config
//	})
//	// utiliser cfg pour le démarrage, w.Config() pour les lectures suivantes
func LoadConfigWithWatcher() (*Watcher, *AppConfig, error) {
	w, err := NewWatcher(LoadConfig)
	if err != nil {
		return nil, nil, err
	}

	cfg := w.Config()

	// Surveiller les fichiers de configuration et d'environnement
	_ = w.Watch(cfg.ConfigFiles...)
	_ = w.Watch(cfg.EnvFiles...)

	return w, cfg, nil
}
