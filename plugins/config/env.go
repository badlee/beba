package config

import (
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// LoadEnvFiles charge les fichiers .env dans l'ordre — chaque fichier suivant
// peut écraser les valeurs précédentes (Overload).
func LoadEnvFiles(files []string) {
	for _, f := range files {
		file, err := ResolveEnvFile(f)
		if err != nil {
			continue
		}
		_ = godotenv.Overload(file)
	}
}

// LoadEnv lit toutes les variables d'environnement correspondant aux tags `env`
// de AppConfig via reflection. Aucun mapping manuel requis.
//
// Parsing automatique selon le type du champ :
//   - string         → direct
//   - bool           → 1/true/yes/on
//   - int, int64     → strconv.Atoi / ParseInt
//   - float64        → ParseFloat
//   - time.Duration  → ParseDuration (ex: "30s", "2m", "1h")
//   - []string       → split sur virgule
func LoadEnv(prefix string) *AppConfig {
	cfg := &AppConfig{}
	setEnvFields(prefix, cfg)
	return cfg
}

// setEnvFields parcourt les champs de dst par reflection et remplit ceux
// dont le tag `env` correspond à une variable d'environnement non vide.
func setEnvFields(prefix string, dst any) {
	rv := reflect.ValueOf(dst).Elem()
	rt := rv.Type()

	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		fval := rv.Field(i)

		tag := field.Tag.Get("env")
		if tag == "" || tag == "-" {
			continue
		}

		raw := os.Getenv(prefix + tag)
		if raw == "" {
			continue
		}

		if err := setFieldFromString(fval, raw); err != nil {
			// valeur invalide → on ignore silencieusement,
			// le champ reste à sa valeur zéro
			continue
		}
	}
}

// setFieldFromString convertit la chaîne raw vers le type du champ cible.
func setFieldFromString(fval reflect.Value, raw string) error {
	switch fval.Kind() {
	case reflect.String:
		fval.SetString(raw)

	case reflect.Bool:
		fval.SetBool(parseBool(raw))

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		// time.Duration est un int64 — on tente ParseDuration en premier
		if fval.Type() == reflect.TypeOf(time.Duration(0)) {
			d, err := time.ParseDuration(raw)
			if err != nil {
				// fallback : si l'utilisateur fournit "30" on l'interprète en secondes
				n, err2 := strconv.ParseInt(raw, 10, 64)
				if err2 != nil {
					return err
				}
				fval.SetInt(n * int64(time.Second))
				return nil
			}
			fval.SetInt(int64(d))
			return nil
		}
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return err
		}
		fval.SetInt(n)

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return err
		}
		fval.SetUint(n)

	case reflect.Float32, reflect.Float64:
		n, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return err
		}
		fval.SetFloat(n)

	case reflect.Slice:
		if fval.Type().Elem().Kind() == reflect.String {
			parts := strings.Split(raw, ",")
			for i, p := range parts {
				parts[i] = strings.TrimSpace(p)
			}
			fval.Set(reflect.ValueOf(parts))
		}
	}

	return nil
}
