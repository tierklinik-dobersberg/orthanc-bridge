package proxy

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/config"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/dicomweb"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/urlutils"
	"github.com/ucarion/urlpath"
)

type SingelHostProxy struct {
	*httputil.ReverseProxy

	Name      string
	Subdir    string
	PublicURL *url.URL

	config.OrthancInstance
}

func New(name string, subdir string, publicURL *url.URL, cfg config.OrthancInstance) (*SingelHostProxy, error) {
	i := &SingelHostProxy{
		Name:            name,
		Subdir:          subdir,
		PublicURL:       publicURL,
		OrthancInstance: cfg,
	}

	proxy, err := i.buildProxy()
	if err != nil {
		return nil, err
	}

	i.ReverseProxy = proxy

	return i, nil
}
func rewriteRequestURL(req *http.Request, target *url.URL) {
	targetQuery := target.RawQuery
	req.URL.Scheme = target.Scheme
	req.URL.Host = target.Host
	req.URL.Path, req.URL.RawPath = urlutils.JoinURLPath(target, req.URL)

	if targetQuery == "" || req.URL.RawQuery == "" {
		req.URL.RawQuery = targetQuery + req.URL.RawQuery
	} else {
		req.URL.RawQuery = targetQuery + "&" + req.URL.RawQuery
	}
}

var (
	// TODO(ppacher): make /dicom-web/ root configurable per instance
	qidoStudyURL    = urlpath.New("/dicom-web/studies")
	qidoSeriesURL   = urlpath.New("/dicom-web/studies/:study/series")
	qidoInstanceURL = urlpath.New("/dicom-web/studies/:study/series/:series/instances")
)

func isQidoUrl(path string) bool {
	if _, match := qidoStudyURL.Match(path); match {
		return true
	}

	if _, match := qidoSeriesURL.Match(path); match {
		return true
	}

	if _, match := qidoInstanceURL.Match(path); match {
		return true
	}

	return false
}

func (p *SingelHostProxy) buildProxy() (*httputil.ReverseProxy, error) {
	target, err := url.Parse(p.Address)
	if err != nil {
		return nil, fmt.Errorf("failed to parse address: %w", err)
	}

	director := func(req *http.Request) {
		rewriteRequestURL(req, target)

		if p.Username != "" {
			req.SetBasicAuth(p.Username, p.Password)
		}

		if host := p.RewriteHost; host != "" {
			req.Host = host
		}

		// check for WADO-RS /rendered requests and fix the Accept header as
		// orthanc does not accept image/* mime type
		if strings.HasSuffix(req.URL.Path, "/rendered") {
			req.Header.Set("Accept", "image/png")
		}

		logrus.Infof("forwarding to %s", req.URL.String())
	}

	return &httputil.ReverseProxy{
		Director: director,
		ModifyResponse: func(r *http.Response) error {
			contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))

			if err != nil {
				logrus.Errorf("failed to parse media type %q: %s", r.Header.Get("Content-Type"), err)

				return nil
			}

			switch {
			case contentType == "application/dicom+json" && isQidoUrl(r.Request.URL.Path):
				return p.rewriteQidoBody(r)

			default:
				logrus.Infof("skipping body modification for content-type %s", contentType)
				// don't do anything here
			}

			return nil
		},
	}, nil
}

