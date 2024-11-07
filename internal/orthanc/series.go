package orthanc

import (
	"context"
	"net/http"

	"github.com/ucarion/urlpath"
)

var (
	seriesList    = urlpath.New("/series")
	getSeries     = urlpath.New("/series/:id")
	getSeriesTags = urlpath.New("/series/:id/simplified-tags")
)

type (
	ListSeriesResponse []GetSeriesResponse

	GetSeriesResponse struct {
		ID            string
		IsStable      bool
		Instances     []string
		LastUpdate    string
		MainDicomTags map[string]string
		ParentStudy   string
		Status        string
		Type          string
	}

	FindSeriesResponse struct {
		ExpandedFindResponse `json:",inline"`

		Instances []string
	}
)

func (c *Client) ListSeries(ctx context.Context, opts ...QueryOption) (res ListSeriesResponse, err error) {
	if err := c.doRequest(ctx, http.MethodGet, seriesList, nil, mergeOpts(WithExpand(), opts), nil, &res); err != nil {
		return nil, err
	}

	return res, nil
}

func (c *Client) GetSeries(ctx context.Context, id string) (res GetSeriesResponse, err error) {
	if err := c.doRequest(ctx, http.MethodGet, getSeries, map[string]string{"id": id}, nil, nil, &res); err != nil {
		return GetSeriesResponse{}, err
	}

	return res, nil
}

func (c *Client) GetSeriesTags(ctx context.Context, id string) (res SimplifiedTags, err error) {
	if err := c.doRequest(ctx, http.MethodGet, getSeriesTags, map[string]string{"id": id}, nil, nil, &res); err != nil {
		return SimplifiedTags{}, err
	}

	return res, nil
}

func (c *Client) FindSeries(ctx context.Context, findOpts ...FindOption) (res []FindSeriesResponse, err error) {
	req := &FindRequest{
		CaseSensitive: false,
		Expand:        true,
		Query:         make(map[string]any),
		Level:         LevelSeries,
	}

	for _, opt := range findOpts {
		opt(req)
	}

	var response []FindSeriesResponse

	if err := c.doRequest(ctx, http.MethodPost, toolsFind, nil, nil, req, &response); err != nil {
		return nil, err
	}

	return response, nil
}
