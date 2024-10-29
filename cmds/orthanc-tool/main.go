package main

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/orthanc"
)

var (
	server string

	cli *orthanc.Client
)

func getRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "orthanc-tool",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			var err error

			cli, err = orthanc.NewClient(server)
			if err != nil {
				logrus.Fatalf("failed to parse --server %q: %s", server, err)
			}
		},
	}

	cmd.PersistentFlags().StringVarP(&server, "server", "s", os.Getenv("ORTHANC_SERVER"), "The address of the orthanc server")

	cmd.AddCommand(
		getPatientCommand(),
		getStudiesCommand(),
		getSeriesCommand(),
		getInstancesCommand(),
		getDicomWebCommand(),
	)

	return cmd
}

func main() {
	if err := getRootCmd().Execute(); err != nil {
		logrus.Fatal(err.Error())
	}
}

func print(res any) {
	enc := json.NewEncoder(os.Stdout)

	enc.SetIndent("", "  ")

	enc.Encode(res)
}

func parseCustomTags(tags []string) []orthanc.FindOption {
	var opts []orthanc.FindOption

	for _, t := range tags {
		tag, value, ok := strings.Cut(t, "=")
		if !ok {
			logrus.Fatalf("invalid value for --dicom-tag: expected key=value")
		}

		opts = append(opts, orthanc.ByTag(tag, value))
	}

	return opts
}
