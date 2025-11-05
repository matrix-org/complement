package internal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/matrix-org/complement/internal"
)

// LoadSyncData loads sync data from disk or by doing a /sync request
func LoadSyncData(hsURL, token, tempFile string) (json.RawMessage, error) {
	syncData := loadDataFromDisk(tempFile)
	if syncData != nil {
		log.Printf("Loaded sync data from %s\n", tempFile)
		return syncData, nil
	}
	// We need to do a remote hit to the homeserver
	httpCli := &http.Client{}

	filterStr := url.QueryEscape(`{"event_format":"federation", "room":{"timeline":{"limit":50}}}`)

	attempts := 0

	var body []byte
	for attempts < 20 {
		attempts++
		// Perform the sync
		log.Println("Performing /sync...")
		filterReq, err := http.NewRequest("GET", hsURL+"/_matrix/client/v3/sync?filter="+filterStr, nil)
		if err != nil {
			log.Printf("failed to create sync request: %s\n", err)
			continue
		}
		body, err = doRequest(httpCli, filterReq, token)
		if err != nil {
			log.Printf("failed to perform sync request: %s\n", err)
			continue
		}
		break
	}
	if body == nil {
		return nil, fmt.Errorf("failed to perform /sync")
	}

	// dump it straight to disk first
	err := ioutil.WriteFile(tempFile, body, 0644)
	if err != nil {
		log.Printf("WARNING: failed to write sync data to disk: %s", err)
	}

	return body, nil
}

func loadDataFromDisk(tempFile string) json.RawMessage {
	data, err := ioutil.ReadFile(tempFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		log.Panicf("FATAL: failed to read data from disk: %s", err)
	}
	return data
}

func doRequest(httpCli *http.Client, req *http.Request, token string) ([]byte, error) {
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := httpCli.Do(req)
	defer internal.CloseIO(
		res.Body,
		fmt.Sprintf(
			"doRequest: response body from %s %s",
			res.Request.Method,
			res.Request.URL.String(),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to perform request: %w", err)
	}
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("response returned %s", res.Status)
	}
	out := bytes.NewBuffer(nil)
	// non-fatal if we have no content length
	cl, _ := strconv.Atoi(res.Header.Get("Content-Length"))
	counter := &writeCounter{
		contentLength: cl,
	}
	if _, err = io.Copy(out, io.TeeReader(res.Body, counter)); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

type writeCounter struct {
	contentLength int
	total         int
}

func (wc *writeCounter) Write(p []byte) (int, error) {
	n := len(p)
	wc.total += n
	// wipe current line
	fmt.Fprintf(os.Stderr, "\r%s", strings.Repeat(" ", 80))
	fmt.Fprintf(os.Stderr, "\rDownloading... %d / %d KB complete", wc.total/1024, wc.contentLength/1024)
	return n, nil
}
