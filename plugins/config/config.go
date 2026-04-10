package config

import "time"

// AppConfig est la configuration centrale de l'application.
//
// Tags par système de résolution (priorité : défaut < fichier < env < flag CLI) :
//
//	json/yaml/toml  → sérialisation fichier
//	mapstructure    → intégration viper/mapstructure
//	env             → variable d'environnement (auto-mappée via reflection)
//	validate        → règles de validation (auto-appliquées via reflection)
//
//	flag            → "[!][#]long[|short]"
//	                    !      : flag obligatoire — erreur si absent après parse
//	                    #      : flag statique — un changement via hot-reload sera ignoré
//	                    long   : nom long (ex: "port")
//	                    |short : shorthand 1 caractère optionnel (ex: "|p")
//	default         → valeur par défaut CLI (tag séparé, optionnel)
//	                    Pour time.Duration : valeur en secondes (entier)
//	desc            → description --help (tag séparé, optionnel)
//
// Exemples :
//
//	Port    int           `flag:"port|p"  default:"8080"     desc:"Port to listen on"`
//	Cert    string        `flag:"!cert"   default:"cert.pem" desc:"TLS certificate"`
//	Socket  string        `flag:"socket|s"`
//	Timeout time.Duration `flag:"timeout" default:"30"       desc:"Timeout (seconds)"`
type AppConfig struct {
	// Securite
	SecretKey string `json:"secret_key"  yaml:"secret_key"  toml:"secret_key"  mapstructure:"secret_key"  env:"SECRET_KEY"  validate:"min=0" flag:"secret-key"  default:"120" desc:"Secret user for cookie's encription and session"`

	// ---- sources -------------------------------------------------------
	ConfigFiles []string `json:"config_files,omitempty" yaml:"config_files,omitempty" toml:"config_files,omitempty" mapstructure:"config_files" env:"CONFIG_FILES" flag:"#config-file|c"   desc:"Config files (json/yaml/toml), comma-separated"`
	EnvFiles    []string `json:"env_files,omitempty"    yaml:"env_files,omitempty"    toml:"env_files,omitempty"    mapstructure:"env_files"    env:"ENV_FILES"    flag:"#env-file"        desc:"Env files (.env/.conf), comma-separated"`
	EnvPrefix   string   `json:"env_prefix,omitempty"   yaml:"env_prefix,omitempty"   toml:"env_prefix,omitempty"   mapstructure:"env_prefix"   env:"PREFIX"        flag:"#env-prefix"      desc:"Environment variable prefix"`
	HotReload   bool     `json:"hot_reload"  yaml:"hot_reload"  toml:"hot_reload"  mapstructure:"hot_reload"  env:"HOT_RELOAD"  flag:"hot-reload|H"  default:"true"       desc:"Enable hot-reload"`

	// ---- bind ----------------------------------------------------------
	Port    int    `json:"port"             yaml:"port"             toml:"port"             mapstructure:"port"    env:"PORT"    validate:"min=1,max=65535" flag:"#port|p"    default:"8080"    desc:"Port to listen on"`
	Address string `json:"address"          yaml:"address"          toml:"address"          mapstructure:"address" env:"ADDRESS" validate:"ip_or_hostname"  flag:"#address|a" default:"0.0.0.0" desc:"Bind address"`
	Socket        string `json:"socket,omitempty" yaml:"socket,omitempty" toml:"socket,omitempty" mapstructure:"socket"  env:"SOCKET"                            flag:"#socket|s"                    desc:"Unix domain socket path"`
	ControlSocket string `json:"control_socket,omitempty" yaml:"control_socket,omitempty" toml:"control_socket,omitempty" mapstructure:"control_socket" env:"CONTROL_SOCKET" flag:"#control-socket"           desc:"Control Unix domain socket path for IPC"`

	// ---- directory -----------------------------------------------------
	DirListing bool   `json:"dir_listing" yaml:"dir_listing" toml:"dir_listing" mapstructure:"dir_listing" env:"DIR_LISTING" flag:"dir-listing|L" default:"true"       desc:"Show directory listings"`
	AutoIndex  bool   `json:"auto_index"  yaml:"auto_index"  toml:"auto_index"  mapstructure:"auto_index"  env:"AUTO_INDEX"  flag:"auto-index|I"  default:"true"       desc:"Serve index file for directories"`
	IndexFile  string `json:"index"       yaml:"index"       toml:"index"       mapstructure:"index"       env:"INDEX"       flag:"index"         default:"index.html" desc:"Index filename"`

	// ---- template ------------------------------------------------------
	HtmxURL     string `json:"htmx_url"              yaml:"htmx_url"              toml:"htmx_url"              mapstructure:"htmx_url"     env:"HTMX_URL"     flag:"htmx-url"      default:"https://unpkg.com/htmx.org@2.0.0" desc:"HTMX CDN URL"`
	NoHtmx      bool   `json:"no_htmx"               yaml:"no_htmx"               toml:"no_htmx"               mapstructure:"no_htmx"      env:"NO_HTMX"      flag:"no-htmx"                                            desc:"Disable HTMX injection"`
	InjectHTML  string `json:"inject_html,omitempty" yaml:"inject_html,omitempty" toml:"inject_html,omitempty" mapstructure:"inject_html"  env:"INJECT_HTML"  flag:"inject-html"                                        desc:"Raw HTML injected into every page"`
	TemplateExt string `json:"template_ext"          yaml:"template_ext"          toml:"template_ext"          mapstructure:"template_ext" env:"TEMPLATE_EXT" flag:"template-ext|e" default:".html"                    desc:"Template file extension"`
	NoTemplate  bool   `json:"no_template"           yaml:"no_template"           toml:"no_template"           mapstructure:"no_template"  env:"NO_TEMPLATE"  flag:"no-template"                                        desc:"Disable template engine"`

	// ---- compression ---------------------------------------------------
	Gzip    bool `json:"gzip"    yaml:"gzip"    toml:"gzip"    mapstructure:"gzip"    env:"GZIP"    flag:"gzip|G"    default:"true" desc:"Enable gzip compression"`
	Brotli  bool `json:"brotli"  yaml:"brotli"  toml:"brotli"  mapstructure:"brotli"  env:"BROTLI"  flag:"brotli|B"  default:"true" desc:"Enable brotli compression"`
	Deflate bool `json:"deflate" yaml:"deflate" toml:"deflate" mapstructure:"deflate" env:"DEFLATE" flag:"deflate|D" default:"true" desc:"Enable deflate compression"`

	// ---- logging -------------------------------------------------------
	Silent bool   `json:"silent" yaml:"silent" toml:"silent" mapstructure:"silent" env:"SILENT" flag:"silent" desc:"Suppress log output"`
	Stdout string `json:"stdout,omitempty" yaml:"stdout,omitempty" toml:"stdout,omitempty" mapstructure:"stdout" env:"STDOUT" flag:"stdout" desc:"File to mirror stdout into"`
	Stderr string `json:"stderr,omitempty" yaml:"stderr,omitempty" toml:"stderr,omitempty" mapstructure:"stderr" env:"STDERR" flag:"stderr" desc:"File to mirror stderr into"`

	// ---- server --------------------------------------------------------
	CORS      bool `json:"cors" yaml:"cors" toml:"cors" mapstructure:"cors" env:"CORS" flag:"cors" desc:"Enable CORS middleware"`
	CacheTime int  `json:"cache" yaml:"cache" toml:"cache" mapstructure:"cache" env:"CACHE" validate:"min=0" flag:"cache" default:"3600" desc:"Static file cache duration (seconds)"`

	// ---- proxy ---------------------------------------------------------
	ProxyURL string `json:"proxy,omitempty" yaml:"proxy,omitempty" toml:"proxy,omitempty" mapstructure:"proxy" env:"PROXY" flag:"proxy" desc:"Fallback reverse-proxy URL"`

	// ---- TLS -----------------------------------------------------------
	HTTPS bool   `json:"https" yaml:"https" toml:"https" mapstructure:"https" env:"HTTPS" flag:"#secure|S" default:"true" desc:"Enable HTTPS"`
	Cert  string `json:"cert,omitempty" yaml:"cert,omitempty" toml:"cert,omitempty" mapstructure:"cert" env:"CERT" flag:"#cert" default:"cert.pem" desc:"TLS certificate file"`
	Key   string `json:"key,omitempty" yaml:"key,omitempty" toml:"key,omitempty" mapstructure:"key" env:"KEY" flag:"#key" default:"key.pem" desc:"TLS private key file"`

	// ---- robots --------------------------------------------------------
	Robots     bool   `json:"robots" yaml:"robots" toml:"robots" mapstructure:"robots" env:"ROBOTS" flag:"robots-allowed|R" default:"true" desc:"Respond to /robots.txt"`
	RobotsFile string `json:"robots_file" yaml:"robots_file" toml:"robots_file" mapstructure:"robots_file" env:"ROBOTS_FILE" flag:"robots-file" default:"robots.txt" desc:"robots.txt file path"`

	// ---- timeouts ------------------------------------------------------
	// Sur la CLI : valeurs en secondes (int). En mémoire : time.Duration.
	// WriteTimeout = 0 : désactivé — indispensable pour SSE/WS longue durée.
	ReadTimeout  time.Duration `json:"read_timeout"  yaml:"read_timeout"  toml:"read_timeout"  mapstructure:"read_timeout"  env:"READ_TIMEOUT"  validate:"min=0" flag:"read-timeout"  default:"30"  desc:"Read timeout in seconds (0=disabled)"`
	WriteTimeout time.Duration `json:"write_timeout" yaml:"write_timeout" toml:"write_timeout" mapstructure:"write_timeout" env:"WRITE_TIMEOUT" validate:"min=0" flag:"write-timeout" default:"0"   desc:"Write timeout in seconds (0=disabled, recommended for SSE/WS)"`
	IdleTimeout  time.Duration `json:"idle_timeout"  yaml:"idle_timeout"  toml:"idle_timeout"  mapstructure:"idle_timeout"  env:"IDLE_TIMEOUT"  validate:"min=0" flag:"idle-timeout"  default:"120" desc:"Idle keep-alive timeout in seconds (0=disabled)"`

	// ---- test ----------------------------------------------------------
	Find  string `json:"find,omitempty"  yaml:"find,omitempty"  toml:"find,omitempty"  mapstructure:"find"  env:"FIND"  flag:"find"  desc:"CSS selector for test mode"`
	Match string `json:"match,omitempty" yaml:"match,omitempty" toml:"match,omitempty" mapstructure:"match" env:"MATCH" flag:"match" desc:"Validation expression for test mode"`

	// ---- vhost ---------------------------------------------------------
	VHosts   bool     `json:"vhosts"              yaml:"vhosts"              toml:"vhosts"              mapstructure:"vhosts"    env:"VHOSTS"    flag:"#vhosts|V"  desc:"Enable virtual host mode"`
	BindFiles []string `json:"bind_files,omitempty" yaml:"bind_files,omitempty" toml:"bind_files,omitempty" mapstructure:"bind_files" env:"BIND_FILES" flag:"#bind|b"       desc:"Paths to .bind protocol configs"`
}

// DefaultConfig retourne la configuration par défaut.
// WriteTimeout = 0 : désactivé — SSE/WS nécessitent des connexions longue durée.
func DefaultConfig() *AppConfig {
	return &AppConfig{
		SecretKey:   "",
		ConfigFiles: []string{"app"},
		EnvFiles:    []string{".env"},
		EnvPrefix:   "APP_",
		HotReload:   true,

		Port:    8080,
		Address: "0.0.0.0",

		DirListing:  true,
		AutoIndex:   true,
		IndexFile:   "index.html",
		HtmxURL:     "https://unpkg.com/htmx.org@2.0.0",
		TemplateExt: ".html",

		Gzip:    true,
		Brotli:  true,
		Deflate: true,

		CacheTime:  3600,
		Cert:       "cert.pem",
		Key:        "key.pem",
		RobotsFile: "robots.txt",

		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0,
		IdleTimeout:  120 * time.Second,
	}
}
