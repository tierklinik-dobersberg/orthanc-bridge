package repo

import "context"

type Repository struct {
}

func NewRepository(ctx context.Context, databaseURL string) (*Repository, error) {
	r := &Repository{}

	return r, nil
}
