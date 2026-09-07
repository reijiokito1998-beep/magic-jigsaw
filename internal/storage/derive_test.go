package storage

import (
	"strings"
	"testing"

	"github.com/cloudinary/cloudinary-go/v2"
)

func TestParseDeliveryURL(t *testing.T) {
	tests := []struct {
		name         string
		raw          string
		wantPublicID string
		wantType     string
		wantVersion  int
		wantSigned   bool
		wantOK       bool
	}{
		{
			name:         "signed authenticated asset",
			raw:          "https://res.cloudinary.com/demo/image/authenticated/s--Ab3_dEf1--/v1712345678/jigsaw/cat.jpg",
			wantPublicID: "jigsaw/cat.jpg",
			wantType:     "authenticated",
			wantVersion:  1712345678,
			wantSigned:   true,
			wantOK:       true,
		},
		{
			name:         "plain upload",
			raw:          "https://res.cloudinary.com/demo/image/upload/v1712345678/jigsaw/cat.jpg",
			wantPublicID: "jigsaw/cat.jpg",
			wantType:     "upload",
			wantVersion:  1712345678,
			wantOK:       true,
		},
		{
			name:         "no version",
			raw:          "https://res.cloudinary.com/demo/image/upload/jigsaw/cat.jpg",
			wantPublicID: "jigsaw/cat.jpg",
			wantType:     "upload",
			wantOK:       true,
		},
		{
			name:         "nested folders stay part of the public id",
			raw:          "https://res.cloudinary.com/demo/image/upload/v1/a/b/c/cat.png",
			wantPublicID: "a/b/c/cat.png",
			wantType:     "upload",
			wantVersion:  1,
			wantOK:       true,
		},
		{name: "not cloudinary", raw: "https://cdn.example.com/image/upload/v1/cat.jpg"},
		{name: "not a delivery url", raw: "https://res.cloudinary.com/demo/image/upload"},
		{name: "garbage", raw: "::not a url::"},
		{name: "empty", raw: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			id, dt, ver, signed, ok := parseDeliveryURL(tc.raw)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if id != tc.wantPublicID {
				t.Errorf("publicID = %q, want %q", id, tc.wantPublicID)
			}
			if dt != tc.wantType {
				t.Errorf("deliveryType = %q, want %q", dt, tc.wantType)
			}
			if ver != tc.wantVersion {
				t.Errorf("version = %d, want %d", ver, tc.wantVersion)
			}
			if signed != tc.wantSigned {
				t.Errorf("signed = %v, want %v", signed, tc.wantSigned)
			}
		})
	}
}

func TestVariantTransformationString(t *testing.T) {
	if got, want := ThumbVariant.transformationString(), "f_auto,q_auto,w_400,c_limit"; got != want {
		t.Errorf("thumb = %q, want %q", got, want)
	}
	if got, want := CoverVariant.transformationString(), "f_auto,q_auto,w_800,c_limit"; got != want {
		t.Errorf("cover = %q, want %q", got, want)
	}
}

// A nil client must not panic: handlers decorate responses unconditionally,
// and a deployment without Cloudinary configured still has to serve them.
func TestDeriveNilClient(t *testing.T) {
	var c *Cloudinary
	const raw = "https://res.cloudinary.com/demo/image/upload/v1/cat.jpg"
	if got := c.Derive(raw, ThumbVariant); got != raw {
		t.Errorf("Derive on nil client = %q, want the url unchanged", got)
	}
}

// Derive has to produce a URL Cloudinary will actually serve: the
// transformation goes between the delivery type and the version, and a signed
// asset keeps a (freshly computed) signature.
func TestDeriveBuildsTransformedURL(t *testing.T) {
	cld, err := cloudinary.NewFromParams("demo", "123456789012345", "abcdefghijklmnopqrstuvwxyz1")
	if err != nil {
		t.Fatalf("new cloudinary: %v", err)
	}
	c := &Cloudinary{cld: cld, folder: "jigsaw"}

	t.Run("plain upload", func(t *testing.T) {
		got := c.Derive("https://res.cloudinary.com/demo/image/upload/v1712345678/jigsaw/cat.jpg", ThumbVariant)
		want := "https://res.cloudinary.com/demo/image/upload/f_auto,q_auto,w_400,c_limit/v1712345678/jigsaw/cat.jpg"
		if got != want {
			t.Errorf("got  %q\nwant %q", got, want)
		}
	})

	t.Run("signed authenticated asset is re-signed", func(t *testing.T) {
		const raw = "https://res.cloudinary.com/demo/image/authenticated/s--Ab3_dEf1--/v1712345678/jigsaw/cat.jpg"
		got := c.Derive(raw, ThumbVariant)
		if !strings.Contains(got, "/image/authenticated/s--") {
			t.Fatalf("lost the signature: %q", got)
		}
		if !strings.Contains(got, "f_auto,q_auto,w_400,c_limit") {
			t.Fatalf("lost the transformation: %q", got)
		}
		if !strings.Contains(got, "/v1712345678/jigsaw/cat.jpg") {
			t.Fatalf("lost the asset: %q", got)
		}
		if strings.Contains(got, "s--Ab3_dEf1--") {
			t.Fatalf("kept the old signature, which no longer covers the transformation: %q", got)
		}
	})

	t.Run("unknown url passes through", func(t *testing.T) {
		const raw = "https://cdn.example.com/cat.jpg"
		if got := c.Derive(raw, ThumbVariant); got != raw {
			t.Errorf("got %q, want it unchanged", got)
		}
	})
}
