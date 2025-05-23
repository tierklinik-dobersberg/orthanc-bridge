package config

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"path"

	"github.com/tierklinik-dobersberg/apis/gen/go/tkd/idm/v1/idmv1connect"
	"github.com/tierklinik-dobersberg/apis/pkg/discovery/consuldiscover"
	"github.com/tierklinik-dobersberg/apis/pkg/events"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/dicomweb"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/export"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/orthanc"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/repo"
)

type Providers struct {
	Users       idmv1connect.UserServiceClient
	Roles       idmv1connect.RoleServiceClient
	EventClient *events.Client

	DICOMWebClient *dicomweb.Client
	OrthancClient  *orthanc.Client

	Repo *repo.Repo

	Artifacts *export.Registry

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

	var eventClient *events.Client
	disc, err := consuldiscover.NewFromEnv()

	if err != nil {
		slog.Error("failed to get service catalog", "error", err)
	} else {
		eventClient = events.NewClient(events.DiscoveredInsecureClient(disc))

		if err := eventClient.Start(ctx); err != nil {
			slog.Error("failed to start event service client", "error", err)
		}
	}

	webClient := dicomweb.NewClient((&url.URL{
		Scheme: u.Scheme,
		Host:   u.Host,
		Path:   path.Join(u.Path, instance.DicomWeb),
		User:   u.User,
	}).String())

	orthancClient, err := orthanc.NewClient(u.String())
	if err != nil {
		return nil, fmt.Errorf("failed to create orthanc client: %w", err)
	}

	storage, err := repo.New(ctx, cfg.Mongo.URL, cfg.Mongo.Database)
	if err != nil {
		return nil, fmt.Errorf("failed to create repository: %w", err)
	}

	p := &Providers{
		Users:          idmv1connect.NewUserServiceClient(httpClient, cfg.IdmURL),
		Roles:          idmv1connect.NewRoleServiceClient(httpClient, cfg.IdmURL),
		DICOMWebClient: webClient,
		OrthancClient:  orthancClient,
		Config:         cfg,
		Artifacts:      export.NewRegistry(ctx, orthancClient, storage),
		Repo:           storage,
		EventClient:    eventClient,
	}

	return p, nil
}
