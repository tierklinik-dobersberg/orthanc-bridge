package orthanc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/sirupsen/logrus"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/urlutils"
	"github.com/ucarion/urlpath"
)

type HTTPDoer interface {
	Do(r *http.Request) (*http.Response, error)
}

type Client struct {
	cli     HTTPDoer
	baseURL *url.URL
}

type ClientOption func(c *Client)

var (
	ErrMissingParameters = errors.New("missing URL path parameters")
)

func WithHTTPClient(cli HTTPDoer) ClientOption {
	return func(c *Client) {
		c.cli = cli
	}
}

func NewClient(baseURL string, opts ...ClientOption) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("url: %w", err)
	}

	cli := &Client{
		baseURL: u,
	}

	for _, opt := range opts {
		opt(cli)
	}

	if cli.cli == nil {
		cli.cli = http.DefaultClient
	}

	return cli, nil
}

func (cli *Client) doRequest(ctx context.Context, method string, endpoint urlpath.Path, params map[string]string, queryOpts []QueryOption, body any, response any, requestOptions ...RequestOption) error {
	finalizedEndpoint, ok := endpoint.Build(urlpath.Match{
		Params: params,
	})

	if !ok {
		return ErrMissingParameters
	}

	query := url.Values{}
	for _, opt := range queryOpts {
		opt(query)
	}

	ep := &url.URL{
		Scheme:   cli.baseURL.Scheme,
		User:     cli.baseURL.User,
		Host:     cli.baseURL.Host,
		RawPath:  finalizedEndpoint,
		Path:     finalizedEndpoint,
		RawQuery: query.Encode(),
	}

	ep.Path, ep.RawPath = urlutils.JoinURLPath(cli.baseURL, ep)

	var bodyBlob io.Reader

	if body != nil {
		var err error

		blob, err := json.Marshal(body)
		if err != nil {
			return err
		}

		logrus.Infof("body: %s", string(blob))

		bodyBlob = bytes.NewReader(blob)
	}

	req, err := http.NewRequestWithContext(ctx, method, ep.String(), bodyBlob)
	if err != nil {
		return err
	}

	for _, opt := range requestOptions {
		opt(req)
	}

	res, err := cli.cli.Do(req)
	if err != nil {
		return err
	}

	defer res.Body.Close()

	if response != nil {
		body, err := io.ReadAll(res.Body)
		if err != nil {
			return err
		}

		switch v := response.(type) {
		case *[]byte:
			*v = body
		default:
			dec := json.NewDecoder(bytes.NewReader(body))

			if err := dec.Decode(response); err != nil {
				logrus.Infof("body: \n%s", string(body))

				return err
			}
		}
	}

	return nil
}
