package service

import (
	"context"
	"fmt"

	"github.com/bufbuild/connect-go"
	"github.com/hashicorp/go-multierror"
	orthanc_bridgev1 "github.com/tierklinik-dobersberg/apis/gen/go/tkd/orthanc_bridge/v1"
	"github.com/tierklinik-dobersberg/apis/pkg/log"
)

func (svc *Service) GetWorklistEntries(ctx context.Context, req *connect.Request[orthanc_bridgev1.GetWorklistEntriesRequest]) (*connect.Response[orthanc_bridgev1.GetWorklistEntriesResponse], error) {
	if svc.Worklist == nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("worklist not configured"))
	}

	entries, err := svc.Providers.Worklist.ListEntries()
	if err != nil {
		return nil, err
	}

	res := &orthanc_bridgev1.GetWorklistEntriesResponse{
		Entries: make([]*orthanc_bridgev1.WorklistEntry, 0, len(entries)),
	}

	var merr multierror.Error
	for _, e := range entries {
		pb, err := e.ToProto()
		if err != nil {
			merr.Errors = append(merr.Errors, fmt.Errorf("%s: %w", e.Path, err))
			continue
		}

		res.Entries = append(res.Entries, pb)
	}

	if len(res.Entries) == 0 && merr.ErrorOrNil() != nil {
		return nil, merr.ErrorOrNil()
	} else if err := merr.ErrorOrNil(); err != nil {
		log.L(ctx).Error("failed to convert some worklist entries", "error", err)
	}

	return connect.NewResponse(res), nil
}
