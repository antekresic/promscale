// This file and its contents are licensed under the Apache License 2.0.
// Please see the included NOTICE for copyright information and
// LICENSE for a copy of the license.

package runner

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/peterbourgon/ff/v3"
	"github.com/peterbourgon/ff/v3/ffyaml"
	"github.com/timescale/promscale/pkg/api"
	"github.com/timescale/promscale/pkg/limits"
	"github.com/timescale/promscale/pkg/log"
	"github.com/timescale/promscale/pkg/pgclient"
	"github.com/timescale/promscale/pkg/tenancy"
	"github.com/timescale/promscale/pkg/util"
)

type Config struct {
	ListenAddr                  string
	ThanosStoreAPIListenAddr    string
	OTLPGRPCListenAddr          string
	PgmodelCfg                  pgclient.Config
	LogCfg                      log.Config
	APICfg                      api.Config
	LimitsCfg                   limits.Config
	TenancyCfg                  tenancy.Config
	ConfigFile                  string
	TLSCertFile                 string
	TLSKeyFile                  string
	ThroughputInterval          time.Duration
	AsyncAcks                   bool
	Migrate                     bool
	StopAfterMigrate            bool
	UseVersionLease             bool
	InstallExtensions           bool
	UpgradeExtensions           bool
	UpgradePrereleaseExtensions bool
}

