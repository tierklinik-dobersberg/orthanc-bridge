package service

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"time"

	connect "github.com/bufbuild/connect-go"
	"github.com/hashicorp/go-multierror"
	v1 "github.com/tierklinik-dobersberg/apis/gen/go/tkd/orthanc_bridge/v1"
	"github.com/tierklinik-dobersberg/apis/gen/go/tkd/orthanc_bridge/v1/orthanc_bridgev1connect"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/config"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/dicomweb"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/orthanc"
	"golang.org/x/exp/rand"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type downloadEntry struct {
	created time.Time
	path    string
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

	instances, err := svc.OrthancClient.FindInstances(ctx, orthanc.ByStudyUID(req.Msg.StudyUid))
	if err != nil {
		return nil, fmt.Errorf("failed to contact orthanc API: %w", err)
	}

	slog.Info("got instances for study", "studyUid", req.Msg.StudyUid, "count", len(instances))

	// Gather all instance IDS that we want to download.
	filtered := make(map[string]string, len(instances))
	for _, instance := range instances {
		sopInstanceUid, ok := instance.MainDicomTags[dicomweb.SOPInstanceUID].(string)
		if !ok {
			slog.Error("invalid orthanc response, SOPInstanceUID is expected to be a string")
			continue
		}

		if len(req.Msg.InstanceUids) == 0 || slices.Contains(req.Msg.InstanceUids, sopInstanceUid) {
			slog.Info("marking DICOM instance for download", "studyUid", req.Msg.StudyUid, "sopInstanceUid", sopInstanceUid, "id", instance.ID)
			filtered[instance.ID] = sopInstanceUid
		}
	}

	// ensure there are actual instances to download
	if len(filtered) == 0 {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("no instances to download"))
	}

	// create a temporary directory and download all files into it
	dir, err := os.MkdirTemp("", "archive-"+req.Msg.StudyUid+"-raw-")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary directory: %w", err)
	}
	// make sure we clean up afterwards
	defer os.RemoveAll(dir)

	// download each instance to the temporary directory
	// TODO(ppacher): instead of reading the images to RAM and then writting
	// 				  to the file consider streaming the response directly to the FS
	for id, sopInstanceUID := range filtered {
		slog.Info("downloading DICOM instance", "id", id)

		// TODO(ppacher): add support for the different download/render types
		blob, err := svc.Providers.OrthancClient.GetRenderedInstance(ctx, id, orthanc.KindPNG)
		if err != nil {
			return nil, fmt.Errorf("failed to download instance %s (%s): %w", id, sopInstanceUID, err)
		}

		dest := filepath.Join(dir, sopInstanceUID+".png")
		if err := os.WriteFile(dest, blob, 0o600); err != nil {
			return nil, fmt.Errorf("failed to write instance image/file to dist: %w", err)
		}

		slog.Info("succesfully downloaded instance file", "name", dest, "id", id, "sopInstanceUID", sopInstanceUID, "studyUid", req.Msg.StudyUid, "size", len(blob))
	}

	// Create the archive file and a zip writer
	archiveFile, err := os.CreateTemp("", "archive-"+req.Msg.StudyUid+"-*.zip")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary archive file: %w", err)
	}
	defer archiveFile.Close()
	archive := zip.NewWriter(archiveFile)

	if err := archive.AddFS(os.DirFS(dir)); err != nil {
		defer os.Remove(archiveFile.Name())

		return nil, fmt.Errorf("failed to create archive: %w", err)
	}

	if err := archive.Close(); err != nil {
		defer os.Remove(archiveFile.Name())

		return nil, fmt.Errorf("failed to finish archive: %w", err)
	}

	archiveId := getRandomString(32)

	svc.rw.Lock()
	defer svc.rw.Unlock()
	svc.downloads[archiveId] = downloadEntry{
		created: time.Now(),
		path:    archiveFile.Name(),
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

	w.Header().Set("Content-Disposition", "attachment; filename=\""+filepath.Base(entry.path)+"\"")

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
