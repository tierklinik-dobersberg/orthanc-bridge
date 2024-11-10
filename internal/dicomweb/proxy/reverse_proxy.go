package proxy

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bufbuild/connect-go"
	"github.com/sirupsen/logrus"
	idmv1 "github.com/tierklinik-dobersberg/apis/gen/go/tkd/idm/v1"
	"github.com/tierklinik-dobersberg/apis/gen/go/tkd/idm/v1/idmv1connect"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/config"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/dicomweb"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/repo"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/urlutils"
	"github.com/ucarion/urlpath"
	"golang.org/x/sync/singleflight"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

var qidoMatcher = []urlpath.Path{
	urlpath.New("/dicom-web/studies"),
	urlpath.New("/dicom-web/studies/:study"),
	urlpath.New("/dicom-web/studies/:study/series"),
	urlpath.New("/dicom-web/studies/:study/series/:series"),
	urlpath.New("/dicom-web/studies/:study/series/:series/instances"),
	urlpath.New("/dicom-web/studies/:study/series/:series/instances/:instance/*"),
}

type Storage interface {
	GetStudyShare(ctx context.Context, token string) (*repo.StudyShare, error)
}

type resolvedAccessToken struct {
	validUntil    time.Time
	isUserAccount bool
	studShare     *repo.StudyShare
}

type SingelHostProxy struct {
	proxy *httputil.ReverseProxy

	Name      string
	Subdir    string
	PublicURL *url.URL

	userClient idmv1connect.AuthServiceClient

	config.OrthancInstance

	once  *singleflight.Group
	store Storage

	rw          sync.RWMutex
	validTokens map[string]resolvedAccessToken
}

func New(name string, storage Storage, subdir string, publicURL *url.URL, cfg config.OrthancInstance, userClient idmv1connect.AuthServiceClient) (*SingelHostProxy, error) {
	i := &SingelHostProxy{
		Name:            name,
		Subdir:          subdir,
		PublicURL:       publicURL,
		OrthancInstance: cfg,
		userClient:      userClient,
		validTokens:     make(map[string]resolvedAccessToken),
		once:            new(singleflight.Group),
		store:           storage,
	}

	proxy, err := i.buildProxy()
	if err != nil {
		return nil, err
	}

	i.proxy = proxy

	return i, nil
}

func (shp *SingelHostProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var resolved *resolvedAccessToken

	if token := getToken(r); token != "" {
		res, valid := shp.isValidToken(token)
		if valid {
			resolved = &res
		} else {
			res, valid := shp.validateToken(context.Background(), token)
			if valid {
				resolved = &res
			}
		}
	}

	if resolved == nil {
		http.Error(w, "you are not allowed to use the dicom-web interface", http.StatusUnauthorized)
		return
	}

	// for a share-token, ensure the user is actually allowed to perform the request
	if resolved.studShare != nil {
		match, isQido := isQidoUrl(r.URL.Path)
		if isQido {
			study, ok := match.Params["study"]
			if !ok {
				// check for StudyInstanceUIDs
				study = r.URL.Query().Get("StudyInstanceUIDs")
				if study == "" {
					// this is a study list request which is never allowed for share tokens
					http.Error(w, "you are not allowed to list studies", http.StatusUnauthorized)
					return
				}
			}

			if resolved.studShare.StudyUID != study {
				http.Error(w, "you are not allowed to access this study", http.StatusUnauthorized)
				return
			}

			instance, ok := match.Params["instance"]
			if ok && len(resolved.studShare.InstanceUIDs) > 0 {
				// check if the user has access to the requested instance
				if !slices.Contains(resolved.studShare.InstanceUIDs, instance) {
					http.Error(w, "you are not allowed to access this study instance", http.StatusUnauthorized)
					return
				}
			}
		}
	}

	r = r.WithContext(
		context.WithValue(r.Context(), proxyContextKey, *resolved),
	)

	shp.proxy.ServeHTTP(w, r)
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

func isQidoUrl(path string) (urlpath.Match, bool) {
	for _, p := range qidoMatcher {
		res, match := p.Match(path)
		if match {
			return res, true
		}
	}

	return urlpath.Match{}, false
}

var proxyContextKey = struct{ S string }{S: "proxyContextKey"}

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

		slog.Debug("forwarding DICOMWEB request", "target", req.URL.String())
	}

	return &httputil.ReverseProxy{
		Director: director,
		ModifyResponse: func(r *http.Response) error {
			contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))

			val := r.Request.Context().Value(proxyContextKey)

			if val == nil {
				return fmt.Errorf("no resolved access token available in request context")
			}

			token, ok := val.(resolvedAccessToken)
			if !ok {
				return fmt.Errorf("invalid resolved access token available in request context: %T", val)
			}

			if err != nil {
				slog.Error("failed to parse mime type", "mimeType", r.Header.Get("Content-type"), "error", err)

				return nil
			}

			match, isQuido := isQidoUrl(r.Request.URL.Path)

			switch {
			case contentType == "application/dicom+json" && isQuido:
				return p.rewriteQidoBody(r, token, match)

			default:
				// don't do anything here
			}

			return nil
		},
	}, nil
}

