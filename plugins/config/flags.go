package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/pflag"
)

// flagMeta contient les métadonnées extraites des tags d'un champ AppConfig.
type flagMeta struct {
	long     string // nom long (ex: "port")
	short    string // shorthand 1 char, "" = aucun
	def      string // valeur par défaut brute (tag `default`, "" si absent)
	desc     string // description --help (tag `desc`, "" si absent)
	required bool   // true si le tag flag contient "!"
	isStatic bool   // true si le tag flag contient "#"
}

// parseFlagTag décode les trois tags d'un champ :
//
//	flag:"[#][!]long[|short]"   — nom + shorthand optionnel + obligatoire
//	default:"valeur"           — valeur par défaut (optionnel)
//	desc:"texte"               — description (optionnel)
//
// Règles de validation :
//   - short doit être exactement 1 caractère ASCII (lettre ou chiffre)
//   - long ne doit pas être vide
//
// Retourne (meta, true) si le tag flag est présent et valide, (zero, false) sinon.
func parseFlagTag(sf reflect.StructField) (flagMeta, bool) {
	raw := sf.Tag.Get("flag")
	if raw == "" || raw == "-" {
		return flagMeta{}, false
	}

	meta := flagMeta{
		def:  sf.Tag.Get("default"),
		desc: sf.Tag.Get("desc"),
	}

	// Détecter les préfixes "!" (requis) et "#" (statique)
	for len(raw) > 0 {
		if raw[0] == '!' {
			meta.required = true
			raw = raw[1:]
			continue
		}
		if raw[0] == '#' {
			meta.isStatic = true
			raw = raw[1:]
			continue
		}
		break
	}

	// Séparer "long" et "short" sur le pipe
	parts := strings.SplitN(raw, "|", 2)
	meta.long = parts[0]
	if meta.long == "" {
		return flagMeta{}, false
	}

	if len(parts) == 2 {
		short := parts[1]
		// Validation : exactement 1 caractère
		if len(short) != 1 {
			panic(fmt.Sprintf(
				"config: champ %s : tag flag|short %q doit être exactement 1 caractère",
				sf.Name, short,
			))
		}
		meta.short = short
	}

	return meta, true
}

// ParseFlags enregistre tous les flags via reflection sur les tags de AppConfig,
// parse os.Args, gère --help, vérifie les flags obligatoires, et retourne
// un AppConfig partiel (seuls les champs fournis par l'utilisateur sont non-zéro).
//
// Comportements spéciaux :
//   - time.Duration : le flag pflag est un int (secondes). Le champ n'est rempli
//     que si f.Changed, pour ne pas écraser les valeurs fichier/env avec le défaut.
//   - []string      : pflag.StringSlice, séparateur virgule.
//   - Flags requis  : erreur fatale si absent après parse.
//   - --help / -?   : affiche l'usage et quitte (os.Exit(0)).
func ParseFlags() (*AppConfig, error) {
	return parseFlagsArgs(os.Args[1:])
}

