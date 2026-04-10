package config

import "reflect"

// MergeInto applique les champs non-zéro de override dans base en place.
// Zéro allocation — base est modifié par pointeur, aucune copie de struct.
//
// Règle de merge : un champ de override écrase base seulement s'il est
// non-zéro (IsZero() == false). Cela permet d'exprimer "je n'ai pas
// fourni cette valeur" avec la valeur zéro du type.
//
// Limitation connue : si une valeur légitimement zéro doit écraser la
// valeur de base (ex: WriteTimeout = 0 pour désactiver), il faut définir
// le champ avant le merge ou utiliser MergeForce.
func MergeInto(base, override *AppConfig) {
	// fmt.Printf("DEBUG: MergeInto base=%p, override=%p\n", base, override)
	if base == nil || override == nil {
		return
	}
	b := reflect.ValueOf(base).Elem()
	o := reflect.ValueOf(override).Elem()

	for i := 0; i < b.NumField(); i++ {
		ov := o.Field(i)
		if !ov.IsZero() {
			b.Field(i).Set(ov)
		}
	}
}

// MergeConfig est l'API compatibilité avec l'ancien code.
// Préférer MergeInto pour éviter les allocations.
func MergeConfig(base, override AppConfig) AppConfig {
	MergeInto(&base, &override)
	return base
}

// MergeForce écrase base avec tous les champs de override, y compris les
// valeurs zéro. Utile pour les flags CLI où 0 est une valeur explicite.
func MergeForce(base, override *AppConfig) {
	*base = *override
}

// MergeSelected fusionne uniquement les champs nommés dans fields.
// fields correspond aux noms de champs Go (pas les tags JSON).
// Ex: MergeSelected(&base, &override, "Port", "Address")
func MergeSelected(base, override *AppConfig, fields ...string) {
	set := make(map[string]struct{}, len(fields))
	for _, f := range fields {
		set[f] = struct{}{}
	}

	b := reflect.ValueOf(base).Elem()
	o := reflect.ValueOf(override).Elem()
	t := b.Type()

	for i := 0; i < b.NumField(); i++ {
		name := t.Field(i).Name
		if _, ok := set[name]; ok {
			b.Field(i).Set(o.Field(i))
		}
	}
}
