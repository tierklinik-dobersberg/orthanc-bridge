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
	"sync"
	"time"

	connect "github.com/bufbuild/connect-go"
	"github.com/hashicorp/go-multierror"
	eventsv1 "github.com/tierklinik-dobersberg/apis/gen/go/tkd/events/v1"
	orthanc_bridgev1 "github.com/tierklinik-dobersberg/apis/gen/go/tkd/orthanc_bridge/v1"
	v1 "github.com/tierklinik-dobersberg/apis/gen/go/tkd/orthanc_bridge/v1"
	"github.com/tierklinik-dobersberg/apis/gen/go/tkd/orthanc_bridge/v1/orthanc_bridgev1connect"
	"github.com/tierklinik-dobersberg/apis/pkg/auth"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/config"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/dicomweb"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/export"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/orthanc"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/repo"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Service struct {
	orthanc_bridgev1connect.UnimplementedOrthancBridgeHandler

	*config.Providers

	recentStudiesLock sync.RWMutex
	recentStudies     []*orthanc_bridgev1.Study
}

func (svc *Service) watchRecentStudies(ctx context.Context) {
	ticker := time.NewTicker(time.Minute * 5)
	var events <-chan *eventsv1.Event

	if svc.Providers.EventClient != nil {
		var err error

		events, err = svc.Providers.EventClient.SubscribeMessage(ctx, &orthanc_bridgev1.InstanceReceivedEvent{})
		if err != nil {
			slog.Error("failed to subscribe to InstanceReceivedEvent", "error", err)
		}
	}

	go func() {
		defer ticker.Stop()

		for {

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
					dicomweb.PatientID,
					dicomweb.PatientName,
				},
			}

			now := time.Now()
			start := now.Add(-7 * 24 * time.Hour)

			qidoReq.FilterTags[dicomweb.StudyDate] = []string{fmt.Sprintf("%s-%s", start.Format("20060102"), now.Format("20060102"))}

			studies, err := svc.fetchStudies(ctx, qidoReq)
			if err != nil {
				slog.Error("failed to fetch recent studies", "error", err)
			} else {
				slog.Info("successfully fetched recent studies", "count", len(studies))

				svc.recentStudiesLock.Lock()
				svc.recentStudies = studies
				svc.recentStudiesLock.Unlock()
			}

			select {
			case <-ticker.C:
			case <-events:
			}
		}

	}()
}

func New(ctx context.Context, p *config.Providers) *Service {
	svc := &Service{
		Providers: p,
	}

	svc.watchRecentStudies(ctx)

	return svc
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

	m := req.Msg

	if dr := m.GetDateRange(); dr != nil && dr.From != nil && dr.To != nil {
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

	if m.PatientId != "" {
		qidoReq.FilterTags[dicomweb.PatientID] = []string{m.PatientId}
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

	studies, err := svc.fetchStudies(ctx, qidoReq)
	if err != nil {
		return nil, err
	}

	response := &orthanc_bridgev1.ListStudiesResponse{
		Studies:    studies,
		TotalCount: int64(len(studies)),
	}

	if len(res) > 0 && len(response.Studies) == 0 {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to convert response, no studies available"))
	}

	// finnally, apply pagination if requested
	if p := m.GetPagination(); p != nil && p.PageSize > 0 {
		page := p.GetPage()
		response.Studies = response.Studies[page*p.PageSize : (page+1)*p.PageSize]
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

func (svc *Service) ShareStudy(ctx context.Context, req *connect.Request[v1.ShareStudyRequest]) (*connect.Response[v1.ShareStudyResponse], error) {
	token := repo.ShareTokenPrefix + export.GetRandomString(48)

	ttl := time.Hour * 24 * 30

	if req.Msg.ValidDuration.IsValid() {
		ttl = req.Msg.ValidDuration.AsDuration()
	}

	share := repo.StudyShare{
		Token:        token,
		CreatedAt:    time.Now(),
		Creator:      auth.From(ctx).ID,
		ExpiresAt:    time.Now().Add(ttl),
		StudyUID:     req.Msg.StudyUid,
		InstanceUIDs: req.Msg.InstanceUids,
	}

	if err := svc.Repo.CreateStudyShare(ctx, share); err != nil {
		return nil, err
	}

	viewerUrl := fmt.Sprintf("%s/viewer/?StudyInstanceUIDs=%s&token=%s", svc.Config.PublicURL, req.Msg.StudyUid, token)

	if len(share.InstanceUIDs) > 0 {
		viewerUrl += "&initialSopInstanceUid=" + share.InstanceUIDs[0]
	}

	return connect.NewResponse(&v1.ShareStudyResponse{
		Token:     token,
		ViewerUrl: viewerUrl,
	}), nil
}

func (svc *Service) fetchStudies(ctx context.Context, qidoReq dicomweb.QIDORequest) ([]*orthanc_bridgev1.Study, error) {
	res, err := svc.DICOMWebClient.Query(ctx, qidoReq)
	if err != nil {
		if re, ok := err.(*dicomweb.ResponseError); ok {
			body, _ := io.ReadAll(re.Response.Body)
			slog.Error("failed to query for studies", "error", err, "response", string(body))
		}

		return nil, fmt.Errorf("failed to query for studies: %w", err)
	}

	var response []*orthanc_bridgev1.Study

	for _, r := range res {
		merr := new(multierror.Error)

		study := &v1.Study{
			StudyUid:    parseFirstString(r, dicomweb.StudyInstanceUID, merr),
			Time:        timestamppb.New(parseDateAndTime(r, dicomweb.StudyDate, dicomweb.StudyTime, nil)),
			Modalities:  parseStringList(r, dicomweb.ModalitiesInStudy, nil),
			PatientName: parseSingleName(r, dicomweb.PatientName, nil),
			OwnerName:   parseSingleName(r, dicomweb.ResponsiblePerson, nil),
			PatientId:   parseSingleName(r, dicomweb.PatientID, nil),
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

		response = append(response, study)
	}

	// Sort the studies by time
	sort.Sort(
		sort.Reverse(StudyListByTime(response)),
	)

	return response, nil
}

func (svc *Service) ListRecentStudies(ctx context.Context, req *connect.Request[emptypb.Empty]) (*connect.Response[orthanc_bridgev1.ListStudiesResponse], error) {
	svc.recentStudiesLock.RLock()
	defer svc.recentStudiesLock.RUnlock()

	response := &orthanc_bridgev1.ListStudiesResponse{
		Studies:    svc.recentStudies,
		TotalCount: int64(len(svc.recentStudies)),
	}

	return connect.NewResponse(response), nil
}