func parseFlagsArgs(args []string) (*AppConfig, error) {
	cfg := &AppConfig{}
	fs := pflag.NewFlagSet("http-server", pflag.ContinueOnError)
	fs.Usage = func() {} // On gère l'affichage nous-mêmes

	// durRefs : champs time.Duration — flag int intermédiaire à convertir post-parse
	type durRef struct {
		fieldIdx int
		flagName string
		intPtr   *int
	}
	var durRefs []durRef

	// negRefs : maps positive flags to their negative --no counterparts
	type negRef struct {
		fieldIdx int
		posName  string
		negName  string
		negPtr   *bool
	}
	var negRefs []negRef

	rt := reflect.TypeOf(AppConfig{})
	rv := reflect.ValueOf(cfg).Elem()

	// ---- Enregistrement des flags via reflection ----
	for i := 0; i < rt.NumField(); i++ {
		sf := rt.Field(i)
		fval := rv.Field(i)

		meta, ok := parseFlagTag(sf)
		if !ok {
			continue
		}

		switch sf.Type.Kind() {

		case reflect.String:
			p := fval.Addr().Interface().(*string)
			if meta.short != "" {
				fs.StringVarP(p, meta.long, meta.short, meta.def, meta.desc)
			} else {
				fs.StringVar(p, meta.long, meta.def, meta.desc)
			}

		case reflect.Bool:
			p := fval.Addr().Interface().(*bool)
			def := meta.def == "true"
			if meta.short != "" {
				fs.BoolVarP(p, meta.long, meta.short, def, meta.desc)
			} else {
				fs.BoolVar(p, meta.long, def, meta.desc)
			}

			// Support --no- prefix for flags not starting with "no-"
			if !strings.HasPrefix(meta.long, "no-") {
				negName := "no-" + meta.long
				negPtr := new(bool)
				fs.BoolVar(negPtr, negName, false, "Disable "+meta.long)
				_ = fs.MarkHidden(negName)
				negRefs = append(negRefs, negRef{
					fieldIdx: i,
					posName:  meta.long,
					negName:  negName,
					negPtr:   negPtr,
				})
			}

		case reflect.Int:
			p := fval.Addr().Interface().(*int)
			def, _ := strconv.Atoi(meta.def)
			if meta.short != "" {
				fs.IntVarP(p, meta.long, meta.short, def, meta.desc)
			} else {
				fs.IntVar(p, meta.long, def, meta.desc)
			}

		case reflect.Int64:
			if sf.Type == reflect.TypeOf(time.Duration(0)) {
				// Déclaré comme int (secondes) côté CLI
				n := new(int)
				def, _ := strconv.Atoi(meta.def)
				if meta.short != "" {
					fs.IntVarP(n, meta.long, meta.short, def, meta.desc)
				} else {
					fs.IntVar(n, meta.long, def, meta.desc)
				}
				durRefs = append(durRefs, durRef{
					fieldIdx: i,
					flagName: meta.long,
					intPtr:   n,
				})
			} else {
				p := fval.Addr().Interface().(*int64)
				def, _ := strconv.ParseInt(meta.def, 10, 64)
				if meta.short != "" {
					fs.Int64VarP(p, meta.long, meta.short, def, meta.desc)
				} else {
					fs.Int64Var(p, meta.long, def, meta.desc)
				}
			}

		case reflect.Slice:
			if sf.Type.Elem().Kind() == reflect.String {
				p := fval.Addr().Interface().(*[]string)
				var def []string
				if meta.def != "" {
					for _, s := range strings.Split(meta.def, ",") {
						def = append(def, strings.TrimSpace(s))
					}
				}
				if meta.short != "" {
					fs.StringSliceVarP(p, meta.long, meta.short, def, meta.desc)
				} else {
					fs.StringSliceVar(p, meta.long, def, meta.desc)
				}
			}
		}
	}

	// ---- Flag --help / -? ----
	help := fs.BoolP("help", "?", false, "Print this list and exit")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	// ---- Affichage de l'aide ----
	if *help {
		executable := os.Args[0]
		if exePath, err := os.Executable(); err == nil {
			if strings.HasPrefix(exePath, os.TempDir()) || strings.Contains(exePath, "/go-build/") {
				executable = filepath.Base(exePath)
			}
			if strings.HasPrefix(executable, "main") {
				executable = "go run ."
			}
		}
		fmt.Printf("Usage: %s [path] [options]\n", executable)
		fmt.Printf("       %s test [file] [options]\n\nOptions:\n", executable)
		fs.PrintDefaults()
		os.Exit(0)
	}

	// ---- Conversion post-parse : int → time.Duration ----
	// Uniquement si le flag a été fourni explicitement (Changed == true) :
	// un flag non fourni laisse le champ à 0 — MergeInto l'ignorera,
	// préservant les valeurs issues du fichier ou de l'env.
	rv2 := reflect.ValueOf(cfg).Elem()
	for _, dr := range durRefs {
		if f := fs.Lookup(dr.flagName); f != nil && f.Changed {
			rv2.Field(dr.fieldIdx).SetInt(int64(*dr.intPtr) * int64(time.Second))
		}
	}

	// ---- Résolution des flags négatifs --no- ----
	// Si le flag négatif est fourni, il peut forcer à false.
	// Règle : le dernier fourni dans args gagne en cas de conflit.
	for _, nr := range negRefs {
		posF := fs.Lookup(nr.posName)
		negF := fs.Lookup(nr.negName)

		if negF != nil && negF.Changed {
			if posF != nil && posF.Changed {
				// Les deux ont été fournis, trouver le dernier dans args
				posIdx, negIdx := -1, -1
				for i, arg := range args {
					if strings.HasPrefix(arg, "--"+nr.posName) {
						posIdx = i
					} else if strings.HasPrefix(arg, "--"+nr.negName) {
						negIdx = i
					}
				}
				if negIdx > posIdx {
					rv2.Field(nr.fieldIdx).SetBool(false)
				} else {
					rv2.Field(nr.fieldIdx).SetBool(true)
				}
			} else {
				// Seul le négatif est fourni
				rv2.Field(nr.fieldIdx).SetBool(false)
			}
		}
	}

	// ---- Vérification des flags obligatoires ----
	var missing []string
	for i := 0; i < rt.NumField(); i++ {
		meta, ok := parseFlagTag(rt.Field(i))
		if !ok || !meta.required {
			continue
		}
		f := fs.Lookup(meta.long)
		if f == nil || !f.Changed {
			missing = append(missing, "--"+meta.long)
		}
	}
	if len(missing) > 0 {
		return &AppConfig{}, fmt.Errorf(
			"flags obligatoires manquants : %s\nUtilisez --help pour la liste complète",
			strings.Join(missing, ", "),
		)
	}

	return cfg, nil
}

// IsBoolFlag retourne true si le flag nommé `name` (long ou short) correspond
// à un champ bool de AppConfig. Basé sur reflection — aucune liste manuelle.
//
// Utilisé par main.go pour distinguer les flags booléens (--gzip, pas de valeur
// séparée) des flags à valeur (--port 8080).
func IsBoolFlag(name string) bool {
	rt := reflect.TypeOf(AppConfig{})
	for i := 0; i < rt.NumField(); i++ {
		meta, ok := parseFlagTag(rt.Field(i))
		if !ok {
			continue
		}
		if meta.long == name || meta.short == name {
			return rt.Field(i).Type.Kind() == reflect.Bool
		}
	}
	// --help est un bool déclaré directement dans ParseFlags
	if name == "help" || name == "?" {
		return true
	}

	// Gérer le cas --no-xxx
	if strings.HasPrefix(name, "no-") {
		baseName := strings.TrimPrefix(name, "no-")
		for i := 0; i < rt.NumField(); i++ {
			meta, ok := parseFlagTag(rt.Field(i))
			if !ok {
				continue
			}
			if meta.long == baseName {
				return rt.Field(i).Type.Kind() == reflect.Bool
			}
		}
	}

	return false
}

// FlagNames retourne tous les noms longs déclarés dans les tags `flag` de AppConfig.
// Utile pour valider la propagation aux processus enfants.
func FlagNames() []string {
	rt := reflect.TypeOf(AppConfig{})
	names := make([]string, 0, rt.NumField())
	for i := 0; i < rt.NumField(); i++ {
		meta, ok := parseFlagTag(rt.Field(i))
		if ok {
			names = append(names, meta.long)
		}
	}
	return names
}