func (p *SingelHostProxy) rewriteQidoBody(r *http.Response) error {
	var bodyReader io.Reader = r.Body

	// wrap bodyReader in a gzip.Reader if the response is compressed
	switch v := r.Header.Get("Content-Encoding"); v {
	case "gzip":
		reader, err := gzip.NewReader(r.Body)
		if err != nil {
			return err
		}

		bodyReader = reader
	case "":
		bodyReader = r.Body
	default:
		return fmt.Errorf("unsupported content-encoding %q for QIDO-RS", v)
	}

	// read the whole body from the backend server
	blob, err := io.ReadAll(bodyReader)
	if err != nil {
		return fmt.Errorf("failed to read body: %s", err)
	}

	// close the body now, it will be replaced anyways
	r.Body.Close()

	var qido []dicomweb.QIDOResponse
	if err := json.Unmarshal(blob, &qido); err != nil {
		logrus.Fatalf("failed to deocde qido reponse: %s", err)

		return nil
	}

	// fix the hostname in the RetrieveURI and RetrieveURL values
	count := 0
	copy := make([]dicomweb.QIDOResponse, 0, len(qido))
	for _, s := range qido {

		if retrieveURI, ok := s[dicomweb.RetrieveURI]; ok {
			for idx, value := range retrieveURI.Value {
				if str, ok := value.(string); ok {
					updated, err := p.updateOutgoingURL(str)
					if err != nil {
						logrus.Errorf("RetrieveURI: %s", err.Error())

						continue
					}

					s[dicomweb.RetrieveURI].Value[idx] = updated
					count++
				}
			}
		}

		if retrieveURL, ok := s[dicomweb.RetrieveURL]; ok {
			for idx, value := range retrieveURL.Value {
				if str, ok := value.(string); ok {
					updated, err := p.updateOutgoingURL(str)
					if err != nil {
						logrus.Errorf("RetrieveURL: %s", err.Error())

						continue
					}

					s[dicomweb.RetrieveURL].Value[idx] = updated
					count++
				}
			}
		}

		copy = append(copy, s)
	}

	// re-create the response body
	blobBuf := bytes.NewBuffer([]byte{})
	enc := json.NewEncoder(blobBuf)
	enc.SetIndent("", "  ")

	if err := enc.Encode(copy); err != nil {
		return err
	}

	// prepare a buffer to compress the updated QIDO repsonse to
	buf := new(bytes.Buffer)
	gzipWriter := gzip.NewWriter(buf)

	if _, err := io.Copy(gzipWriter, blobBuf); err != nil {
		return fmt.Errorf("failed to compress blob: %s", err)
	}

	if err := gzipWriter.Close(); err != nil {
		return fmt.Errorf("failed to flush and close gzip writer: %s", err)
	}

	// Set the new gzip compressed body
	r.Body = io.NopCloser(buf)

	// Update the response headers
	r.Header.Set("Content-Encoding", "gzip")
	r.Header.Set("Content-Length", fmt.Sprintf("%d", buf.Len()))

	logrus.Infof("intercepted QIDO-RS call and replaced %d URLs", count)

	// we're done
	return nil
}

func (p *SingelHostProxy) updateOutgoingURL(v string) (string, error) {
	// create a cop yof PublicURL
	var publicURL url.URL = *p.PublicURL

	publicURL.Path = urlutils.SingleJoiningSlash(publicURL.Path, p.Subdir)

	rurl, err := url.Parse(v)
	if err != nil {
		return "", fmt.Errorf("failed to parse url %q: %w", v, err)
	}

	rurl.Scheme = publicURL.Scheme
	targetQuery := publicURL.RawQuery
	rurl.Scheme = publicURL.Scheme
	rurl.Host = publicURL.Host
	rurl.Path, rurl.RawPath = urlutils.JoinURLPath(&publicURL, rurl)

	if targetQuery == "" || rurl.RawQuery == "" {
		rurl.RawQuery = targetQuery + rurl.RawQuery
	} else {
		rurl.RawQuery = targetQuery + "&" + rurl.RawQuery
	}

	return rurl.String(), nil
}

func AddCORSHeaders(r http.ResponseWriter) {
	headers := map[string]string{
		"Access-Control-Allow-Methods":     "GET, POST, OPTIONS",
		"Access-Control-Allow-Headers":     "DNT,User-Agent,X-Requested-With,If-Modified-Since,Cache-Control,Content-Type,Range,Authorization",
		"Access-Control-Allow-Credentials": "true",
		"Access-Control-Max-Age":           "172800",
		"Access-Control-Expose-Headers":    "Content-Length,Content-Range",
		"Cross-Origin-Opener-Policy":       "same-origin",
		"Cross-Origin-Embedder-Policy":     "require-corp",
		"Cross-Origin-Resource-Policy":     "cross-origin",
	}

	for key, val := range headers {
		r.Header().Set(key, val)
	}
}
