package export

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/tierklinik-dobersberg/apis/pkg/auth"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/orthanc"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/repo"
	"golang.org/x/exp/rand"
)

type Storage interface {
	AddArtifact(context.Context, repo.Artifact) error
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

func (reg *Registry) ExportStudy(ctx context.Context, ttl time.Duration, studyUid string, instances []orthanc.FindInstancesResponse, filterUids []string, renderKinds []orthanc.RenderKind) (repo.Artifact, error) {
	path, err := CreateStudyArchive(ctx, reg.cli, studyUid, instances, filterUids, renderKinds)
	if err != nil {
		return repo.Artifact{}, err
	}

	return reg.storeArtifact(ctx, path, ttl, studyUid, filterUids, renderKinds)
}

func (reg *Registry) ExportSingle(ctx context.Context, ttl time.Duration, studyUid string, instanceUid string, instances []orthanc.FindInstancesResponse, kind orthanc.RenderKind) (repo.Artifact, error) {
	path, err := ExportSingle(ctx, studyUid, instanceUid, instances, reg.cli, kind)
	if err != nil {
		return repo.Artifact{}, err
	}

	return reg.storeArtifact(ctx, path, ttl, studyUid, []string{instanceUid}, []orthanc.RenderKind{kind})
}

func (reg *Registry) storeArtifact(ctx context.Context, path string, ttl time.Duration, studyUid string, filterUids []string, kinds []orthanc.RenderKind) (repo.Artifact, error) {
	creator := ""

	if user := auth.From(ctx); user != nil {
		creator = user.ID
	}

	artifact := repo.Artifact{
		ID:           getRandomString(32),
		Filepath:     path,
		CreatedAt:    time.Now(),
		ExpiresAt:    time.Now().Add(ttl),
		Creator:      creator,
		StudyUID:     studyUid,
		InstanceUIDs: filterUids,
		RenderTypes:  kinds,
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
