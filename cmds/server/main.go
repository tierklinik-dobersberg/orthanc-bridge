package main

import (
	"context"
	"net/http"
	"net/url"
	"os"

	"github.com/bufbuild/connect-go"
	"github.com/bufbuild/protovalidate-go"
	"github.com/sirupsen/logrus"
	"github.com/tierklinik-dobersberg/apis/gen/go/tkd/orthanc_bridge/v1/orthanc_bridgev1connect"
	"github.com/tierklinik-dobersberg/apis/pkg/auth"
	"github.com/tierklinik-dobersberg/apis/pkg/cors"
	"github.com/tierklinik-dobersberg/apis/pkg/log"
	"github.com/tierklinik-dobersberg/apis/pkg/server"
	"github.com/tierklinik-dobersberg/apis/pkg/validator"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/config"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/dicomweb"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/service"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/viewer"
	"google.golang.org/protobuf/reflect/protoregistry"
)

func main() {
	ctx := context.Background()

	logger := log.L(ctx)

	var cfgFilePath string
	if len(os.Args) > 1 {
		cfgFilePath = os.Args[1]
	}

	cfg, err := config.LoadConfig(ctx, cfgFilePath)
	if err != nil {
		logger.Fatalf("failed to load configuration: %s", err)
	}
	logger.Infof("configuration loaded successfully")

	providers, err := config.NewProviders(ctx, *cfg)
	if err != nil {
		logger.Fatalf("failed to prepare providers: %s", err)
	}
	logger.Infof("application providers prepared successfully")

	protoValidator, err := protovalidate.New()
	if err != nil {
		logger.Fatalf("failed to prepare protovalidator: %s", err)
	}

	authInterceptor := auth.NewAuthAnnotationInterceptor(
		protoregistry.GlobalFiles,
		auth.NewIDMRoleResolver(providers.Roles),
		auth.RemoteHeaderExtractor)

	interceptors := connect.WithInterceptors(
		log.NewLoggingInterceptor(),
		authInterceptor,
		validator.NewInterceptor(protoValidator),
	)

	_ = interceptors

	corsConfig := cors.Config{
		AllowedOrigins:   cfg.AllowedOrigins,
		AllowCredentials: true,
	}

	// Prepare our servemux and add handlers.
	serveMux := http.NewServeMux()

	serveMux.Handle("/", viewer.Handler())

	publicURL, err := url.Parse(cfg.PublicURL)
	if err != nil {
		logrus.Fatalf("failed to parse publicURL setting: %s", err)
	}

	// setup reverse proxy routes for each orthanc instance
	for name, instance := range cfg.Instances {
		prefix := "/bridge/" + name + "/"

		proxy, err := dicomweb.New(name, prefix, publicURL, instance)
		if err != nil {
			logger.Fatalf("failed to create dicomweb-proxy for %s: %s", name, err)
		}

		serveMux.Handle(prefix, http.StripPrefix(prefix, proxy))
	}

	// create a new CallService and add it to the mux.
	svc := service.New(providers)

	path, handler := orthanc_bridgev1connect.NewOrthancBridgeHandler(svc, interceptors)
	serveMux.Handle(path, handler)

	// Create the server
	srv, err := server.CreateWithOptions(cfg.PublicListenAddress, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dicomweb.AddCORSHeaders(w)
		serveMux.ServeHTTP(w, r)
	}), server.WithCORS(corsConfig))

	if err != nil {
		logger.Fatalf("failed to create HTTP/2 server: %s", err)
	}

	logger.Infof("HTTP/2 server (h2c) prepared successfully, startin to listen ...")

	if err := server.Serve(ctx, srv); err != nil {
		logger.Fatalf("failed to serve: %s", err)
	}
}
