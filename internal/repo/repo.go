package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var (
	ErrNotFound = errors.New("not found")
)

type Repo struct {
	artifacts *mongo.Collection
	shares    *mongo.Collection
}

func New(ctx context.Context, url string, db string) (*Repo, error) {
	cli, err := mongo.Connect(ctx, options.Client().ApplyURI(url))
	if err != nil {
		return nil, err
	}

	if err := cli.Ping(ctx, nil); err != nil {
		return nil, err
	}

	r := &Repo{
		artifacts: cli.Database(db).Collection("artifacts"),
		shares:    cli.Database(db).Collection("shares"),
	}

	// setup indexes
	if _, err := r.artifacts.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys: bson.D{
				{
					Key:   "artifactId",
					Value: 1,
				},
			},
			Options: options.Index().SetUnique(true),
		},
		{
			Keys: bson.D{
				{
					Key:   "expiresAt",
					Value: 1,
				},
			},
		},
		{
			Keys: bson.D{
				{
					Key:   "hash",
					Value: 1,
				},
			},
			Options: options.Index().SetUnique(true),
		},
	}); err != nil {
		return nil, err
	}

	return r, nil
}

func (r *Repo) AddArtifact(ctx context.Context, artifact Artifact) error {
	_, err := r.artifacts.InsertOne(ctx, artifact)
	if err != nil {
		return fmt.Errorf("failed to store artifact: %w", err)
	}

	return nil
}

func (r *Repo) FindByHashAndUpdateExpiry(ctx context.Context, hash string, expiry time.Time) (*Artifact, error) {
	res := r.artifacts.FindOneAndUpdate(
		ctx,
		bson.M{"hash": hash},
		bson.M{
			"$set": bson.M{
				"expiresAt": expiry,
			},
		},
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	)

	if err := res.Err(); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, ErrNotFound
		}

		return nil, err
	}

	var artifact Artifact
	if err := res.Decode(&artifact); err != nil {
		return nil, fmt.Errorf("failed to decode BSON document: %w", err)
	}

	return &artifact, nil
}

func (r *Repo) FindArtifact(ctx context.Context, id string) (*Artifact, error) {
	res := r.artifacts.FindOne(ctx, bson.M{"artifactId": id})
	if err := res.Err(); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, ErrNotFound
		}

		return nil, err
	}

	var artifact Artifact
	if err := res.Decode(&artifact); err != nil {
		return nil, fmt.Errorf("failed to decode BSON document: %w", err)
	}

	return &artifact, nil
}

func (r *Repo) FindCleanupCandidates(ctx context.Context, threshold time.Time) ([]Artifact, error) {
	res, err := r.artifacts.Find(ctx, bson.M{
		"expiresAt": bson.M{
			"$lte": threshold,
		},
	})

	if err != nil {
		return nil, fmt.Errorf("failed to perform find operation: %w", err)
	}

	var result []Artifact
	if err := res.All(ctx, &result); err != nil {
		return nil, fmt.Errorf("failed to decode BSON documents: %w", err)
	}

	return result, nil
}

func (r *Repo) DeleteArtifacts(ctx context.Context, ids []string) error {
	_, err := r.artifacts.DeleteMany(ctx, bson.M{
		"artifactId": bson.M{
			"$in": ids,
		},
	})

	if err != nil {
		return fmt.Errorf("failed to perform delete operation: %w", err)
	}

	return nil
}

func (r *Repo) ListArtifacts(ctx context.Context) ([]Artifact, error) {
	res, err := r.artifacts.Find(ctx, bson.M{})
	if err != nil {
		return nil, fmt.Errorf("failed to perform find operation: %w", err)
	}

	var result []Artifact
	if err := res.All(ctx, &result); err != nil {
		return nil, fmt.Errorf("failed to decode BSON documents: %w", err)
	}

	return result, nil
}

func (r *Repo) GetStudyShare(ctx context.Context, token string) (*StudyShare, error) {
	res := r.shares.FindOne(ctx, bson.M{"token": token})
	if err := res.Err(); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, ErrNotFound
		}

		return nil, err
	}

	var share StudyShare
	if err := res.Decode(&share); err != nil {
		return nil, fmt.Errorf("failed to decode BSON document: %w", err)
	}

	return &share, nil
}

func (r *Repo) CreateStudyShare(ctx context.Context, share StudyShare) error {
	_, err := r.shares.InsertOne(ctx, share)
	if err != nil {
		return fmt.Errorf("failed to insert share token: %w", err)
	}

	return nil
}
