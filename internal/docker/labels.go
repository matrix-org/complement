package docker

import (
	"strings"

	"github.com/docker/docker/api/types/filters"

	"github.com/matrix-org/complement/b"
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

func asIDToRegistrationFromLabels(labels map[string]string) map[string]string {
	asMap := make(map[string]string)
	for k, v := range labels {
		if strings.HasPrefix(k, "application_service_") {
			// cf comment of generateASRegistrationYaml for ReplaceAll explanation
			asMap[strings.TrimPrefix(k, "application_service_")] = strings.ReplaceAll(v, "\\n", "\n")
		}
	}
	return asMap
}

func labelsForApplicationServices(hs b.Homeserver) map[string]string {
	labels := make(map[string]string)
	// collect and store app service registrations as labels 'application_service_$as_id: $registration'
	// collect and store app service access tokens as labels 'access_token_$sender_localpart: $as_token'
	for _, as := range hs.ApplicationServices {
		labels["application_service_"+as.ID] = generateASRegistrationYaml(as)

		labels["access_token_@"+as.SenderLocalpart+":"+hs.Name] = as.ASToken
	}
	return labels
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
