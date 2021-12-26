package validate

import (
	"bytes"
	"fmt"
)

// TODO write more full PNG validator, this one is taken from sytest

var pngMagic = []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}
var chunkOfIHDR = []byte{0, 0, 0, 0x0D, 'I', 'H', 'D', 'R'}

// ValidatePng validates the bytes of a PNG blob
func ValidatePng(png []byte) error {
	if !bytes.HasPrefix(png, pngMagic) {
		return fmt.Errorf("PNG does not contain magic header")
	}

	if !bytes.HasPrefix(png[8:], chunkOfIHDR) {
		return fmt.Errorf("PNG does not contain IHDR chunk")
	}

	return nil
}
