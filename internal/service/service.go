package service

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"path"
	"slices"
	"sort"
	"time"

	connect "github.com/bufbuild/connect-go"
	"github.com/hashicorp/go-multierror"
	v1 "github.com/tierklinik-dobersberg/apis/gen/go/tkd/orthanc_bridge/v1"
	"github.com/tierklinik-dobersberg/apis/gen/go/tkd/orthanc_bridge/v1/orthanc_bridgev1connect"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/config"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/dicomweb"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/export"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/orthanc"
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

func (svc *Service) ListStudies(ctx context.Context, req *connect.Request[v1.ListStudiesRequest]) (*connect.Response[v1.ListStudiesResponse], error) {
	if svc.DICOMWebClient == nil {
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

	// Apply paggination when set.
	if pg := m.GetPagination(); pg != nil && pg.PageSize > 0 {
		qidoReq.Limit = int(pg.PageSize)
		qidoReq.Offset = int(pg.PageSize * pg.GetPage())
	}

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

	res, err := svc.DICOMWebClient.Query(ctx, qidoReq)
	if err != nil {
		if re, ok := err.(*dicomweb.ResponseError); ok {
			body, _ := io.ReadAll(re.Response.Body)
			slog.Error("failed to query for studies", "error", err, "response", string(body))
		}

		return nil, fmt.Errorf("failed to query for studies: %w", err)
	}

	response := new(v1.ListStudiesResponse)

	for _, r := range res {
		merr := new(multierror.Error)

		study := &v1.Study{
			StudyUid:    parseFirstString(r, dicomweb.StudyInstanceUID, merr),
			Time:        timestamppb.New(parseDateAndTime(r, dicomweb.StudyDate, dicomweb.StudyTime, nil)),
			Modalities:  parseStringList(r, dicomweb.ModalitiesInStudy, nil),
			PatientName: parseSingleName(r, dicomweb.PatientName, nil),
			OwnerName:   parseSingleName(r, dicomweb.ResponsiblePerson, nil),
			Tags:        parseTags(r),
		}

		// bail out if there were errors
		if err := merr.ErrorOrNil(); err != nil {
			slog.Info("failed to get study", "id", study.StudyUid, "error", err)
			continue
		}

		// get series for the study
		series, err := svc.DICOMWebClient.Query(ctx, dicomweb.QIDORequest{
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

			seriesPb := &v1.Series{
				SeriesUid: parseFirstString(s, dicomweb.SeriesInstanceUID, merr),
				Time:      timestamppb.New(parseDateAndTime(s, dicomweb.SeriesDate, dicomweb.SeriesTime, nil)),
				Tags:      parseTags(s),
			}

			// bail out if there were errors
			if err := merr.ErrorOrNil(); err != nil {
				slog.Info("failed to convert series", "id", study.StudyUid, "series", seriesPb.SeriesUid, "error", err)
				continue
			}

			instances, err := svc.DICOMWebClient.Query(ctx, dicomweb.QIDORequest{
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

				ipb := &v1.Instance{
					InstanceUid: parseFirstString(i, dicomweb.SOPInstanceUID, merr),
					Time:        timestamppb.New(parseDateAndTime(i, dicomweb.InstanceCreationDate, dicomweb.InstanceCreationTime, nil)),
					Tags:        parseTags(i),
				}

				if err := merr.ErrorOrNil(); err != nil {
					slog.Error("failed to convert instance", "id", study.StudyUid, "series", seriesPb.SeriesUid, "instance", ipb.InstanceUid, "error", err)
					continue
				}

				seriesPb.Instances = append(seriesPb.Instances, ipb)
			}

			study.Series = append(study.Series, seriesPb)
		}

		response.Studies = append(response.Studies, study)
	}

	// Sort the studies by time
	sort.Sort(
		sort.Reverse(StudyListByTime(response.Studies)),
	)

	if len(res) > 0 && len(response.Studies) == 0 {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to convert response, no studies available"))
	}

	return connect.NewResponse(response), nil
}

func (svc *Service) DownloadStudy(ctx context.Context, req *connect.Request[v1.DownloadStudyRequest]) (*connect.Response[v1.DownloadStudyResponse], error) {
	if svc.OrthancClient == nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("no default orthanc instance configured"))
	}

	renderKinds := make([]orthanc.RenderKind, len(req.Msg.Types))
	for idx, t := range req.Msg.Types {
		var v orthanc.RenderKind

		switch t {
		case v1.DownloadType_DICOM:
			v = orthanc.KindDICOM

		case v1.DownloadType_JPEG:
			v = orthanc.KindJPEG

		case v1.DownloadType_PNG:
			v = orthanc.KindPNG

		case v1.DownloadType_AVI:
			v = orthanc.KindAVI

		default:
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("unsupported or unspecified render kind: %q", t))
		}

		renderKinds[idx] = v
	}

	// sort and compact
	slices.SortFunc(renderKinds, func(a, b orthanc.RenderKind) int {
		return int(b) - int(a)
	})
	renderKinds = slices.Compact(renderKinds)

	if len(renderKinds) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("no valid render kinds specified"))
	}

	ttl := time.Minute * 30
	if req.Msg.TimeToLive != nil {
		ttl = req.Msg.TimeToLive.AsDuration()
	}

	archive, err := svc.Artifacts.Export(ctx, export.ExportOptions{
		TTL:          ttl,
		StudyUID:     req.Msg.StudyUid,
		InstanceUIDs: req.Msg.InstanceUids,
		Kinds:        renderKinds,
	})
	if err != nil {
		return nil, err
	}

	accessUrl, _ := url.Parse(svc.Config.PublicURL)
	accessUrl.Path = path.Join(accessUrl.Path, "download", archive.ID)

	return connect.NewResponse(&v1.DownloadStudyResponse{
		DownloadLink: accessUrl.String(),
		ExpireTime:   timestamppb.New(archive.ExpiresAt),
	}), nil
}
