// Package config loads credential-vault settings from YAML, environment, and flags.
package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	VaultDir          string   `mapstructure:"vault_dir"`
	ScanTargets       []string `mapstructure:"scan_targets"`
	DashboardInterval int      `mapstructure:"dashboard_interval_seconds"`
}

func Load(path string) (Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, err
	}
	v := viper.New()
	v.SetDefault("vault_dir", filepath.Join(home, ".credential-vault-go"))
	v.SetDefault("scan_targets", []string{"."})
	v.SetDefault("dashboard_interval_seconds", 2)
	v.SetEnvPrefix("CREDENTIAL_VAULT")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	if path != "" {
		v.SetConfigFile(path)
	} else {
		v.SetConfigName("config")
		v.SetConfigType("yaml")
		v.AddConfigPath(filepath.Join(home, ".config", "vaultctl"))
	}
	if err = v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok && path != "" {
			return Config{}, err
		}
	}
	var c Config
	return c, v.Unmarshal(&c)
}
