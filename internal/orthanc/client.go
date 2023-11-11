package orthanc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

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

func (cli *Client) doRequest(ctx context.Context, method string, endpoint urlpath.Path, params map[string]string, query url.Values, body io.Reader, response any) error {
	finalizedEndpoint, ok := endpoint.Build(urlpath.Match{
		Params: params,
	})

	if !ok {
		return ErrMissingParameters
	}

	ep := &url.URL{
		Scheme:  cli.baseURL.Scheme,
		User:    cli.baseURL.User,
		Host:    cli.baseURL.Host,
		RawPath: finalizedEndpoint,
	}

	ep.Path, ep.RawPath = urlutils.JoinURLPath(cli.baseURL, ep)

	req, err := http.NewRequestWithContext(ctx, method, ep.String(), body)
	if err != nil {
		return err
	}

	res, err := cli.cli.Do(req)
	if err != nil {
		return err
	}

	defer res.Body.Close()

	if response != nil {
		dec := json.NewDecoder(res.Body)

		if err := dec.Decode(response); err != nil {
			return err
		}
	}

	return nil
}
