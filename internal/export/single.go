package export

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/bufbuild/connect-go"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/orthanc"
)

func ExportSingle(ctx context.Context, studyUid string, instanceUid string, instances []orthanc.FindInstancesResponse, client *orthanc.Client, kind orthanc.RenderKind) (string, error) {
	var instance *orthanc.FindInstancesResponse

	for _, i := range instances {
		sopInstanceUid, ok := i.MainDicomTags["SOPInstanceUID"].(string)
		if !ok {
			slog.Error("invalid orthanc response, SOPInstanceUID is expected to be a string")
			continue
		}

		if sopInstanceUid == instanceUid {
			instance = &i
			break
		}
	}

	if instance == nil {
		return "", connect.NewError(connect.CodeNotFound, fmt.Errorf("instance not found"))
	}

	ext, err := getExtension(kind)
	if err != nil {
		return "", err
	}

	blob, err := render(ctx, client, *instance, kind)
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
