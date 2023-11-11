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
	ListPatientsResponse []string

	GetPatientResponse struct {
		ID            string
		IsStable      bool
		LastUpdate    string
		MainDicomTags map[string]string
		Studies       []string
		Type          string
	}
)

func (c *Client) ListPatients(ctx context.Context) (res ListPatientsResponse, err error) {
	if err := c.doRequest(ctx, http.MethodGet, listPatients, nil, nil, nil, &res); err != nil {
		return res, err
	}

	return res, err
}

func (c *Client) GetPatient(ctx context.Context, id string) (res GetPatientResponse, err error) {
	err = c.doRequest(ctx, http.MethodGet, getPatient, map[string]string{"id": id}, nil, nil, &res)

	return res, err
}
