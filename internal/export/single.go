package export

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"

	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/orthanc"
)

func exportSingle(ctx context.Context, studyUid string, instances []orthanc.FindInstancesResponse, client *orthanc.Client, kind orthanc.RenderKind) (string, error) {
	instance := instances[0]

	ext, err := getExtension(kind)
	if err != nil {
		return "", err
	}

	blob, err := render(ctx, client, instance, kind)
	if err != nil {
		return "", err
	}

	tmpFile, err := os.CreateTemp("", instance.ID+"-*-"+ext)
	if err != nil {
		return "", err
	}
	defer tmpFile.Close()

	if _, err := io.Copy(tmpFile, bytes.NewReader(blob)); err != nil {
		defer os.Remove(tmpFile.Name())

		return "", fmt.Errorf("failed to write file: %w", err)
	}

	return tmpFile.Name(), nil
}

func getExtension(kind orthanc.RenderKind) (string, error) {
	switch kind {
	case orthanc.KindDICOM:
		return ".dcm", nil
	case orthanc.KindJPEG:
		return ".jpg", nil
	case orthanc.KindPNG:
		return ".png", nil
	case orthanc.KindAVI:
		return ".avi", nil

	default:
		return "", fmt.Errorf("unsupported render kind")
	}
}
