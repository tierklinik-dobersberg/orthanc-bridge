package export

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/icza/mjpeg"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/orthanc"
)

var ErrNotApplicable = errors.New("render type not applicable for instance")

func render(ctx context.Context, cli *orthanc.Client, instance orthanc.FindInstancesResponse, kind orthanc.RenderKind) ([]byte, error) {
	if kind != orthanc.KindAVI {
		return cli.GetRenderedInstance(ctx, instance.ID, 0, kind)
	}

	numberOfFrames, ok := instance.MainDicomTags["NumberOfFrames"]
	if !ok {
		return nil, ErrNotApplicable
	}

	conv, ok := numberOfFrames.(float64)
	if !ok {
		return nil, fmt.Errorf("invalid value for NumberOfFrames: %v (%T)", numberOfFrames, numberOfFrames)
	}

	tmpFile, err := os.CreateTemp("", instance.ID+"-*.avi")
	if err != nil {
		return nil, err
	}
	tmpFile.Close()
	defer os.Remove(tmpFile.Name())

	writer, err := mjpeg.New(tmpFile.Name(), 2000, 2000, 10)
	if err != nil {
		return nil, err
	}

	for i := 1; i <= int(conv); i++ {
		blob, err := cli.GetRenderedInstance(ctx, instance.ID, i, orthanc.KindJPEG)
		if err != nil {
			return nil, fmt.Errorf("failed to get rendered frame: %w", err)
		}

		if err := writer.AddFrame(blob); err != nil {
			return nil, fmt.Errorf("failed to create frame: %w", err)
		}
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("failed to close mjpeg writer: %w", err)
	}

	return os.ReadFile(tmpFile.Name())
}