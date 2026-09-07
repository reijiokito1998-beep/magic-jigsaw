package storage

import (
	"fmt"
	"log"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/cloudinary/cloudinary-go/v2/api"
	"github.com/cloudinary/cloudinary-go/v2/transformation"
)

// A Variant is a rendering of a stored image: how wide to deliver it and how
// to fit it into that width.
//
// Every list in the app shows pictures at a fraction of their uploaded size.
// Serving the original costs Cloudinary bandwidth for pixels that are thrown
// away the moment they are decoded, so each response carries a URL for the
// size the screen will actually paint.
type Variant struct {
	// Width in device pixels of the widest screen expected to show it.
	Width int
	// Crop is the Cloudinary crop mode. "limit" scales to fit without
	// upscaling and keeps the aspect ratio — the safe default, since the same
	// URL is reused by cards of slightly different shapes.
	Crop string
}

// The two sizes the app actually needs. Keeping the set this small matters:
// each distinct transformation is a separate derivation on Cloudinary's side
// and a separate CDN cache entry.
var (
	// ThumbVariant covers list rows, grid tiles and covers.
	ThumbVariant = Variant{Width: 400, Crop: "limit"}
	// CoverVariant covers full-width hero images and editor previews.
	CoverVariant = Variant{Width: 800, Crop: "limit"}
)

// transformationString renders the variant as a Cloudinary transformation.
//
// f_auto negotiates WebP/AVIF per device and q_auto picks the lowest quality
// Cloudinary's own analysis considers visually lossless — together usually a
// bigger saving than the resize itself.
func (v Variant) transformationString() string {
	return fmt.Sprintf("f_auto,q_auto,w_%d,c_%s", v.Width, v.Crop)
}

// signaturePattern matches the `s--AbCdEf12--` element Cloudinary puts in the
// URL of a signed (authenticated) asset.
var signaturePattern = regexp.MustCompile(`^s--[A-Za-z0-9_-]+--$`)

// versionPattern matches the `v1712345678` element.
var versionPattern = regexp.MustCompile(`^v[0-9]+$`)

// derivedURLs memoises Derive. The inputs are a small, fixed set (every image
// in the catalogue times two variants) and the output is deterministic, but
// building a signed URL runs a SHA-1 over the transformation — worth doing
// once per image rather than once per row of every list response.
var derivedURLs sync.Map // string -> string

// Derive returns a delivery URL for the same asset as rawURL, rendered at [v].
//
// Uploads are stored with delivery type "authenticated", whose URLs carry a
// signature computed over the transformation. A client therefore *cannot*
// paste a transformation into the URL itself — the signature would no longer
// match and Cloudinary would answer 401. That is why sizing has to happen
// here, where the API secret is: this re-signs correctly.
//
// Anything that is not a recognisable Cloudinary delivery URL comes back
// unchanged, so callers can pass any URL blindly.
func (c *Cloudinary) Derive(rawURL string, v Variant) string {
	if c == nil || rawURL == "" {
		return rawURL
	}
	cacheKey := rawURL + "|" + v.transformationString()
	if cached, ok := derivedURLs.Load(cacheKey); ok {
		return cached.(string)
	}

	publicID, deliveryType, version, signed, ok := parseDeliveryURL(rawURL)
	if !ok {
		return rawURL
	}

	asset, err := c.cld.Image(publicID)
	if err != nil {
		log.Printf("cloudinary: derive %s failed to build asset: %v", publicID, err)
		return rawURL
	}
	asset.DeliveryType = api.DeliveryType(deliveryType)
	asset.Transformation = transformation.RawTransformation(v.transformationString())
	asset.Version = version
	// Only re-sign what was signed to begin with: adding a signature to a
	// plain upload URL would break it just as surely as dropping one.
	asset.Config.URL.SignURL = signed
	// Drop the SDK's `?_a=` analytics token. It varies with the SDK version, so
	// leaving it on would change every URL in the catalogue on a library bump
	// and invalidate whatever the clients had cached.
	asset.Config.URL.Analytics = false

	out, err := asset.String()
	if err != nil || out == "" {
		log.Printf("cloudinary: derive %s failed to build URL: %v", publicID, err)
		return rawURL
	}
	derivedURLs.Store(cacheKey, out)
	return out
}

// parseDeliveryURL pulls the addressing parts out of a Cloudinary delivery URL:
//
//	https://res.cloudinary.com/<cloud>/image/authenticated/s--SIG--/v123/folder/pic.jpg
//	                                   ^asset ^delivery    ^sig     ^ver ^public id
//
// The public id keeps its extension, which is what the signature was computed
// over. ok is false for anything that is not shaped like one.
func parseDeliveryURL(raw string) (publicID, deliveryType string, version int, signed, ok bool) {
	u, err := url.Parse(raw)
	if err != nil || !strings.Contains(u.Host, "cloudinary.com") {
		return "", "", 0, false, false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")

	// Find the asset type ("image"); the delivery type follows it.
	at := -1
	for i, p := range parts {
		if p == "image" {
			at = i
			break
		}
	}
	if at < 0 || at+2 >= len(parts) {
		return "", "", 0, false, false
	}
	deliveryType = parts[at+1]
	rest := parts[at+2:]

	if signaturePattern.MatchString(rest[0]) {
		signed = true
		rest = rest[1:]
	}
	if len(rest) > 0 && versionPattern.MatchString(rest[0]) {
		version, _ = strconv.Atoi(strings.TrimPrefix(rest[0], "v"))
		rest = rest[1:]
	}
	if len(rest) == 0 {
		return "", "", 0, false, false
	}
	return strings.Join(rest, "/"), deliveryType, version, signed, true
}
