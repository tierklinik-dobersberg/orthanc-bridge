package repo

import (
	"time"

	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/orthanc"
)

const ShareTokenPrefix = "sh_"

type Artifact struct {
	ID           string               `bson:"artifactId"`
	Filepath     string               `bson:"filepath"`
	DownloadName string               `bson:"downloadName"`
	CreatedAt    time.Time            `bson:"createdAt"`
	ExpiresAt    time.Time            `bson:"expiresAt"`
	Creator      string               `bson:"creator"`
	StudyUID     string               `bson:"studyUid"`
	InstanceUIDs []string             `bson:"instanceUids"`
	Hash         string               `bson:"hash"`
	RenderTypes  []orthanc.RenderKind `bson:"renderKinds"`
}

type StudyShare struct {
	Token        string    `bson:"token"`
	CreatedAt    time.Time `bson:"createdAt"`
	ExpiresAt    time.Time `bson:"expiresAt"`
	Creator      string    `bson:"creator"`
	StudyUID     string    `bson:"studyUid"`
	InstanceUIDs []string  `bson:"instanceUids"`
	Recipients   []string  `bson:"recipients"`
}

func (share StudyShare) IsValid() bool {
	if share.ExpiresAt.IsZero() {
		return true
	}

	return time.Now().Before(share.ExpiresAt)
}
