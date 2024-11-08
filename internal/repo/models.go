package repo

import (
	"time"

	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/orthanc"
)

type Artifact struct {
	ID           string               `bson:"artifactId"`
	Filepath     string               `bson:"filepath"`
	CreatedAt    time.Time            `bson:"createdAt"`
	ExpiresAt    time.Time            `bson:"expiresAt"`
	Creator      string               `bson:"creator"`
	StudyUID     string               `bson:"studyUid"`
	InstanceUIDs []string             `bson:"instanceUids"`
	Hash         string               `bson:"hash"`
	RenderTypes  []orthanc.RenderKind `bson:"renderKinds"`
}
