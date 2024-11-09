package export

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bufbuild/connect-go"
	"github.com/tierklinik-dobersberg/apis/pkg/auth"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/orthanc"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/repo"
	"golang.org/x/exp/rand"
)

type Storage interface {
	AddArtifact(context.Context, repo.Artifact) error
	FindArtifact(context.Context, string) (*repo.Artifact, error)
	FindCleanupCandidates(context.Context, time.Time) ([]repo.Artifact, error)
	DeleteArtifacts(context.Context, []string) error
}

type Registry struct {
	repo Storage

	cli *orthanc.Client

	wg sync.WaitGroup
}

func NewRegistry(ctx context.Context, cli *orthanc.Client, repo Storage) *Registry {
	reg := &Registry{
		repo: repo,
		cli:  cli,
	}

	reg.start(ctx)

	return reg
}

func (reg *Registry) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	archiveId := r.PathValue("id")

	archive, err := reg.repo.FindArtifact(r.Context(), archiveId)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Disposition", "attachment; filename=\""+archive.DownloadName+"\"")

	http.ServeFile(w, r, archive.Filepath)
}

func (reg *Registry) start(ctx context.Context) {
	reg.wg.Add(1)
	go func() {
		defer reg.wg.Done()

		ticker := time.NewTicker(time.Minute * 10)

		for {
			candidates, err := reg.repo.FindCleanupCandidates(ctx, time.Now())
			if err == nil {
				ids := make([]string, 0, len(candidates))

				// remove the actual artifacts from the disk
				for _, c := range candidates {
					err := os.Remove(c.Filepath)
					if err == nil || errors.Is(err, os.ErrNotExist) {
						ids = append(ids, c.ID)
					} else {
						slog.Error("failed to delete artifact", "id", c.ID, "error", err, "path", c.Filepath)
					}
				}

				// finally, remove the artifact entries from the repo
				if err := reg.repo.DeleteArtifacts(ctx, ids); err != nil {
					slog.Error("failed to remove artifacts from repository", "error", err)
				}
			} else {
				slog.Error("failed to find artifact cleanup candidates", "error", err)
			}

			select {
			case <-ctx.Done():
				return

			case <-ticker.C:
			}
		}
	}()
}

func (reg *Registry) Wait() {
	reg.wg.Wait()
}

type ExportOptions struct {
	TTL          time.Duration
	StudyUID     string
	InstanceUIDs []string
	Kinds        []orthanc.RenderKind
}

type studyAndInstances struct {
	studyUID          string
	patientName       string
	responsiblePerson string
	instances         []orthanc.FindInstancesResponse
}

func (reg *Registry) Export(ctx context.Context, options ExportOptions) (repo.Artifact, error) {
	// TODO(ppacher): calculate hash and check if the artifact already exists, if, just update the TTL

	res, err := reg.fetchStudyAndInstances(ctx, options.StudyUID, options.InstanceUIDs)
	if err != nil {
		return repo.Artifact{}, fmt.Errorf("failed to fetch study instances: %w", err)
	}

	if len(res.instances) == 0 {
		return repo.Artifact{}, fmt.Errorf("instance not found")
	}

	needsArchive := len(options.InstanceUIDs) != 1 || len(options.Kinds) != 1
	if needsArchive {
		return reg.exportArchive(ctx, options.TTL, res, options.Kinds)
	}

	return reg.exportSingle(ctx, options.TTL, res, options.Kinds[0])
}

