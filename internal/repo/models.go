package repo

import (
	"time"

	"github.com/hashicorp/go-multierror"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/dicomweb"
)

type Study struct {
	ID                string    `bson:"id"`
	ResponsiblePerson string    `bson:"responsiblePerson"`
	PatientName       string    `bson:"patientName"`
	Time              time.Time `bson:"time"`

	Series []Series `bson:"series"`
}

func StudyFromQIDO(res dicomweb.QIDOResponse) (Study, error) {
	merr := new(multierror.Error)

	study := Study{
		ID:                parseFirstString(res, dicomweb.StudyInstanceUID, merr),
		ResponsiblePerson: parseSingleName(res, dicomweb.ResponsiblePerson, merr),
		PatientName:       parseSingleName(res, dicomweb.PatientName, merr),
		Time:              parseDateAndTime(res, dicomweb.StudyDate, dicomweb.StudyTime, merr),
	}

	return study, merr.ErrorOrNil()
}

type Series struct {
	ID        string     `bson:"id"`
	Instances []Instance `bson:"instances"`
}

func SeriesFromQIDO(res dicomweb.QIDOResponse) (Series, error) {
	merr := new(multierror.Error)

	series := Series{
		ID: parseFirstString(res, dicomweb.SeriesInstanceUID, merr),
	}

	return series, merr.ErrorOrNil()
}

type Instance struct {
	ID string `bson:"id"`
}

func InstanceFromQIDO(res dicomweb.QIDOResponse) (Instance, error) {
	merr := new(multierror.Error)

	instance := Instance{
		ID: parseFirstString(res, dicomweb.SOPInstanceUID, merr),
	}

	return instance, merr.ErrorOrNil()
}
