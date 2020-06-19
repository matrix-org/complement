package client

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
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

// MustDoAndParse is the same as MustDo but will unmarshal the response body into `responseBody`, which should be a pointer to the struct.
// Also returns the HTTP body as a slice of bytes.
func (c *Federation) MustDoAndParse(t *testing.T, method string, paths []string, jsonBody interface{}, responseBody interface{}) []byte {
	res := c.MustDo(t, method, paths, jsonBody)
	defer res.Body.Close()
	b, err := ioutil.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("Federation.MustDoAndParse failed to read response body: %s", err)
	}
	if err := json.Unmarshal(b, responseBody); err != nil {
		t.Fatalf("Federation.MustDoAndParse failed to unmarshal into response body: %s", err)
	}
	return b
}

// MustDo is the same as Do but fails the test if the response is not 2xx
func (c *Federation) MustDo(t *testing.T, method string, paths []string, jsonBody interface{}) *http.Response {
	res, err := c.Do(t, method, paths, jsonBody)
	if err != nil {
		t.Fatalf("Federation.MustDo %s %s error: %s", method, strings.Join(paths, "/"), err)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		t.Fatalf("Federation.MustDo %s %s returned HTTP %d", method, res.Request.URL.String(), res.StatusCode)
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
		t.Fatalf("Federation.Do failed to marshal JSON body: %s", err)
	}
	req, err := http.NewRequest(method, reqURL, bytes.NewReader(b))
	if err != nil {
		t.Fatalf("Federation.Do failed to create http.NewRequest: %s", err)
	}
	req.Header.Set("Content-Type", "application/json")
	return c.Client.Do(req)
}
