package api

import (
	"bytes"
	"errors"
	"io"
	"mime/multipart"
	"testing"
)

// fakeFile adapts a byte slice to multipart.File (Reader + ReaderAt + Seeker +
// Closer), which is what r.FormFile hands the upload handlers.
type fakeFile struct{ *bytes.Reader }

func (fakeFile) Close() error { return nil }

func newFakeFile(b []byte) multipart.File { return fakeFile{bytes.NewReader(b)} }

// Minimal but real magic-byte prefixes, padded so the payload is not just a
// header.
var (
	pngBytes  = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...)
	jpegBytes = append([]byte("\xff\xd8\xff\xe0"), make([]byte, 64)...)
	gifBytes  = append([]byte("GIF89a"), make([]byte, 64)...)
	// A real WebP always names its codec chunk after the "WEBP" form tag, and
	// that is what the sniffer keys on — "RIFF....WEBP" alone is not enough.
	webpBytes = append([]byte("RIFF\x00\x00\x00\x00WEBPVP8 "), make([]byte, 64)...)
	bmpBytes  = append([]byte("BM\x00\x00\x00\x00"), make([]byte, 64)...)
	heicBytes = append([]byte("\x00\x00\x00\x18ftypheic"), make([]byte, 64)...)
	avifBytes = append([]byte("\x00\x00\x00\x1cftypavif"), make([]byte, 64)...)
)

func TestSniffImageTypeAcceptsImageFormats(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"png", pngBytes, "image/png"},
		{"jpeg", jpegBytes, "image/jpeg"},
		{"gif", gifBytes, "image/gif"},
		{"webp", webpBytes, "image/webp"},
		{"bmp", bmpBytes, "image/bmp"},
		// Phones shoot these and Cloudinary accepts them, but Go's
		// DetectContentType does not recognise them.
		{"heic", heicBytes, "image/heic"},
		{"avif", avifBytes, "image/avif"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := sniffImageType(newFakeFile(tc.data))
			if err != nil {
				t.Fatalf("sniffImageType() error = %v, want nil", err)
			}
			if got != tc.want {
				t.Errorf("sniffImageType() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSniffImageTypeRejectsNonImages(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{"pdf", append([]byte("%PDF-1.7\n"), make([]byte, 64)...)},
		{"zip", append([]byte("PK\x03\x04"), make([]byte, 64)...)},
		{"elf_binary", append([]byte("\x7fELF"), make([]byte, 64)...)},
		{"shell_script", []byte("#!/bin/sh\nrm -rf /\n")},
		{"html", []byte("<!DOCTYPE html><html><body>hi</body></html>")},
		{"svg_with_script", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)},
		{"empty", nil},
		// The bytes decide, not the name: a .jpg holding a PDF is still a PDF.
		{"pdf_named_jpg", append([]byte("%PDF-1.7\n"), make([]byte, 64)...)},
		// "ftyp" alone is not enough — MP4 video shares the container.
		{"mp4_video", append([]byte("\x00\x00\x00\x18ftypmp42"), make([]byte, 64)...)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := sniffImageType(newFakeFile(tc.data)); !errors.Is(err, errNotImage) {
				t.Errorf("sniffImageType() error = %v, want errNotImage", err)
			}
		})
	}
}

// TestSniffImageTypeRewinds is the one that keeps uploads intact: the sniff
// consumes the header, so a file left un-rewound would reach Cloudinary with its
// first 512 bytes missing.
func TestSniffImageTypeRewinds(t *testing.T) {
	f := newFakeFile(pngBytes)
	if _, err := sniffImageType(f); err != nil {
		t.Fatalf("sniffImageType() error = %v", err)
	}
	rest, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("ReadAll after sniff: %v", err)
	}
	if !bytes.Equal(rest, pngBytes) {
		t.Errorf("read %d bytes after sniff, want the full %d", len(rest), len(pngBytes))
	}
}

// TestSniffImageTypeShortFile covers a file smaller than the 512-byte sniff
// window: io.ReadFull reports ErrUnexpectedEOF, which is not a failure.
func TestSniffImageTypeShortFile(t *testing.T) {
	got, err := sniffImageType(newFakeFile([]byte("GIF89a")))
	if err != nil {
		t.Fatalf("sniffImageType() error = %v, want nil", err)
	}
	if got != "image/gif" {
		t.Errorf("sniffImageType() = %q, want image/gif", got)
	}
}
