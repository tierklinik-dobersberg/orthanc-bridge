package orthanc

import (
	"net/url"
	"strconv"
)

type (
	ChangesResult struct {
		Changes []ChangeResult
		Done    bool
		Last    int
	}

	ChangeResult struct {
		ChangeType   string
		Date         string
		ID           string
		Path         string
		ResourceType string
		Seq          int
	}

	InstanceTag struct {
		Name  string
		Type  string
		Value any
	}

	SimplifiedTags map[string]string

	QueryOption func(q url.Values)
)

func WithLimit(limit int) QueryOption {
	return func(q url.Values) {
		q.Set("limit", strconv.FormatInt(int64(limit), 10))
	}
}

func WithRequestedTags(tags []string) QueryOption {
	return func(q url.Values) {
		for _, tag := range tags {
			q.Add("requestedTags", tag)
		}
	}
}

func WithSince(since int) QueryOption {
	return func(q url.Values) {
		q.Set("since", strconv.FormatInt(int64(since), 10))
	}
}

func WithExpand() QueryOption {
	return func(q url.Values) {
		q.Set("expand", "true")
	}
}

func mergeOpts(opt QueryOption, rest []QueryOption) []QueryOption {
	return append(rest, opt)
}
