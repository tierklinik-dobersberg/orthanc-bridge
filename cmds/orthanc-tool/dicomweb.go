package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/dicomweb"
)

func getDicomWebCommand() *cobra.Command {
	var filterTags []string

	req := dicomweb.QIDORequest{
		Type:       dicomweb.Study,
		FilterTags: make(map[string][]string),
	}

	cmd := &cobra.Command{
		Use: "dicomweb [flags]",
		Run: func(cmd *cobra.Command, args []string) {
			if req.StudyInstanceUID != "" {
				req.Type = dicomweb.Series
			}

			if req.SeriesInstanceUID != "" {
				req.Type = dicomweb.Instance
			}

			if req.SOPInstanceUID != "" {
				req.Type = dicomweb.Metadata
			}

			cli := dicomweb.NewClient(server)

			for _, filter := range filterTags {
				key, value, found := strings.Cut(filter, "=")

				if !found {
					logrus.Fatalf("invalid value for --filter: %q", filter)
				}

				req.FilterTags[key] = append(req.FilterTags[key], value)
			}

			res, err := cli.Query(context.Background(), req)
			if err != nil {
				logrus.Fatalf("failed to query: %s", err)
			}

			for _, r := range res {
				blob, _ := r.PrettyJSON()
				fmt.Println(string(blob))
			}
		},
	}

	f := cmd.Flags()

	f.IntVar(&req.Limit, "limit", 0, "")
	f.IntVar(&req.Offset, "offset", 0, "")
	f.StringSliceVar(&req.IncludeFields, "include-field", nil, "")
	f.StringSliceVar(&filterTags, "filter", nil, "Format: <tag>=<value>")
	f.BoolVar(&req.FuzzyMatching, "fuzzy", true, "")
	f.StringVar(&req.StudyInstanceUID, "study-id", "", "")
	f.StringVar(&req.SeriesInstanceUID, "series-id", "", "")
	f.StringVar(&req.SOPInstanceUID, "instance-id", "", "")

	return cmd
}
