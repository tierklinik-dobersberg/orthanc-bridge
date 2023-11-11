package orthanc

type GetSeriesResponse struct {
	ID            string
	IsStable      bool
	Instances     []string
	LastUpdate    string
	MainDicomTags map[string]string
	ParentStudy   string
	Status        string
	Type          string
}

type GetStudyResponse struct {
	ID                   string
	IsStable             bool
	LastUpdate           string
	MainDicomTags        map[string]string
	ParentPatient        string
	PatientMainDicomTags map[string]string
	Series               []string
	Type                 string
}

type GetInstanceResponse struct {
	ID            string
	Type          string
	FileSize      int
	MainDicomTags map[string]string
}

type ChangesResult struct {
	Changes []ChangeResult
	Done    bool
	Last    int
}

type ChangeResult struct {
	ChangeType   string
	Date         string
	ID           string
	Path         string
	ResourceType string
	Seq          int
}

type InstanceTag struct {
	Name  string
	Type  string
	Value any
}
