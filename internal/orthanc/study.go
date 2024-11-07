package orthanc

import (
	"context"
	"net/http"

	"github.com/ucarion/urlpath"
)

var (
	studyList    = urlpath.New("/studies")
	getStudy     = urlpath.New("/studies/:id")
	getStudyTags = urlpath.New("/studies/:id/simplified-tags")
)

type (
	ListStudiesResponse []GetStudyResponse

	GetStudyResponse struct {
		ID                   string
		IsStable             bool
		LastUpdate           string
		MainDicomTags        map[string]string
		ParentPatient        string
		PatientMainDicomTags map[string]string
		Series               []string
		Type                 string
	}

	FindStudiesResponse struct {
		ExpandedFindResponse `json:",inline"`
		Series               []string
	}
)

func (c *Client) ListStudies(ctx context.Context, opts ...QueryOption) (res ListStudiesResponse, err error) {
	if err := c.doRequest(ctx, http.MethodGet, studyList, nil, mergeOpts(WithExpand(), opts), nil, &res); err != nil {
		return nil, err
	}

	return res, nil
}

func (c *Client) GetStudy(ctx context.Context, id string) (res GetStudyResponse, err error) {
	if err := c.doRequest(ctx, http.MethodGet, getStudy, map[string]string{"id": id}, nil, nil, &res); err != nil {
		return GetStudyResponse{}, err
	}

	return res, nil
}

func (c *Client) GetStudyTags(ctx context.Context, id string) (res SimplifiedTags, err error) {
	if err := c.doRequest(ctx, http.MethodGet, getStudyTags, map[string]string{"id": id}, nil, nil, &res); err != nil {
		return SimplifiedTags{}, err
	}

	return res, nil
}

func (c *Client) FindStudy(ctx context.Context, findOpts ...FindOption) (res []FindStudiesResponse, err error) {
	req := &FindRequest{
		CaseSensitive: false,
		Expand:        true,
		Query:         make(map[string]any),
		Level:         LevelStudy,
	}

	for _, opt := range findOpts {
		opt(req)
	}

	var response []FindStudiesResponse

	if err := c.doRequest(ctx, http.MethodPost, toolsFind, nil, nil, req, &response); err != nil {
		return nil, err
	}

	return response, nil
}
