// Package storage wraps Cloudinary uploads.
package storage

import (
	"context"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/cloudinary/cloudinary-go/v2"
	"github.com/cloudinary/cloudinary-go/v2/api"
	"github.com/cloudinary/cloudinary-go/v2/api/uploader"

	"github.com/reijiokito/jigsaw-backend/internal/config"
	"github.com/reijiokito/jigsaw-backend/internal/metrics"
)

// UploadResult describes a stored image.
type UploadResult struct {
	PublicID string
	URL      string
	Width    int
	Height   int
}

// Cloudinary uploads images to a Cloudinary account.
type Cloudinary struct {
	cld    *cloudinary.Cloudinary
	folder string
}

// New builds a Cloudinary client from configuration.
func New(cfg *config.Config) (*Cloudinary, error) {
	var (
		cld *cloudinary.Cloudinary
		err error
	)
	if cfg.CloudinaryURL != "" {
		log.Printf("cloudinary: initializing client from CLOUDINARY_URL (folder=%s)", cfg.CloudinaryFolder)
		cld, err = cloudinary.NewFromURL(cfg.CloudinaryURL)
	} else {
		log.Printf("cloudinary: initializing client from params (cloud_name=%s, folder=%s)", cfg.CloudinaryCloudName, cfg.CloudinaryFolder)
		cld, err = cloudinary.NewFromParams(
			cfg.CloudinaryCloudName, cfg.CloudinaryAPIKey, cfg.CloudinaryAPISecret)
	}
	if err != nil {
		log.Printf("cloudinary: init failed: %v", err)
		return nil, fmt.Errorf("init cloudinary: %w", err)
	}
	log.Printf("cloudinary: client initialized successfully (folder=%s)", cfg.CloudinaryFolder)
	return &Cloudinary{cld: cld, folder: cfg.CloudinaryFolder}, nil
}

// Upload stores the image read from r and returns its hosted URL and metadata.
//
// Uploads are the slowest thing this service does and depend on a third party,
// so both latency and outcome are measured separately from the HTTP metrics —
// an upload can be slow while the handler still returns 200.
func (c *Cloudinary) Upload(ctx context.Context, r io.Reader) (UploadResult, error) {
	start := time.Now()
	defer func() { metrics.UploadDuration.WithLabelValues("image").Observe(time.Since(start).Seconds()) }()

	useFilename := false
	uniqueFilename := true
	log.Printf("cloudinary: uploading image to folder=%s", c.folder)
	resp, err := c.cld.Upload.Upload(ctx, r, uploader.UploadParams{
		Folder:         c.folder,
		ResourceType:   "image",
		UseFilename:    &useFilename,
		UniqueFilename: &uniqueFilename,
		Type:           api.Authenticated,
	})
	if err != nil {
		metrics.UploadsTotal.WithLabelValues("image", "failure").Inc()
		log.Printf("cloudinary: upload to folder=%s failed: %v", c.folder, err)
		return UploadResult{}, fmt.Errorf("cloudinary upload: %w", err)
	}
	if resp.SecureURL == "" {
		metrics.UploadsTotal.WithLabelValues("image", "failure").Inc()
		log.Printf("cloudinary: upload to folder=%s returned empty response (public_id=%s)", c.folder, resp.PublicID)
		return UploadResult{}, fmt.Errorf("cloudinary upload failed: empty response")
	}
	metrics.UploadsTotal.WithLabelValues("image", "success").Inc()
	log.Printf("cloudinary: upload succeeded (public_id=%s, width=%d, height=%d)", resp.PublicID, resp.Width, resp.Height)
	return UploadResult{
		PublicID: resp.PublicID,
		URL:      resp.SecureURL,
		Width:    resp.Width,
		Height:   resp.Height,
	}, nil
}
