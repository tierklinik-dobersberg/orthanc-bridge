package orthanc

import (
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/dicomweb"
	"github.com/ucarion/urlpath"
)

var (
	toolsFind = urlpath.New("/tools/find")
)

var (
	LabelAny  = LabelConstraint("Any")
	LabelAll  = LabelConstraint("All")
	LabelNone = LabelConstraint("None")
)

var (
	LevelPatient  = Level("Patient")
	LevelStudy    = Level("Study")
	LevelSeries   = Level("Series")
	LevelInstance = Level("Instance")
)

type (
	Level string

	LabelConstraint string

	FindOption func(*FindRequest)

	FindRequest struct {
		CaseSensitive    bool            `json:",omitempty"`
		Expand           bool            `json:",omitempty"`
		Full             bool            `json:",omitempty"`
		Labels           []string        `json:",omitempty"`
		LabelsConstraint LabelConstraint `json:",omitempty"`
		Level            Level           `json:",omitempty"`
		Limit            int             `json:",omitempty"`
		Query            map[string]any  `json:",omitempty"`
		RequstedTags     []string        `json:",omitempty"`
		Short            bool            `json:",omitempty"`
		Since            int             `json:",omitempty"`
	}

	ExpandedFindResponse struct {
		ID                   string
		IsStable             bool
		MainDicomTags        map[string]any
		PatientMainDicomTags map[string]any
		Type                 string
	}
)

func ByResponsiblePerson(person string) FindOption {
	return func(fr *FindRequest) {
		fr.Query[dicomweb.ResponsiblePerson] = person
	}
}

func ByPatientID(id string) FindOption {
	return func(fr *FindRequest) {
		fr.Query[dicomweb.PatientID] = id
	}
}

func ByPatientName(name string) FindOption {
	return func(fr *FindRequest) {
		fr.Query[dicomweb.PatientName] = name
	}
}

func ByTag(tag string, value string) FindOption {
	return func(fr *FindRequest) {
		fr.Query[tag] = value
	}
}

func WithFindLimit(limit int) FindOption {
	return func(fr *FindRequest) {
		fr.Limit = limit
	}
}

func ByStudyUID(uid string) FindOption {
	return func(fr *FindRequest) {
		fr.Query[dicomweb.StudyInstanceUID] = uid
	}
}
