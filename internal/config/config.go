package config

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/ghodss/yaml"
	"github.com/sethvargo/go-envconfig"
)

type OrthancInstance struct {
	Address            string `json:"address"`
	Username           string `json:"user"`
	Password           string `json:"password"`
	InsecureSkipVerify bool   `json:"insecureSkipVerify"`
	RewriteHost        string `json:"rewriteHost"`
	DicomWeb           string `json:"dicomWebPath"` // defaults to /dicom-web/
}

type Config struct {
	IdmURL              string                     `env:"IDM_URL" json:"idmURL"`
	AllowedOrigins      []string                   `env:"ALLOWED_ORIGINS" json:"allowedOrigins"`
	PublicListenAddress string                     `env:"PUBLIC_LISTEN" json:"publicListen"`
	AdminListenAddress  string                     `env:"ADMIN_LISTEN" json:"adminListen"`
	PublicURL           string                     `env:"PUBLIC_URL" json:"publicUrl"`
	Instances           map[string]OrthancInstance `json:"instances"`
	DefaultInstance     string                     `json:"defaultInstance"`
	Mongo               struct {
		URL      string `json:"url"`
		Database string `json:"database"`
	} `json:"mongodb"`
}

func LoadConfig(ctx context.Context, path string) (*Config, error) {
	var cfg Config

	if path != "" {
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("failed to read config file at path %q: %w", path, err)
		}

		switch filepath.Ext(path) {
		case ".yaml", ".yml":
			content, err = yaml.YAMLToJSON(content)
			if err != nil {
				return nil, fmt.Errorf("failed to convert YAML to JSON: %w", err)
			}

			fallthrough
		case ".json":
			dec := json.NewDecoder(bytes.NewReader(content))
			dec.DisallowUnknownFields()

			if err := dec.Decode(&cfg); err != nil {
				return nil, fmt.Errorf("failed to decode JSON: %w", err)
			}
		}
	}

	if err := envconfig.Process(ctx, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse configuration from environment: %w", err)
	}

	if _, err := url.Parse(cfg.PublicURL); err != nil {
		return nil, fmt.Errorf("failed to parse publicUrl: %w", err)
	}

	if cfg.PublicListenAddress == "" {
		cfg.PublicListenAddress = ":8080"
	}

	if len(cfg.AllowedOrigins) == 0 {
		cfg.AllowedOrigins = []string{"*"}
	}

	if cfg.IdmURL == "" {
		return nil, fmt.Errorf("missing idmUrl config setting")
	}

	if _, err := url.Parse(cfg.IdmURL); err != nil {
		return nil, fmt.Errorf("invalid IDM_URL: %w", err)
	}

	if cfg.Mongo.URL == "" || cfg.Mongo.Database == "" {
		return nil, fmt.Errorf("invalid mongodb configuration")
	}

	return &cfg, nil
}
