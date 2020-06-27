package match

// HTTPResponse is the desired shape of the HTTP response. Can include any number of JSON matchers.
type HTTPResponse struct {
	StatusCode int
	Headers    map[string]string
	JSON       []JSON
}

// HTTPRequest is the desired shape of the HTTP request. Can include any number of JSON matchers.
type HTTPRequest struct {
	Headers map[string]string
	JSON    []JSON
}
