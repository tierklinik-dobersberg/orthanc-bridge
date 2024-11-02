package service

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"sort"
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

	qidoReq := dicomweb.QIDORequest{
		Type:       dicomweb.Study,
		FilterTags: make(map[string][]string),
		IncludeFields: []string{
			dicomweb.ResponsiblePerson,
			dicomweb.StudyDate,
			dicomweb.StudyTime,
			dicomweb.SeriesDate,
			dicomweb.SeriesTime,
			dicomweb.InstanceCreationDate,
			dicomweb.InstanceCreationTime,
		},
	}

	m := req.Msg

	if dr := m.GetDateRange(); dr != nil {
		from := dr.From.AsTimeInLocation(time.Local)
		to := dr.To.AsTimeInLocation(time.Local)

		qidoReq.FilterTags[dicomweb.StudyDate] = []string{fmt.Sprintf("%s-%s", from.Format("20060102"), to.Format("20060102"))}
	}

	if m.EnableFuzzyMatching {
		qidoReq.FuzzyMatching = true
	}

	if m.Modality != "" {
		qidoReq.FilterTags[dicomweb.ModalitiesInStudy] = []string{m.Modality}
	}

	if m.OwnerName != "" {
		qidoReq.FilterTags[dicomweb.ResponsiblePerson] = []string{m.OwnerName}
	}

	if m.PatientName != "" {
		qidoReq.FilterTags[dicomweb.PatientName] = []string{m.PatientName}
	}

	qidoReq.IncludeFields = append(qidoReq.IncludeFields, m.IncludeTags...)
	sort.Strings(qidoReq.IncludeFields)
	qidoReq.IncludeFields = slices.Compact(qidoReq.IncludeFields)

	for _, values := range m.FilterTags {
		qidoReq.FilterTags[values.Tag] = values.Value
	}

	res, err := svc.Client.Query(ctx, qidoReq)
	if err != nil {
		if re, ok := err.(*dicomweb.ResponseError); ok {
			body, _ := io.ReadAll(re.Response.Body)
			slog.Error("failed to query for studies", "error", err, "response", string(body))
		}

		return nil, fmt.Errorf("failed to query for studies: %w", err)
	}

	response := new(orthanc_bridgev1.ListStudiesResponse)

	for _, r := range res {
		merr := new(multierror.Error)

		study := &orthanc_bridgev1.Study{
			StudyUid:    parseFirstString(r, dicomweb.StudyInstanceUID, merr),
			Time:        timestamppb.New(parseDateAndTime(r, dicomweb.StudyDate, dicomweb.StudyTime, merr)),
			Modalities:  parseStringList(r, dicomweb.ModalitiesInStudy, merr),
			PatientName: parseSingleName(r, dicomweb.PatientName, merr),
			OwnerName:   parseSingleName(r, dicomweb.ResponsiblePerson, merr),
			Tags:        parseTags(r),
		}

		// bail out if there were errors
		if err := merr.ErrorOrNil(); err != nil {
			slog.Info("failed to get study", "id", study.StudyUid, "error", err)
			continue
		}

		// get series for the study
		series, err := svc.Client.Query(ctx, dicomweb.QIDORequest{
			Type:             dicomweb.Series,
			StudyInstanceUID: study.StudyUid,
			IncludeFields:    qidoReq.IncludeFields,
		})
		if err != nil {
			slog.Info("failed to fetch study series", "id", study.StudyUid, "error", err)
			continue
		}

		for _, s := range series {
			merr := new(multierror.Error)

			seriesPb := &orthanc_bridgev1.Series{
				SeriesUid: parseFirstString(s, dicomweb.SeriesInstanceUID, merr),
				Time:      timestamppb.New(parseDateAndTime(s, dicomweb.SeriesDate, dicomweb.SeriesTime, nil)),
				Tags:      parseTags(s),
			}

			// bail out if there were errors
			if err := merr.ErrorOrNil(); err != nil {
				slog.Info("failed to convert series", "id", study.StudyUid, "series", seriesPb.SeriesUid, "error", err)
				continue
			}

			instances, err := svc.Client.Query(ctx, dicomweb.QIDORequest{
				Type:              dicomweb.Instance,
				StudyInstanceUID:  study.StudyUid,
				SeriesInstanceUID: seriesPb.SeriesUid,
				IncludeFields:     qidoReq.IncludeFields,
			})
			if err != nil {
				slog.Info("failed to fetch instances", "id", study.StudyUid, "series", seriesPb.SeriesUid, "error", err)
				continue
			}

			for _, i := range instances {
				merr := new(multierror.Error)

				ipb := &orthanc_bridgev1.Instance{
					InstanceUid: parseFirstString(i, dicomweb.SOPInstanceUID, merr),
					Time:        timestamppb.New(parseDateAndTime(i, dicomweb.InstanceCreationDate, dicomweb.InstanceCreationTime, nil)),
					Tags:        parseTags(i),
				}

				if err := merr.ErrorOrNil(); err != nil {
					slog.Info("failed to convert instance", "id", study.StudyUid, "series", seriesPb.SeriesUid, "instance", ipb.InstanceUid, "error", err)
				}

				seriesPb.Instances = append(seriesPb.Instances, ipb)
			}

			study.Series = append(study.Series, seriesPb)
		}

		response.Studies = append(response.Studies, study)
	}

	if len(res) > 0 && len(response.Studies) == 0 {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to convert response, no studies available"))
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
			Name:                dicomweb.TagToName[key],
		})
	}

	return result
}
