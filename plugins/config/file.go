package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

// ResolveConfigFile résout un chemin de fichier de configuration.
// Si le chemin n'a pas d'extension, il essaie .json, .yaml, .yml, .toml.
func ResolveConfigFile(path string) (string, error) {
	if filepath.Ext(path) != "" {
		if fileExists(path) {
			return path, nil
		}
		return "", fmt.Errorf("config file not found: %s", path)
	}

	for _, ext := range []string{".json", ".yaml", ".yml", ".toml"} {
		if candidate := path + ext; fileExists(candidate) {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("config file not found: %s (.json/.yaml/.yml/.toml)", path)
}

// LoadConfigFile charge un fichier de configuration et retourne un AppConfig partiel.
func LoadConfigFile(path string) (*AppConfig, error) {
	cfg := &AppConfig{}

	file, err := ResolveConfigFile(path)
	if err != nil {
		return cfg, err
	}

	data, err := os.ReadFile(file)
	if err != nil {
		return cfg, err
	}

	switch filepath.Ext(file) {
	case ".json":
		err = json.Unmarshal(data, &cfg)
	case ".yaml", ".yml":
		err = yaml.Unmarshal(data, &cfg)
	case ".toml":
		err = toml.Unmarshal(data, &cfg)
	default:
		return cfg, fmt.Errorf("unsupported config format: %s", filepath.Ext(file))
	}

	return cfg, err
}

// LoadConfigFiles charge plusieurs fichiers et les fusionne dans l'ordre.
func LoadConfigFiles(files []string) (*AppConfig, error) {
	cfg := &AppConfig{}

	for _, f := range files {
		c, err := LoadConfigFile(f)
		if err != nil {
			continue // fichier optionnel absent
		}
		MergeInto(cfg, c)
	}

	return cfg, nil
}
