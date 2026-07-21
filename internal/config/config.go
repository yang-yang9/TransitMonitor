// Package config loads the YAML configuration (stations, alert rules,
// dashboard, db path). Stations carry plaintext credentials in the YAML file
// (operator protects file perms 0600); at-rest encryption applies when creds
// are persisted to the store's credentials table.
package config

import (
	"os"

	"gopkg.in/yaml.v3"

	"transitmonitor/internal/alert"
	"transitmonitor/internal/domain"
)

// File is the top-level config.
type File struct {
	DB        DBConfig         `yaml:"db"`
	Stations  []domain.Station `yaml:"stations"`
	Alerts    AlertsConfig     `yaml:"alerts"`
	Dashboard DashboardConfig  `yaml:"dashboard"`
}

type DBConfig struct {
	Path string `yaml:"path"`
}

type AlertsConfig struct {
	Rules    []alert.Rule `yaml:"rules"`
	DingTalk struct {
		Webhook string `yaml:"webhook"`
		Secret  string `yaml:"secret"`
	} `yaml:"dingtalk"`
	Webhook struct {
		URL string `yaml:"url"`
	} `yaml:"webhook"`
}

type DashboardConfig struct {
	Addr  string `yaml:"addr"`
	Token string `yaml:"token"`
}

// Load reads and parses a config file. Stations default to enabled=true
// (disable by omitting from the file). DB path defaults to transitmonitor.db.
func Load(path string) (*File, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f File
	if err := yaml.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	if f.DB.Path == "" {
		f.DB.Path = "transitmonitor.db"
	}
	if f.Dashboard.Addr == "" {
		f.Dashboard.Addr = "127.0.0.1:7421"
	}
	for i := range f.Stations {
		f.Stations[i].Enabled = true // default enabled; remove from file to disable
	}
	return &f, nil
}
