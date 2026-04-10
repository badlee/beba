package config

import (
	"fmt"
	"path/filepath"
)

// ResolveEnvFile résout un chemin de fichier d'environnement.
// Si le chemin n'a pas d'extension, essaie le chemin brut, puis .env, puis .conf.
func ResolveEnvFile(path string) (string, error) {
	if filepath.Ext(path) != "" {
		if fileExists(path) {
			return path, nil
		}
		return "", fmt.Errorf("env file not found: %s", path)
	}

	for _, candidate := range []string{path, path + ".env", path + ".conf"} {
		if fileExists(candidate) {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("env file not found: %s", path)
}
