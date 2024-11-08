package export

import (
	"context"
	"os"
	"time"

	"github.com/tierklinik-dobersberg/apis/pkg/auth"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/orthanc"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/repo"
	"golang.org/x/exp/rand"
)

type Storage interface {
	AddArtifact(context.Context, repo.Artifact) error
}

type Registry struct {
	repo Storage

	cli *orthanc.Client
}

func NewRegistry(cli *orthanc.Client, repo Storage) *Registry {
	return &Registry{
		repo: repo,
		cli:  cli,
	}
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
