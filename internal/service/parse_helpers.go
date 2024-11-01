package service

import (
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/go-multierror"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/dicomweb"
)

func parseFirstString(res dicomweb.QIDOResponse, tag string, merr *multierror.Error) string {
	value, ok := res.GetFirst(tag)
	if !ok {
		merr.Errors = append(merr.Errors, fmt.Errorf("no studyInstanceUID found"))
		return ""
	}

	id, ok := value.(string)
	if !ok {
		merr.Errors = append(merr.Errors, fmt.Errorf("studyInstanceUID: %w", dicomweb.ErrUnexpectedValueType))
		return ""
	}

	return id
}

func parseStringList(res dicomweb.QIDOResponse, tag string, merr *multierror.Error) []string {
	value, ok := res.Get(tag)
	if !ok {
		merr.Errors = append(merr.Errors, fmt.Errorf("no studyInstanceUID found"))
		return nil
	}

	var result []string

	for _, value := range value {
		id, ok := value.(string)
		if !ok {
			merr.Errors = append(merr.Errors, fmt.Errorf("studyInstanceUID: %w", dicomweb.ErrUnexpectedValueType))
			return nil
		}
		result = append(result, id)
	}

	return result
}

func parseSingleName(res dicomweb.QIDOResponse, tag string, merr *multierror.Error) string {
	name, err := dicomweb.ParsePN(res[tag])
	if err != nil {
		merr.Errors = append(merr.Errors, fmt.Errorf("%s: %w", tag, err))

		return ""
	}

	if len(name) == 0 {
		merr.Errors = append(merr.Errors, fmt.Errorf("%s: no values", tag))
		return ""
	}

	return strings.TrimSpace(name[0])
}

func parseDateAndTime(res dicomweb.QIDOResponse, dateTag, timeTag string, merr *multierror.Error) time.Time {
	dates, err := dicomweb.ParseDA(res[dateTag])
	if err != nil || len(dates) == 0 {
		merr.Errors = append(merr.Errors, fmt.Errorf("%s: invalid or unavailable", dateTag))
		return time.Time{}
	}

	times, err := dicomweb.ParseTM(res[timeTag])
	if err != nil || len(times) == 0 {
		merr.Errors = append(merr.Errors, fmt.Errorf("%s: invalid or unavailable", timeTag))
		return time.Time{}
	}

	return times[0].At(dates[0])
}
