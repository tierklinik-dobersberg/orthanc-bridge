package dicomweb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/bufbuild/connect-go"
)

type Client struct {
	baseUrl    string
	httpClient connect.HTTPClient
}

type ClientOption func(*Client)

// NewClient returns a new DICOMWeb client
func NewClient(url string, opts ...ClientOption) *Client {
	cli := &Client{
		baseUrl:    strings.TrimSuffix(url, "/"),
		httpClient: http.DefaultClient,
	}

	for _, opt := range opts {
		opt(cli)
	}

	return cli
}

func (cli *Client) InstancePreview(ctx context.Context, study, series, instance string) ([]byte, string, error) {
	endpoint := fmt.Sprintf("%s/studies/%s/series/%s/instances/%s/rendered", cli.baseUrl, study, series, instance)

	r, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create HTTP request: %w", err)
	}

	res, err := cli.httpClient.Do(r)
	if err != nil {
		return nil, "", fmt.Errorf("failed to fetch preview: %w", err)
	}
	defer res.Body.Close()

	content, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read response body: %w", err)
	}

	if res.StatusCode != http.StatusOK {
		res.Body = io.NopCloser(bytes.NewReader(content))
		return nil, "", &ResponseError{
			Response: res,
		}
	}

	contentType := res.Header.Get("Content-Type")

	return content, contentType, nil
}

func (cli *Client) Query(ctx context.Context, req QIDORequest) ([]QIDOResponse, error) {
	endpoint := cli.baseUrl

	switch req.Type {
	case Study:
		endpoint += "/studies"
	case Series:
		endpoint += "/studies/" + req.StudyInstanceUID
		endpoint += "/series"

	case Instance:
		endpoint += "/studies/" + req.StudyInstanceUID
		endpoint += "/series/" + req.SeriesInstanceUID
		endpoint += "/instances"

	case Metadata:
		endpoint += "/studies/" + req.StudyInstanceUID
		endpoint += "/series/" + req.SeriesInstanceUID
		endpoint += "/instances/" + req.SOPInstanceUID + "/metadata"

	default:
		return nil, errors.New("failed to query: need to specify query type")
	}

	values := url.Values{}

	// append all query values
	if req.Limit > 0 {
		values.Add("limit", strconv.Itoa(req.Limit))
	}

	if req.Offset > 0 {
		values.Add("offset", strconv.Itoa(req.Offset))
	}

	if req.FuzzyMatching {
		values.Add("fuzzymatching", "true")
	}

	for _, field := range req.IncludeFields {
		// try to lookup the field-tag by name and fallback to using
		// field directly
		tag, ok := TagNames[field]
		if !ok {
			tag = field
		}

		values.Add("includefield", tag)
	}

	for field, filterValues := range req.FilterTags {
		// try to lookup the field-tag by name and fallback to using
		// field directly
		tag, ok := TagNames[field]
		if !ok {
			tag = field
		}

		for _, val := range filterValues {
			values.Add(tag, val)
		}
	}

	if req.SOPInstanceUID != "" {
		values.Add(SOPInstanceUID, req.SOPInstanceUID)
	}

	if req.SeriesInstanceUID != "" {
		values.Add(SeriesInstanceUID, req.SeriesInstanceUID)
	}

	if req.StudyInstanceUID != "" {
		values.Add(StudyInstanceUID, req.StudyInstanceUID)
	}

	if e := values.Encode(); e != "" {
		endpoint += "?" + e
	}

	slog.Debug("sending QIDO query request", "url", endpoint)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	response, err := cli.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to perform HTTP GET request: %w", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if response.StatusCode != http.StatusOK {
		response.Body = io.NopCloser(bytes.NewReader(body))

		return nil, &ResponseError{response}
	}

	var quidoResponse []QIDOResponse
	if err := json.Unmarshal(body, &quidoResponse); err != nil {
		return nil, fmt.Errorf("failed to decode response body: %w (body: %s)", err, string(body)[:80])
	}

	return quidoResponse, nil
}

type ResponseError struct {
	Response *http.Response
}

func (re *ResponseError) Error() string {
	return re.Response.Status
}
