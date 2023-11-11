package config

import (
	"context"
	"net/http"

	"github.com/tierklinik-dobersberg/apis/gen/go/tkd/idm/v1/idmv1connect"
)

type Providers struct {
	Users idmv1connect.UserServiceClient
	Roles idmv1connect.RoleServiceClient

	Config Config
}

func NewProviders(ctx context.Context, cfg Config) (*Providers, error) {
	httpClient := http.DefaultClient

	p := &Providers{
		Users:  idmv1connect.NewUserServiceClient(httpClient, cfg.IdmURL),
		Roles:  idmv1connect.NewRoleServiceClient(httpClient, cfg.IdmURL),
		Config: cfg,
	}

	return p, nil
}
