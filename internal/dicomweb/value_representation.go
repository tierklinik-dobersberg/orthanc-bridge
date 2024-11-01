package dicomweb

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/go-multierror"
	"github.com/mitchellh/mapstructure"
	commonv1 "github.com/tierklinik-dobersberg/apis/gen/go/tkd/common/v1"
)

var (
	ErrUnexpectedVR        = errors.New("unexpected VR")
	ErrUnexpectedValueType = errors.New("unexpected value type")
)

type personName struct {
	Alphabetic string `mapstructure:"Alphabetic"`
}

func ParsePN(t Tag) ([]string, error) {
	if t.VR != "PN" {
		return nil, ErrUnexpectedVR
	}

	names := make([]string, len(t.Value))
	merr := new(multierror.Error)

	for idx, val := range t.Value {
		s, ok := val.(map[string]any)
		if !ok {
			merr.Errors = append(merr.Errors, fmt.Errorf("value at index %d: %w", idx, ErrUnexpectedValueType))
			continue
		}

		var p personName
		if err := mapstructure.Decode(s, &p); err != nil {
			merr.Errors = append(merr.Errors, fmt.Errorf("value at index %d: %w", idx, ErrUnexpectedValueType))
			continue
		}

		names[idx] = strings.TrimSpace(p.Alphabetic)
	}

	return names, merr.ErrorOrNil()
}

func ParseDA(t Tag) ([]time.Time, error) {
	return ParseDAInLocation(t, time.UTC)
}

func ParseDAInLocation(t Tag, loc *time.Location) ([]time.Time, error) {
	if t.VR != "DA" {
		return nil, ErrUnexpectedVR
	}

	dates := make([]time.Time, len(t.Value))
	merr := new(multierror.Error)

	for idx, val := range t.Value {
		s, ok := val.(string)
		if !ok {
			merr.Errors = append(merr.Errors, fmt.Errorf("value at index %d: %w", idx, ErrUnexpectedValueType))
			continue
		}

		parsed, err := time.ParseInLocation("20060102", s, loc)
		if err != nil {
			merr.Errors = append(merr.Errors, fmt.Errorf("value at index %d: invalid date format: %w", idx, err))
			continue
		}

		dates[idx] = parsed
	}

	return dates, merr.ErrorOrNil()
}

func ParseDT(t Tag) ([]time.Time, error) {
	return ParseDAInLocation(t, time.UTC)
}

func ParseDTInLocation(t Tag, loc *time.Location) ([]time.Time, error) {
	if t.VR != "DT" {
		return nil, ErrUnexpectedVR
	}

	dates := make([]time.Time, len(t.Value))
	merr := new(multierror.Error)

	for idx, val := range t.Value {
		s, ok := val.(string)
		if !ok {
			merr.Errors = append(merr.Errors, fmt.Errorf("value at index %d: %w", idx, ErrUnexpectedValueType))
			continue
		}

		parsed, err := time.ParseInLocation("20060102150405.999999-0700", s, time.Local)
		if err != nil {
			// Try without a timezone specifier
			parsed, err = time.ParseInLocation("20060102150405.999999", s, time.Local)
		}

		if err != nil {
			merr.Errors = append(merr.Errors, fmt.Errorf("value at index %d: invalid date format: %w", idx, err))
			continue
		}

		dates[idx] = parsed
	}

	return dates, merr.ErrorOrNil()
}

func ParseTM(t Tag) ([]*commonv1.DayTime, error) {
	if t.VR != "TM" {
		return nil, ErrUnexpectedVR
	}

	dates := make([]*commonv1.DayTime, len(t.Value))
	merr := new(multierror.Error)

	for idx, val := range t.Value {
		s, ok := val.(string)
		if !ok {
			merr.Errors = append(merr.Errors, fmt.Errorf("value at index %d: %w", idx, ErrUnexpectedValueType))
			continue
		}

		parsed, err := time.ParseInLocation("150405.999999", s, time.Local)

		if err != nil {
			merr.Errors = append(merr.Errors, fmt.Errorf("value at index %d: invalid date format: %w", idx, err))
			continue
		}

		dates[idx] = &commonv1.DayTime{
			Hour:   int32(parsed.Hour()),
			Minute: int32(parsed.Minute()),
			Second: int32(parsed.Second()),
		}
	}

	return dates, merr.ErrorOrNil()
}
