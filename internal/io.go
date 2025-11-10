package internal

import (
	"io"
	"log"
)

// CloseIO is a little helper to close an io.Closer and log any error encountered.
//
// Based off of https://blevesearch.com/news/Deferred-Cleanup,-Checking-Errors,-and-Potential-Problems/
//
// Probably, most relevant for closing HTTP response bodies as they MUST be closed, even
// if you don’t read it. https://manishrjain.com/must-close-golang-http-response
//
// Usage:
// ```go
// res, err := client.Do(req)
// defer internal.CloseIO(res.Body, "request body")
// ```
//
// Alternative to this bulky pattern:
//
// ```go
// res, err := client.Do(req)
// defer func(c io.Closer) {
// 	if c != nil {
// 		err := c.Close()
// 		if err != nil {
// 			log.Fatalf("error closing request body stream %v", err)
// 		}
// 	}
// }(res.Body)
// ```
func CloseIO(c io.Closer, contextString string) {
	if c != nil {
		err := c.Close()
		if err != nil {
			// In most cases, not much we can do besides logging as we already received and
			// handled whatever resource this io.Closer was wrapping.
			log.Fatalf("error closing io.Closer (%s): %v", contextString, err)
		}
	}
}
