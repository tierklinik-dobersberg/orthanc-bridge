package main

import (
	"context"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/orthanc"
)

func getInstancesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use: "instances",
		Run: func(cmd *cobra.Command, args []string) {
			res, err := cli.ListInstances(context.Background())
			if err != nil {
				logrus.Fatalf("ListInstances: %s", err)
			}

			print(res)
		},
	}

	cmd.AddCommand(getFindInstancesCommand())

	return cmd
}

func getFindInstancesCommand() *cobra.Command {
	var (
		patientID         string
		responsiblePerson string
		patientName       string
		customTags        []string
	)

	cmd := &cobra.Command{
		Use: "find",
		Run: func(cmd *cobra.Command, args []string) {
			opts := []orthanc.FindOption{}

			if patientID != "" {
				opts = append(opts, orthanc.ByPatientID(patientID))
			}

			if responsiblePerson != "" {
				opts = append(opts, orthanc.ByResponsiblePerson(responsiblePerson))
			}

			if patientName != "" {
				opts = append(opts, orthanc.ByPatientName(patientName))
			}

			opts = append(opts, parseCustomTags(customTags)...)

			res, err := cli.FindInstances(context.Background(), opts...)
			if err != nil {
				logrus.Fatalf("FindInstances: %s", err)
			}

			print(res)
		},
	}

	f := cmd.Flags()
	{
		f.StringVar(&patientID, "id", "", "The internal orthanc ID of the patient")
		f.StringVar(&responsiblePerson, "owner", "", "The name of the owner")
		f.StringVar(&patientName, "name", "", "The name of the patient")
		f.StringSliceVar(&customTags, "dicom-tag", nil, "")
	}

	return cmd
}
