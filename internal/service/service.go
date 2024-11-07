package service

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	connect "github.com/bufbuild/connect-go"
	"github.com/hashicorp/go-multierror"
	v1 "github.com/tierklinik-dobersberg/apis/gen/go/tkd/orthanc_bridge/v1"
	"github.com/tierklinik-dobersberg/apis/gen/go/tkd/orthanc_bridge/v1/orthanc_bridgev1connect"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/config"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/dicomweb"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/export"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/orthanc"
	"golang.org/x/exp/rand"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type downloadEntry struct {
	created     time.Time
	path        string
	patientName string
	ownerName   string
}

type Service struct {
	orthanc_bridgev1connect.UnimplementedOrthancBridgeHandler

	*config.Providers

	rw        sync.RWMutex
	downloads map[string]downloadEntry
}

func New(p *config.Providers) *Service {
	return &Service{
		Providers: p,
		downloads: make(map[string]downloadEntry),
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

	m := req.Msg

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

	// first, read the study metadata
	studies, err := svc.OrthancClient.FindStudy(ctx, orthanc.ByStudyUID(req.Msg.StudyUid))
	if err != nil {
		return nil, fmt.Errorf("failed to find study: %w", err)
	}

	switch {
	case len(studies) == 0:
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("study with uid %q not found", req.Msg.StudyUid))

	case len(studies) > 1:
		return nil, connect.NewError(connect.CodeUnknown, fmt.Errorf("to many results"))
	}

	study := studies[0]

	archiveId := getRandomString(32)
	patientName, _ := study.PatientMainDicomTags["PatientName"].(string)
	ownerName, _ := study.PatientMainDicomTags["ResponsiblePerson"].(string)

	instances, err := svc.OrthancClient.FindInstances(ctx, orthanc.ByStudyUID(req.Msg.StudyUid))
	if err != nil {
		return nil, fmt.Errorf("failed to contact orthanc API: %w", err)
	}

	slog.Info("got instances for study", "studyUid", req.Msg.StudyUid, "count", len(instances))

	var resourcePath string
	if len(req.Msg.InstanceUids) != 1 {
		var err error
		resourcePath, err = export.CreateStudyArchive(ctx, svc.OrthancClient, req.Msg.StudyUid, instances, req.Msg.InstanceUids, []orthanc.RenderKind{orthanc.KindPNG})
		if err != nil {
			return nil, err
		}
	} else {
		return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("retrieving a single instance is not yet supported"))
	}

	svc.rw.Lock()
	defer svc.rw.Unlock()
	svc.downloads[archiveId] = downloadEntry{
		created:     time.Now(),
		path:        resourcePath,
		patientName: patientName,
		ownerName:   ownerName,
	}

	accessUrl, _ := url.Parse(svc.Config.PublicURL)
	accessUrl.Path = path.Join(accessUrl.Path, "download", archiveId)

	return connect.NewResponse(&v1.DownloadStudyResponse{
		DownloadLink: accessUrl.String(),
	}), nil
}

func (svc *Service) DownloadHandler(w http.ResponseWriter, r *http.Request) {
	archiveId := r.PathValue("id")

	svc.rw.RLock()
	entry, ok := svc.downloads[archiveId]
	svc.rw.RUnlock()

	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	filename := filepath.Base(entry.path)

	replace := func(s string) string {
		s = strings.ReplaceAll(s, "ERROR", "")
		s = strings.ReplaceAll(s, ",", "-")
		s = strings.ReplaceAll(s, " ", "-")

		return s
	}

	if entry.ownerName != "" || entry.patientName != "" {
		parts := []string{
			replace(entry.ownerName),
			replace(entry.patientName),
		}

		filename = strings.Join(parts, "-") + filepath.Ext(entry.path)
	}

	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")

	http.ServeFile(w, r, entry.path)
}

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")

func getRandomString(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return string(b)
}
