package api

import (
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
)

// Uploaded files are identified by their leading bytes, never by the filename
// extension or the Content-Type the client declares — both are attacker-chosen
// and neither says anything about what the bytes actually are. Cloudinary would
// reject a non-image anyway, but only after the whole file has crossed the
// network and been paid for, and only while that upload path stays in place.

// sniffLen is how many leading bytes are read to identify a file. 512 is what
// http.DetectContentType inspects; every signature below sits well inside it.
const sniffLen = 512

// errNotImage is returned by sniffImageType for anything that is not a
// recognised image.
var errNotImage = errors.New("file is not an image")

// allowedImageTypes are the formats the upload endpoints accept, limited to
// what http.DetectContentType recognises. The HEIC/AVIF family is handled
// separately by isoBMFFImageBrand.
var allowedImageTypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/gif":  true,
	"image/webp": true,
	"image/bmp":  true,
}

// acceptedImageFormats names the accepted formats for the client-facing error
// message.
const acceptedImageFormats = "JPEG, PNG, GIF, WebP, BMP, HEIC or AVIF"

// sniffImageType identifies f from its magic bytes and returns the detected
// content type, or errNotImage when the bytes are not a supported image.
//
// f is rewound before returning, so the caller still uploads the whole file —
// including on the error path, where the caller may want to keep reading.
func sniffImageType(f multipart.File) (string, error) {
	head := make([]byte, sniffLen)
	n, err := io.ReadFull(f, head)
	// Short files are fine: a valid GIF header is 6 bytes. Only a genuine read
	// failure is an error.
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", fmt.Errorf("read file header: %w", err)
	}
	head = head[:n]

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("rewind file: %w", err)
	}

	ct := http.DetectContentType(head)
	if allowedImageTypes[ct] {
		return ct, nil
	}
	if brand, ok := isoBMFFImageBrand(head); ok {
		return brand, nil
	}
	return "", fmt.Errorf("%w: detected %s", errNotImage, ct)
}

// isoBMFFImageBrand recognises the HEIC/HEIF/AVIF family, which phones produce
// and Cloudinary accepts but http.DetectContentType does not know. The layout is
// a 4-byte box size, the literal "ftyp", then the 4-byte major brand.
func isoBMFFImageBrand(head []byte) (string, bool) {
	if len(head) < 12 || string(head[4:8]) != "ftyp" {
		return "", false
	}
	switch string(head[8:12]) {
	case "heic", "heix", "heim", "heis", "hevc", "hevx", "hevm", "hevs", "mif1", "msf1":
		return "image/heic", true
	case "avif", "avis":
		return "image/avif", true
	}
	return "", false
}
