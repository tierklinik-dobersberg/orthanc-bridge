package service

import (
	"github.com/tierklinik-dobersberg/apis/gen/go/tkd/orthanc_bridge/v1/orthanc_bridgev1connect"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/config"
)

type Service struct {
	orthanc_bridgev1connect.UnimplementedOrthancBridgeHandler

	*config.Providers
}

func New(p *config.Providers) *Service {
	return &Service{
		Providers: p,
	}
}
