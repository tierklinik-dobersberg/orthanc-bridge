package orthanc

import (
	"context"
	"fmt"
	"net/http"

	"github.com/ucarion/urlpath"
)

var (
	instanceList       = urlpath.New("/instances")
	getInstance        = urlpath.New("/instances/:id")
	getInstanceDicom   = urlpath.New("/instances/:id/file")
	getInstancePreview = urlpath.New("/instances/:id/preview")
	getInstanceTags    = urlpath.New("/instances/:id/simplified-tags")
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
		ExpandedFindResponse `json:",inline"`
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

type RenderKind int

const (
	KindDICOM = RenderKind(iota)
	KindPNG
	KindJPEG
)

func (c *Client) GetRenderedInstance(ctx context.Context, instanceId string, accept RenderKind) ([]byte, error) {
	var (
		p            urlpath.Path
		acceptHeader string
	)

	switch accept {
	case KindDICOM:
		p = getInstanceDicom
	case KindPNG:
		p = getInstancePreview
		acceptHeader = "image/png"
	case KindJPEG:
		p = getInstancePreview
		acceptHeader = "image/jpeg"

	default:
		return nil, fmt.Errorf("invalid download type")
	}

	var response []byte
	if err := c.doRequest(
		ctx,
		http.MethodGet,
		p,
		map[string]string{
			"id": instanceId,
		},
		nil,
		nil,
		&response,
		func(r *http.Request) {
			if acceptHeader != "" {
				r.Header.Set("Accept", acceptHeader)
			}
		},
	); err != nil {
		return nil, fmt.Errorf("failed to download instance: %w", err)
	}

	return ([]byte)(response), nil
}
