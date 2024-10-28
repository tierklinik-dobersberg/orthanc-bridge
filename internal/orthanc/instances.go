package orthanc

import (
	"context"
	"net/http"

	"github.com/ucarion/urlpath"
)

var (
	instanceList    = urlpath.New("/instances")
	getInstance     = urlpath.New("/instances/:id")
	getInstanceTags = urlpath.New("/instances/:id/simplified-tags")
)

type (
	ListInstanceResponse []GetInstanceResponse

	GetInstanceResponse struct {
		ID            string
		Type          string
		FileSize      int
		MainDicomTags map[string]string
	}

	FindInstancesResponse struct {
		FindResponse `json:",inline"`
	}
)

func (c *Client) ListInstances(ctx context.Context, opts ...QueryOption) (res ListInstanceResponse, err error) {
	if err := c.doRequest(ctx, http.MethodGet, instanceList, nil, mergeOpts(WithExpand(), opts), nil, &res); err != nil {
		return nil, err
	}

	return res, nil
}

func (c *Client) GetInstance(ctx context.Context, id string) (res GetInstanceResponse, err error) {
	if err := c.doRequest(ctx, http.MethodGet, getInstance, map[string]string{"id": id}, nil, nil, &res); err != nil {
		return GetInstanceResponse{}, err
	}

	return res, nil
}

func (c *Client) GetInstanceTags(ctx context.Context, id string) (res SimplifiedTags, err error) {
	if err := c.doRequest(ctx, http.MethodGet, getInstanceTags, map[string]string{"id": id}, nil, nil, &res); err != nil {
		return SimplifiedTags{}, err
	}

	return res, nil
}

func (c *Client) FindInstances(ctx context.Context, findOpts ...FindOption) (res []FindInstancesResponse, err error) {
	req := &FindRequest{
		CaseSensitive: false,
		Expand:        true,
		Query:         make(map[string]any),
		Level:         LevelInstance,
	}

	for _, opt := range findOpts {
		opt(req)
	}

	var response []FindInstancesResponse

	if err := c.doRequest(ctx, http.MethodPost, toolsFind, nil, nil, req, &response); err != nil {
		return nil, err
	}

	return response, nil
}
