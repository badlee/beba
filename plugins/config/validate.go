package config

import (
	"fmt"
	"net"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// ValidationError agrège toutes les erreurs de validation.
type ValidationError struct {
	Errors []string
}

func (e *ValidationError) Error() string {
	return "config validation failed:\n  - " + strings.Join(e.Errors, "\n  - ")
}

func (e *ValidationError) HasErrors() bool {
	return len(e.Errors) > 0
}

// Validate vérifie les contraintes des tags `validate` sur AppConfig.
// Règles supportées :
//   - min=N          : valeur numérique >= N (ou durée >= N*second si int)
//   - max=N          : valeur numérique <= N
//   - required       : champ non vide
//   - ip_or_hostname : string est une IP valide ou un hostname non vide
func Validate(cfg *AppConfig) error {
	ve := &ValidationError{}

	rv := reflect.ValueOf(cfg).Elem()
	rt := rv.Type()

	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		fval := rv.Field(i)
		tag := field.Tag.Get("validate")
		if tag == "" {
			continue
		}

		rules := strings.Split(tag, ",")
		for _, rule := range rules {
			rule = strings.TrimSpace(rule)
			if err := applyRule(field.Name, fval, rule); err != nil {
				ve.Errors = append(ve.Errors, err.Error())
			}
		}
	}

	// Règles croisées (dépendances entre champs)
	if cfg.HTTPS {
		if cfg.Cert == "" {
			ve.Errors = append(ve.Errors, "HTTPS=true requires Cert to be set")
		}
		if cfg.Key == "" {
			ve.Errors = append(ve.Errors, "HTTPS=true requires Key to be set")
		}
	}

	if ve.HasErrors() {
		return ve
	}
	return nil
}

func applyRule(fieldName string, fval reflect.Value, rule string) error {
	switch {
	case rule == "required":
		if fval.IsZero() {
			return fmt.Errorf("%s: required", fieldName)
		}

	case rule == "ip_or_hostname":
		s, ok := fval.Interface().(string)
		if !ok || s == "" {
			return fmt.Errorf("%s: must be a valid IP or hostname", fieldName)
		}
		if s != "0.0.0.0" && s != "::" && net.ParseIP(s) == nil {
			// Hostname basique : pas d'espace, pas de /
			if strings.ContainsAny(s, " /") {
				return fmt.Errorf("%s: invalid IP or hostname %q", fieldName, s)
			}
		}

	case strings.HasPrefix(rule, "min="):
		n, err := strconv.ParseInt(strings.TrimPrefix(rule, "min="), 10, 64)
		if err != nil {
			return nil
		}
		return checkMin(fieldName, fval, n)

	case strings.HasPrefix(rule, "max="):
		n, err := strconv.ParseInt(strings.TrimPrefix(rule, "max="), 10, 64)
		if err != nil {
			return nil
		}
		return checkMax(fieldName, fval, n)
	}
	return nil
}

func checkMin(fieldName string, fval reflect.Value, min int64) error {
	switch fval.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if fval.Type() == reflect.TypeOf(time.Duration(0)) {
			if fval.Int() < min*int64(time.Second) && fval.Int() != 0 {
				return fmt.Errorf("%s: must be >= %ds (or 0 to disable)", fieldName, min)
			}
			return nil
		}
		if fval.Int() < min {
			return fmt.Errorf("%s: must be >= %d, got %d", fieldName, min, fval.Int())
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if int64(fval.Uint()) < min {
			return fmt.Errorf("%s: must be >= %d, got %d", fieldName, min, fval.Uint())
		}
	case reflect.Float32, reflect.Float64:
		if fval.Float() < float64(min) {
			return fmt.Errorf("%s: must be >= %d, got %g", fieldName, min, fval.Float())
		}
	}
	return nil
}

func checkMax(fieldName string, fval reflect.Value, max int64) error {
	switch fval.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if fval.Int() > max {
			return fmt.Errorf("%s: must be <= %d, got %d", fieldName, max, fval.Int())
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if int64(fval.Uint()) > max {
			return fmt.Errorf("%s: must be <= %d, got %d", fieldName, max, fval.Uint())
		}
	case reflect.Float32, reflect.Float64:
		if fval.Float() > float64(max) {
			return fmt.Errorf("%s: must be <= %d, got %g", fieldName, max, fval.Float())
		}
	}
	return nil
}
