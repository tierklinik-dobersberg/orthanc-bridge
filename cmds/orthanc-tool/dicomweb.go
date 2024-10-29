package main

import (
	"context"
	"encoding/json"
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

			blob, err := json.MarshalIndent(res, "", "    ")
			if err != nil {
				logrus.Fatalf("failed to marshal response: %s", err)
			}

			fmt.Print(string(blob))
		},
	}

	f := cmd.Flags()

	f.IntVar(&req.Limit, "limit", 0, "")
	f.IntVar(&req.Offset, "offset", 0, "")
	f.StringSliceVar(&req.IncludeFields, "include-field", nil, "")
	f.StringSliceVar(&filterTags, "filter", nil, "Format: <tag>=<value>")
	f.BoolVar(&req.FuzzyMatching, "fuzzy", true, "")

	return cmd
}
