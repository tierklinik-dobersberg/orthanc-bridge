package config

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/tierklinik-dobersberg/apis/gen/go/tkd/idm/v1/idmv1connect"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/dicomweb"
)

type Providers struct {
	Users idmv1connect.UserServiceClient
	Roles idmv1connect.RoleServiceClient

	Client *dicomweb.Client

	Config Config
}

func NewProviders(ctx context.Context, cfg Config) (*Providers, error) {
	httpClient := http.DefaultClient

	var instance OrthancInstance
	if cfg.DefaultInstance != "" {
		var ok bool
		instance, ok = cfg.Instances[cfg.DefaultInstance]
		if !ok {
			return nil, fmt.Errorf("not configuration for default client %q found", cfg.DefaultInstance)
		}
	}

	u, err := url.Parse(instance.Address)
	if err != nil {
		return nil, fmt.Errorf("failed to parse instance address: %w", err)
	}

	if instance.Username != "" {
		u.User = url.UserPassword(instance.Username, instance.Password)
	}

	defaultClient := dicomweb.NewClient(u.String())

	p := &Providers{
		Users:  idmv1connect.NewUserServiceClient(httpClient, cfg.IdmURL),
		Roles:  idmv1connect.NewRoleServiceClient(httpClient, cfg.IdmURL),
		Client: defaultClient,
		Config: cfg,
	}

	return p, nil
}