func (reg *Registry) fetchStudyAndInstances(ctx context.Context, studyUid string, filterInstanceUids []string) (*studyAndInstances, error) {
	// first, read the study metadata
	studies, err := reg.cli.FindStudy(ctx, orthanc.ByStudyUID(studyUid))
	if err != nil {
		return nil, fmt.Errorf("failed to find study: %w", err)
	}

	switch {
	case len(studies) == 0:
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("study with uid %q not found", studyUid))

	case len(studies) > 1:
		return nil, connect.NewError(connect.CodeUnknown, fmt.Errorf("to many results"))
	}

	study := studies[0]

	patientName, _ := study.PatientMainDicomTags["PatientName"].(string)
	ownerName, _ := study.PatientMainDicomTags["ResponsiblePerson"].(string)

	instances, err := reg.cli.FindInstances(ctx, orthanc.ByStudyUID(studyUid))
	if err != nil {
		return nil, fmt.Errorf("failed to contact orthanc API: %w", err)
	}

	filteredInstances := make([]orthanc.FindInstancesResponse, 0, len(filterInstanceUids))

	for _, instance := range instances {
		sopInstanceUid, ok := instance.MainDicomTags["SOPInstanceUID"].(string)
		if !ok {
			slog.Error("invalid orthanc response, SOPInstanceUID is expected to be a string")
			continue
		}

		if len(filterInstanceUids) == 0 || slices.Contains(filterInstanceUids, sopInstanceUid) {
			slog.Info("marking DICOM instance for download", "sopInstanceUid", sopInstanceUid, "id", instance.ID, "study-uid", studyUid)
			filteredInstances = append(filteredInstances, instance)
		}
	}

	// ensure there are actual instances to download
	if len(filteredInstances) == 0 {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("no instances to download"))
	}

	return &studyAndInstances{
		studyUID:          studyUid,
		patientName:       patientName,
		responsiblePerson: ownerName,
		instances:         filteredInstances,
	}, nil
}

func (reg *Registry) exportArchive(ctx context.Context, ttl time.Duration, res *studyAndInstances, renderKinds []orthanc.RenderKind) (repo.Artifact, error) {
	path, err := createStudyArchive(ctx, reg.cli, res.studyUID, res.instances, renderKinds)
	if err != nil {
		return repo.Artifact{}, err
	}

	return reg.storeArtifact(ctx, path, ttl, res, renderKinds)
}

func (reg *Registry) exportSingle(ctx context.Context, ttl time.Duration, res *studyAndInstances, kind orthanc.RenderKind) (repo.Artifact, error) {
	path, err := exportSingle(ctx, res.studyUID, res.instances, reg.cli, kind)
	if err != nil {
		return repo.Artifact{}, err
	}

	return reg.storeArtifact(ctx, path, ttl, res, []orthanc.RenderKind{kind})
}

func (reg *Registry) storeArtifact(ctx context.Context, path string, ttl time.Duration, res *studyAndInstances, kinds []orthanc.RenderKind) (repo.Artifact, error) {
	creator := ""

	if user := auth.From(ctx); user != nil {
		creator = user.ID
	}

	hasher := sha1.New()
	_, _ = hasher.Write(([]byte)(res.studyUID))

	filterUids := make([]string, len(res.instances))
	for idx, i := range res.instances {
		filterUids[idx], _ = i.MainDicomTags["SOPInstanceUID"].(string)
		_, _ = hasher.Write(([]byte)(filterUids[idx]))
	}

	for _, kind := range kinds {
		_, _ = hasher.Write(([]byte)(strconv.Itoa(int(kind))))
	}

	// Construct the artifact file name
	filename := filepath.Base(path)
	replace := func(s string) string {
		s = strings.ReplaceAll(s, "ERROR", "")
		s = strings.ReplaceAll(s, ",", "-")
		s = strings.ReplaceAll(s, " ", "-")
		s = strings.ReplaceAll(s, "\n", "")

		for strings.Contains(s, "--") {
			s = strings.ReplaceAll(s, "--", "-")
		}

		return strings.TrimSpace(s)
	}

	if res.responsiblePerson != "" || res.patientName != "" {
		parts := []string{}

		if on := replace(res.responsiblePerson); on != "" {
			parts = append(parts, on)
		}

		if pn := replace(res.patientName); pn != "" {
			parts = append(parts, pn)
		}

		if len(parts) == 0 {
			parts = []string{
				res.studyUID,
			}
		}

		filename = strings.Join(parts, "-") + filepath.Ext(path)
	}

	artifact := repo.Artifact{
		ID:           getRandomString(32),
		Filepath:     path,
		DownloadName: filename,
		CreatedAt:    time.Now(),
		ExpiresAt:    time.Now().Add(ttl),
		Creator:      creator,
		StudyUID:     res.studyUID,
		InstanceUIDs: filterUids,
		RenderTypes:  kinds,
		Hash:         hex.EncodeToString(hasher.Sum(nil)),
	}

	if err := reg.repo.AddArtifact(ctx, artifact); err != nil {
		defer os.Remove(path)

		return repo.Artifact{}, err
	}

	return artifact, nil
}

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")

func getRandomString(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return string(b)
}
