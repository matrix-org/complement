package client

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type Federation struct {
	BaseURL string
	HSName  string
	Client  *http.Client
}

// MustDo is the same as Do but fails the test if the response is not 2xx
func (c *Federation) MustDo(t *testing.T, method string, paths []string, jsonBody interface{}) *http.Response {
	res, err := c.Do(t, method, paths, jsonBody)
	if err != nil {
		t.Fatalf("CSAPI.MustDo %s %s error: %s", method, strings.Join(paths, "/"), err)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		t.Fatalf("CSAPI.MustDo %s %s returned HTTP %d", method, res.Request.URL.String(), res.StatusCode)
	}
	return res
}

// Do a JSON request.
func (c *Federation) Do(t *testing.T, method string, paths []string, jsonBody interface{}) (*http.Response, error) {
	for i := range paths {
		paths[i] = url.PathEscape(paths[i])
	}

	reqURL := "https://" + c.HSName + "/" + strings.Join(paths, "/")
	b, err := json.Marshal(jsonBody)
	if err != nil {
		t.Fatalf("CSAPI.Do failed to marshal JSON body: %s", err)
	}
	req, err := http.NewRequest(method, reqURL, bytes.NewReader(b))
	if err != nil {
		t.Fatalf("CSAPI.Do failed to create http.NewRequest: %s", err)
	}
	req.Header.Set("Content-Type", "application/json")
	return c.Client.Do(req)
}
