package export

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"

	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/orthanc"
)

func createStudyArchive(ctx context.Context, client *orthanc.Client, studyUid string, instances []orthanc.FindInstancesResponse, renderKinds []orthanc.RenderKind) (string, error) {

	// create a temporary directory and download all files into it
	dir, err := os.MkdirTemp("", "archive-"+studyUid+"-raw-")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary directory: %w", err)
	}
	// make sure we clean up afterwards
	defer os.RemoveAll(dir)

	// download each instance to the temporary directory
	// TODO(ppacher): instead of reading the images to RAM and then writting
	// 				  to the file consider streaming the response directly to the FS
	for _, instance := range instances {
		slog.Info("downloading DICOM instance", "id", instance.ID)

		for _, kind := range renderKinds {

			// skip JPEG and PNG images if we are going to create a AVI for multi-frame images
			if _, ok := instance.MainDicomTags["NumberOfFrames"]; ok && slices.Contains(renderKinds, orthanc.KindAVI) && (kind == orthanc.KindJPEG || kind == orthanc.KindPNG) {
				// skip it since we are going to create a MJPEG file for this instance anyway
				continue
			}

			blob, err := render(ctx, client, instance, kind)
			if err != nil {
				// not applicable to render
				if errors.Is(err, ErrNotApplicable) {
					continue
				}

				return "", fmt.Errorf("failed to download and render instance %s: %w", instance.ID, err)
			}

			ext, err := getExtension(kind)
			if err != nil {
				return "", err
			}

			dest := filepath.Join(dir, instance.ID+ext)
			if err := os.WriteFile(dest, blob, 0o600); err != nil {
				return "", fmt.Errorf("failed to write instance image/file to dist: %w", err)
			}

			slog.Info("succesfully downloaded instance file", "name", dest, "id", instance.ID, "size", len(blob))
		}
	}

	// Create the archive file and a zip writer
	archiveFile, err := os.CreateTemp("", "archive-"+studyUid+"-*.zip")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary archive file: %w", err)
	}
	defer archiveFile.Close()
	archive := zip.NewWriter(archiveFile)

	if err := archive.AddFS(os.DirFS(dir)); err != nil {
		defer os.Remove(archiveFile.Name())

		return "", fmt.Errorf("failed to create archive: %w", err)
	}

	if err := archive.Close(); err != nil {
		defer os.Remove(archiveFile.Name())

		return "", fmt.Errorf("failed to finish archive: %w", err)
	}

	return archiveFile.Name(), nil
}
