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
		merr.Errors = append(merr.Errors, fmt.Errorf("%s: not found", tag))
		return ""
	}

	id, ok := value.(string)
	if !ok {
		merr.Errors = append(merr.Errors, fmt.Errorf("%s: %w (%T / %v)", tag, dicomweb.ErrUnexpectedValueType, value, value))
		return ""
	}

	return id
}

func parseStringList(res dicomweb.QIDOResponse, tag string, merr *multierror.Error) []string {
	value, ok := res.Get(tag)
	if !ok {
		merr.Errors = append(merr.Errors, fmt.Errorf("%s: no found", tag))
		return nil
	}

	var result []string

	for _, value := range value {
		id, ok := value.(string)
		if !ok {
			merr.Errors = append(merr.Errors, fmt.Errorf("%s: %w (%T / %c)", tag, dicomweb.ErrUnexpectedValueType, value, value))
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
	if err != nil {
		if merr != nil {
			merr.Errors = append(merr.Errors, fmt.Errorf("%s: %w", dateTag, err))
		}
		return time.Time{}
	}

	if len(dates) == 0 {
		if merr != nil {
			merr.Errors = append(merr.Errors, fmt.Errorf("%s: no values found", dateTag))
		}
		return time.Time{}
	}

	times, err := dicomweb.ParseTM(res[timeTag])
	if err != nil {
		if merr != nil {
			merr.Errors = append(merr.Errors, fmt.Errorf("%s: %w", timeTag, err))
		}
		return time.Time{}
	}

	if len(times) == 0 {
		if merr != nil {
			merr.Errors = append(merr.Errors, fmt.Errorf("%s: no values found", timeTag))
		}
		return time.Time{}
	}

	return times[0].At(dates[0])
}
