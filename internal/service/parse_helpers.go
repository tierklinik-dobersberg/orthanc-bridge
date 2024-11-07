package service

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/hashicorp/go-multierror"
	orthanc_bridgev1 "github.com/tierklinik-dobersberg/apis/gen/go/tkd/orthanc_bridge/v1"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/dicomweb"
	"google.golang.org/protobuf/types/known/structpb"
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
		if merr != nil {
			merr.Errors = append(merr.Errors, fmt.Errorf("%s: no found", tag))
		}
		return nil
	}

	var result []string

	for _, value := range value {
		id, ok := value.(string)
		if !ok {
			if merr != nil {
				merr.Errors = append(merr.Errors, fmt.Errorf("%s: %w (%T / %c)", tag, dicomweb.ErrUnexpectedValueType, value, value))
			}

			continue
		}
		result = append(result, id)
	}

	return result
}

func parseSingleName(res dicomweb.QIDOResponse, tag string, merr *multierror.Error) string {
	name, err := dicomweb.ParsePN(res[tag])
	if err != nil {
		if merr != nil {
			merr.Errors = append(merr.Errors, fmt.Errorf("%s: %w", tag, err))
		}

		return ""
	}

	if len(name) == 0 {
		if merr != nil {
			merr.Errors = append(merr.Errors, fmt.Errorf("%s: no values", tag))
		}
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

func parseTags(r dicomweb.QIDOResponse) []*orthanc_bridgev1.DICOMTag {
	var result []*orthanc_bridgev1.DICOMTag

	for key, t := range r {
		var values []*structpb.Value

		for _, v := range t.Value {
			vpb, err := structpb.NewValue(v)
			if err != nil {
				slog.Error("failed to convert value for dicom tag", "tag", key, "error", err)
				continue
			}

			values = append(values, vpb)
		}

		result = append(result, &orthanc_bridgev1.DICOMTag{
			Tag:                 key,
			ValueRepresentation: t.VR,
			Value:               values,
			Name:                dicomweb.TagToName[key],
		})
	}

	return result
}
