package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	connect "github.com/bufbuild/connect-go"
	"github.com/hashicorp/go-multierror"
	orthanc_bridgev1 "github.com/tierklinik-dobersberg/apis/gen/go/tkd/orthanc_bridge/v1"
	"github.com/tierklinik-dobersberg/apis/gen/go/tkd/orthanc_bridge/v1/orthanc_bridgev1connect"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/config"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/dicomweb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
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

func (svc *Service) ListStudies(ctx context.Context, req *connect.Request[orthanc_bridgev1.ListStudiesRequest]) (*connect.Response[orthanc_bridgev1.ListStudiesResponse], error) {
	if svc.Client == nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("no dicomweb client configured"))
	}

	r := dicomweb.QIDORequest{
		Type:       dicomweb.Study,
		FilterTags: make(map[string][]string),
	}

	m := req.Msg

	if dr := m.GetDateRange(); dr != nil {
		from := dr.From.AsTimeInLocation(time.Local)
		to := dr.To.AsTimeInLocation(time.Local)

		r.FilterTags[dicomweb.StudyDate] = []string{fmt.Sprintf("%s-%s", from.Format("20060102"), to.Format("20060102"))}
	}

	if m.EnableFuzzyMatching {
		r.FuzzyMatching = true
	}

	if m.Modality != "" {
		r.FilterTags[dicomweb.ModalitiesInStudy] = []string{m.Modality}
	}

	if m.OwnerName != "" {
		r.FilterTags[dicomweb.ResponsiblePerson] = []string{m.OwnerName}
	}

	if m.PatientName != "" {
		r.FilterTags[dicomweb.PatientName] = []string{m.PatientName}
	}

	r.IncludeFields = m.IncludeTags

	for _, values := range m.FilterTags {
		r.FilterTags[values.Tag] = values.Value
	}

	res, err := svc.Client.Query(ctx, r)
	if err != nil {
		return nil, err
	}

	response := new(orthanc_bridgev1.ListStudiesResponse)

	merr := new(multierror.Error)
	for _, r := range res {
		study := &orthanc_bridgev1.Study{
			StudyUid:    parseFirstString(r, dicomweb.StudyInstanceUID, merr),
			Time:        timestamppb.New(parseDateAndTime(r, dicomweb.StudyDate, dicomweb.StudyTime, merr)),
			Modalities:  parseStringList(r, dicomweb.ModalitiesInStudy, merr),
			PatientName: parseFirstString(r, dicomweb.PatientName, merr),
			OwnerName:   parseFirstString(r, dicomweb.ResponsiblePerson, merr),
			Tags:        parseTags(r),
		}

		// get series for the study
		series, err := svc.Client.Query(ctx, dicomweb.QIDORequest{
			Type:             dicomweb.Series,
			StudyInstanceUID: study.StudyUid,
			IncludeFields:    m.IncludeTags,
		})
		if err != nil {
			merr.Errors = append(merr.Errors, fmt.Errorf("failed to fetch series: %w", err))
			continue
		}

		for _, s := range series {
			seriesPb := &orthanc_bridgev1.Series{
				SeriesUid: parseFirstString(s, dicomweb.SeriesInstanceUID, merr),
				Time:      timestamppb.New(parseDateAndTime(s, dicomweb.SeriesDate, dicomweb.SeriesTime, merr)),
				Tags:      parseTags(s),
			}

			instances, err := svc.Client.Query(ctx, dicomweb.QIDORequest{
				Type:              dicomweb.Instance,
				StudyInstanceUID:  study.StudyUid,
				SeriesInstanceUID: seriesPb.SeriesUid,
				IncludeFields:     m.IncludeTags,
			})
			if err != nil {
				merr.Errors = append(merr.Errors, fmt.Errorf("failed to fetch series: %w", err))
				continue
			}

			for _, i := range instances {
				ipb := &orthanc_bridgev1.Instance{
					InstanceUid: parseFirstString(i, dicomweb.SOPInstanceUID, merr),
					Time:        timestamppb.New(parseDateAndTime(i, dicomweb.InstanceCreationDate, dicomweb.InstanceCreationTime, merr)),
					Tags:        parseTags(i),
				}

				seriesPb.Instances = append(seriesPb.Instances, ipb)
			}

			study.Series = append(study.Series, seriesPb)
		}

		response.Studies = append(response.Studies, study)
	}

	if err := merr.ErrorOrNil(); err != nil {
		return nil, err
	}

	return connect.NewResponse(response), nil
}

func parseTags(r dicomweb.QIDOResponse) []*orthanc_bridgev1.DICOMTag {
	var result []*orthanc_bridgev1.DICOMTag

	for key, t := range r {
		var values []*structpb.Value

		for _, v := range t.Value {
			vpb, err := structpb.NewValue(v)
			if err != nil {
				slog.Error("failed to convert value for dicom tag", "tag", key, "error", err)
				continue
			}

			values = append(values, vpb)
		}

		result = append(result, &orthanc_bridgev1.DICOMTag{
			Tag:                 key,
			ValueRepresentation: t.VR,
			Value:               values,
		})
	}

	return result
}
