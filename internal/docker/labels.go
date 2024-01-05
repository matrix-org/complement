package docker

import (
	"strings"

	"github.com/docker/docker/api/types/filters"
)

// label returns a filter for the presence of certain labels ("complement_context") or a match of
// labels ("complement_blueprint=foo").
func label(labelFilters ...string) filters.Args {
	f := filters.NewArgs()
	// label=<key> or label=<key>=<value>
	for _, in := range labelFilters {
		f.Add("label", in)
	}
	return f
}

func tokensFromLabels(labels map[string]string) map[string]string {
	userIDToToken := make(map[string]string)
	for k, v := range labels {
		if strings.HasPrefix(k, "access_token_") {
			userIDToToken[strings.TrimPrefix(k, "access_token_")] = v
		}
	}
	return userIDToToken
}

func deviceIDsFromLabels(labels map[string]string) map[string]string {
	userIDToToken := make(map[string]string)
	for k, v := range labels {
		if strings.HasPrefix(k, "device_id") {
			userIDToToken[strings.TrimPrefix(k, "device_id")] = v
		}
	}
	return userIDToToken
}
