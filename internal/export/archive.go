package export

import (
	"archive/zip"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"

	"github.com/bufbuild/connect-go"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/orthanc"
)

func CreateStudyArchive(ctx context.Context, client *orthanc.Client, studyUid string, instances []orthanc.FindInstancesResponse, filterUids []string, renderKinds []orthanc.RenderKind) (string, error) {
	l := slog.With("studyUid", studyUid)

	// Gather all instance IDS that we want to download.
	filtered := make(map[string]string, len(instances))
	for _, instance := range instances {
		sopInstanceUid, ok := instance.MainDicomTags["SOPInstanceUID"].(string)
		if !ok {
			l.Error("invalid orthanc response, SOPInstanceUID is expected to be a string")
			continue
		}

		if len(filterUids) == 0 || slices.Contains(filterUids, sopInstanceUid) {
			l.Info("marking DICOM instance for download", "sopInstanceUid", sopInstanceUid, "id", instance.ID)
			filtered[instance.ID] = sopInstanceUid
		}
	}

	// ensure there are actual instances to download
	if len(filtered) == 0 {
		return "", connect.NewError(connect.CodeNotFound, fmt.Errorf("no instances to download"))
	}

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
	for id, sopInstanceUID := range filtered {
		slog.Info("downloading DICOM instance", "id", id)

		for _, kind := range renderKinds {
			blob, err := client.GetRenderedInstance(ctx, id, kind)
			if err != nil {
				return "", fmt.Errorf("failed to download instance %s (%s): %w", id, sopInstanceUID, err)
			}

			var ext string
			switch kind {
			case orthanc.KindDICOM:
				ext = ".dcm"
			case orthanc.KindJPEG:
				ext = ".jpg"
			case orthanc.KindPNG:
				ext = ".png"

			default:
				return "", fmt.Errorf("invalid render type %d", kind)
			}

			dest := filepath.Join(dir, sopInstanceUID+ext)
			if err := os.WriteFile(dest, blob, 0o600); err != nil {
				return "", fmt.Errorf("failed to write instance image/file to dist: %w", err)
			}

			slog.Info("succesfully downloaded instance file", "name", dest, "id", id, "sopInstanceUID", sopInstanceUID, "size", len(blob))
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
