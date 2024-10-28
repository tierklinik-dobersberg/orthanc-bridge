package orthanc

import (
	"context"
	"net/http"

	"github.com/ucarion/urlpath"
)

// Orthanc endpoint definitions
var (
	listPatients = urlpath.New("/patients")
	getPatient   = urlpath.New("/patients/:id")
)

type (
	ListPatientsResponse []GetPatientResponse

	FindPatientResponse struct {
		FindResponse `json:",inline"`

		Studies []string
	}

	GetPatientResponse struct {
		ID            string
		IsStable      bool
		LastUpdate    string
		MainDicomTags map[string]string
		Studies       []string
		Type          string
	}
)

func (c *Client) ListPatients(ctx context.Context, opts ...QueryOption) (res ListPatientsResponse, err error) {
	if err := c.doRequest(ctx, http.MethodGet, listPatients, nil, mergeOpts(WithExpand(), opts), nil, &res); err != nil {
		return res, err
	}

	return res, err
}

func (c *Client) GetPatient(ctx context.Context, id string) (res GetPatientResponse, err error) {
	err = c.doRequest(ctx, http.MethodGet, getPatient, map[string]string{"id": id}, nil, nil, &res)

	return res, err
}

func (c *Client) FindPatient(ctx context.Context, opts ...FindOption) ([]FindPatientResponse, error) {
	req := &FindRequest{
		CaseSensitive: false,
		Expand:        true,
		Query:         make(map[string]any),
		Level:         LevelPatient,
	}

	for _, opt := range opts {
		opt(req)
	}

	var response []FindPatientResponse

	if err := c.doRequest(ctx, http.MethodPost, toolsFind, nil, nil, req, &response); err != nil {
		return nil, err
	}

	return response, nil
}