func unpackBody(r *http.Response) ([]byte, error) {
	var bodyReader io.Reader = r.Body

	// wrap bodyReader in a gzip.Reader if the response is compressed
	switch v := r.Header.Get("Content-Encoding"); v {
	case "gzip":
		reader, err := gzip.NewReader(r.Body)
		if err != nil {
			return nil, err
		}

		bodyReader = reader
	case "":
		bodyReader = r.Body
	default:
		return nil, fmt.Errorf("unsupported content-encoding %q for QIDO-RS", v)
	}

	// read the whole body from the backend server
	blob, err := io.ReadAll(bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to read body: %s", err)
	}

	return blob, nil
}

func (p *SingelHostProxy) rewriteQidoBody(r *http.Response, token resolvedAccessToken, match urlpath.Match) error {
	blob, err := unpackBody(r)
	if err != nil {
		return err
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

		if token.studShare != nil && len(token.studShare.InstanceUIDs) > 0 {
			var instanceUid string

			val, ok := s[dicomweb.SOPInstanceUID]
			if ok && len(val.Value) > 0 {
				instanceUid, ok = val.Value[0].(string)
			}

			if ok && !slices.Contains(token.studShare.InstanceUIDs, instanceUid) {
				slog.Info("filtered SOPInstanceUID since it's not allowed by the share token", "uid", instanceUid)

				continue
			}
		}

		if retrieveURI, ok := s[dicomweb.RetrieveURI]; ok {
			for idx, value := range retrieveURI.Value {
				if str, ok := value.(string); ok {
					updated, err := p.updateOutgoingURL(str)
					if err != nil {
						slog.Error("failed to update outgoing URLs for tag RetrieveURI", "error", err)

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
						slog.Error("failed to update outgoing URLs for tag RetrieveURL", "error", err)

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

func (shp *SingelHostProxy) validateToken(ctx context.Context, token string) (resolvedAccessToken, bool) {
	// check for share tokens
	if strings.HasPrefix(token, repo.ShareTokenPrefix) {
		share, err := shp.store.GetStudyShare(ctx, token)
		if err != nil {
			slog.Error("failed to fetch study share token", "token", token, "error", err.Error())

			// Better deny the request in an error case
			return resolvedAccessToken{}, false
		}

		// ensure the share token is still valid
		if !share.IsValid() {
			return resolvedAccessToken{}, false
		}

		r := resolvedAccessToken{
			validUntil: share.ExpiresAt,
			studShare:  share,
		}

		shp.cacheToken(token, r)

		return r, true
	}

	resolved, _, _ := shp.once.Do(token, func() (interface{}, error) {
		req := connect.NewRequest(&idmv1.IntrospectRequest{
			ReadMask: &fieldmaskpb.FieldMask{
				Paths: []string{"user.id", "user.username", "user.display_name", "valid_time"},
			},
		})

		req.Header().Set("Authorization", "Bearer "+token)

		slog.Info("querying IDM for token validity", "token-prefix", token[:8]+"****")
		res, err := shp.userClient.Introspect(ctx, req)
		if err == nil {
			resolved := resolvedAccessToken{
				isUserAccount: true,
			}

			if res.Msg.ValidTime.IsValid() {
				resolved.validUntil = res.Msg.ValidTime.AsTime()

				shp.cacheToken(token, resolved)
			}

			return &resolved, nil
		}

		return nil, nil
	})

	if resolved != nil {
		return *(resolved.(*resolvedAccessToken)), true
	}

	return resolvedAccessToken{}, false
}

func (shp *SingelHostProxy) isValidToken(token string) (resolvedAccessToken, bool) {
	shp.rw.RLock()
	defer shp.rw.RUnlock()

	t, ok := shp.validTokens[token]
	if !ok {
		return resolvedAccessToken{}, false
	}

	valid := time.Now().Before(t.validUntil)

	if !valid {
		go func() {
			shp.rw.Lock()
			defer shp.rw.Unlock()

			delete(shp.validTokens, token)
		}()
	}

	return t, valid
}

func (shp *SingelHostProxy) cacheToken(token string, resolved resolvedAccessToken) {
	shp.rw.Lock()
	defer shp.rw.Unlock()

	shp.validTokens[token] = resolved
}

func getToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return strings.TrimSpace(h[7:])
	}

	for _, c := range r.Cookies() {
		// TODO(ppacher): make cookie name configurable
		if c.Name == "cis_idm_access" {
			return c.Value
		}
	}

	return ""
}