func ParseFlags(cfg *Config, args []string) (*Config, error) {
	var (
		fs = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

		corsOriginFlag string
		migrateOption  string
	)

	pgclient.ParseFlags(fs, &cfg.PgmodelCfg)
	log.ParseFlags(fs, &cfg.LogCfg)
	api.ParseFlags(fs, &cfg.APICfg)
	limits.ParseFlags(fs, &cfg.LimitsCfg)
	tenancy.ParseFlags(fs, &cfg.TenancyCfg)

	fs.StringVar(&cfg.ConfigFile, "config", "config.yml", "YAML configuration file path for Promscale.")
	fs.StringVar(&cfg.ListenAddr, "web-listen-address", ":9201", "Address to listen on for web endpoints.")
	fs.StringVar(&cfg.ThanosStoreAPIListenAddr, "thanos-store-api-listen-address", "", "Address to listen on for Thanos Store API endpoints.")
	fs.StringVar(&cfg.OTLPGRPCListenAddr, "otlp-grpc-server-listen-address", "", "Address to listen on for OTLP GRPC server.")
	fs.StringVar(&corsOriginFlag, "web-cors-origin", ".*", `Regex for CORS origin. It is fully anchored. Example: 'https?://(domain1|domain2)\.com'`)
	fs.DurationVar(&cfg.ThroughputInterval, "tput-report", time.Second, "Duration interval at which throughput should be reported. Setting duration to `0` will disable reporting throughput, otherwise, an interval with unit must be provided, e.g. `10s` or `3m`.")
	fs.StringVar(&migrateOption, "migrate", "true", "Update the Prometheus SQL schema to the latest version. Valid options are: [true, false, only]. (DEPRECATED) Will be removed in version 0.9.0")
	fs.BoolVar(&cfg.UseVersionLease, "use-schema-version-lease", true, "Use schema version lease to prevent race conditions during migration.")
	fs.BoolVar(&cfg.InstallExtensions, "install-extensions", true, "Install TimescaleDB, Promscale extension.")
	fs.BoolVar(&cfg.UpgradeExtensions, "upgrade-extensions", true, "Upgrades TimescaleDB, Promscale extensions.")
	fs.BoolVar(&cfg.AsyncAcks, "async-acks", false, "Acknowledge asynchronous inserts. If this is true, the inserter will not wait after insertion of metric data in the database. This increases throughput at the cost of a small chance of data loss.")
	fs.BoolVar(&cfg.UpgradePrereleaseExtensions, "upgrade-prerelease-extensions", false, "Upgrades to pre-release TimescaleDB, Promscale extensions.")
	fs.StringVar(&cfg.TLSCertFile, "tls-cert-file", "", "TLS Certificate file used for server authentication, leave blank to disable TLS. NOTE: this option is used for all servers that Promscale runs (web and GRPC).")
	fs.StringVar(&cfg.TLSKeyFile, "tls-key-file", "", "TLS Key file for server authentication, leave blank to disable TLS. NOTE: this option is used for all servers that Promscale runs (web and GRPC).")

	if err := util.ParseEnv("PROMSCALE", fs); err != nil {
		return nil, fmt.Errorf("error parsing env variables: %w", err)
	}

	if err := ff.Parse(fs, args,
		ff.WithConfigFileFlag("config"),
		ff.WithConfigFileParser(ffyaml.Parser),
		ff.WithAllowMissingConfigFile(true),
	); err != nil {
		return nil, fmt.Errorf("configuration error: %w", err)
	}

	// Checking if TLS files are not both set or both empty.
	if (cfg.TLSCertFile != "") != (cfg.TLSKeyFile != "") {
		return nil, fmt.Errorf("both TLS Ceriticate File and TLS Key File need to be provided for a valid TLS configuration")
	}

	corsOriginRegex, err := compileAnchoredRegexString(corsOriginFlag)
	if err != nil {
		return nil, fmt.Errorf("could not compile CORS regex string %v: %w", corsOriginFlag, err)
	}
	cfg.APICfg.AllowedOrigin = corsOriginRegex

	if err := validate(cfg); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	_, tracingEnabled := (cfg.APICfg.EnabledFeatureMap)["tracing"]
	OLTPGRPCListenerConfigured := len(cfg.OTLPGRPCListenAddr) > 0

	if !tracingEnabled && OLTPGRPCListenerConfigured {
		return nil, fmt.Errorf("feature 'tracing' must be enabled (with `-enable-feature tracing`) to configure 'otlp-grpc-server-listen-address'")
	}
	if tracingEnabled && !OLTPGRPCListenerConfigured {
		return nil, fmt.Errorf("'otlp-grpc-server-listen-address' must be configured if 'tracing' enabled")
	}

	cfg.StopAfterMigrate = false
	if strings.EqualFold(migrateOption, "true") {
		cfg.Migrate = true
	} else if strings.EqualFold(migrateOption, "false") {
		cfg.Migrate = false
	} else if strings.EqualFold(migrateOption, "only") {
		cfg.Migrate = true
		cfg.StopAfterMigrate = true
	} else {
		return nil, fmt.Errorf("Invalid option for migrate: %v. Valid options are [true, false, only]", migrateOption)
	}

	if cfg.APICfg.ReadOnly {
		flagset := make(map[string]bool)
		fs.Visit(func(f *flag.Flag) { flagset[f.Name] = true })
		if (flagset["migrate"] && cfg.Migrate) || (flagset["use-schema-version-lease"] && cfg.UseVersionLease) {
			return nil, fmt.Errorf("Migration flags not supported in read-only mode")
		}
		if flagset["install-extensions"] && cfg.InstallExtensions {
			return nil, fmt.Errorf("Cannot install or update TimescaleDB extension in read-only mode")
		}
		cfg.Migrate = false
		cfg.StopAfterMigrate = false
		cfg.UseVersionLease = false
		cfg.InstallExtensions = false
		cfg.UpgradeExtensions = false
	}

	if cfg.APICfg.HighAvailability {
		cfg.PgmodelCfg.UsesHA = true
	}
	return cfg, nil
}

func validate(cfg *Config) error {
	if err := api.Validate(&cfg.APICfg); err != nil {
		return fmt.Errorf("error validating API configuration: %w", err)
	}
	if err := limits.Validate(&cfg.LimitsCfg); err != nil {
		return fmt.Errorf("error validating limits configuration: %w", err)
	}
	if err := pgclient.Validate(&cfg.PgmodelCfg, cfg.LimitsCfg); err != nil {
		return fmt.Errorf("error validating client configuration: %w", err)
	}
	if err := tenancy.Validate(&cfg.TenancyCfg); err != nil {
		return fmt.Errorf("error validating multi-tenancy configuration: %w", err)
	}
	return nil
}
