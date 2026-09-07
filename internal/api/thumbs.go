package api

import (
	"github.com/reijiokito/jigsaw-backend/internal/models"
	"github.com/reijiokito/jigsaw-backend/internal/storage"
)

// Thumbnails: every list response carries, next to each full-size image URL, a
// URL for the same picture rendered at the size the screen will paint it.
//
// This is where the bandwidth goes. A library grid of eight tiles used to pull
// eight originals — several megabytes each, straight off Cloudinary's metered
// bandwidth — to fill boxes about 110pt wide. The derived URL asks for a
// WebP/AVIF copy a few hundred pixels wide instead, which Cloudinary builds
// once and its CDN then serves to every other client for free.
//
// It has to happen server-side: uploads are stored with delivery type
// "authenticated", and those URLs carry a signature computed over the
// transformation, so a client cannot add one itself. See [storage.Cloudinary.Derive].

// thumb is shorthand for the list-row variant.
func (s *Server) thumb(url string) string {
	return s.cloud.Derive(url, storage.ThumbVariant)
}

// decorateImage fills in an image's thumbnail URL.
func (s *Server) decorateImage(img *models.Image) {
	img.ThumbURL = s.thumb(img.URL)
}

func (s *Server) decorateImages(imgs []models.Image) []models.Image {
	for i := range imgs {
		s.decorateImage(&imgs[i])
	}
	return imgs
}

func (s *Server) decorateCollection(items []models.CollectionItem) []models.CollectionItem {
	for i := range items {
		s.decorateImage(&items[i].Image)
	}
	return items
}

func (s *Server) decorateChallenges(list []models.DailyChallenge) []models.DailyChallenge {
	for i := range list {
		s.decorateImage(&list[i].Image)
	}
	return list
}

func (s *Server) decorateAdminChallenges(list []models.AdminChallengeView) []models.AdminChallengeView {
	for i := range list {
		s.decorateImage(&list[i].Image)
	}
	return list
}

func (s *Server) decorateStories(list []models.Story) []models.Story {
	for i := range list {
		list[i].CoverThumbURL = s.thumb(list[i].CoverURL)
	}
	return list
}

func (s *Server) decorateHiddenSecrets(list []models.HiddenSecret) []models.HiddenSecret {
	for i := range list {
		list[i].ThumbURL = s.thumb(list[i].ImageURL)
	}
	return list
}

func (s *Server) decorateAdminHiddenSecrets(list []models.AdminHiddenSecret) []models.AdminHiddenSecret {
	for i := range list {
		list[i].ThumbURL = s.thumb(list[i].ImageURL)
	}
	return list
}

func (s *Server) decorateHistory(list []models.HistoryItem) []models.HistoryItem {
	for i := range list {
		list[i].ThumbURL = s.thumb(list[i].ImageURL)
	}
	return list
}

func (s *Server) decorateExpeditionLevels(list []models.ExpeditionLevel) []models.ExpeditionLevel {
	for i := range list {
		list[i].ThumbURL = s.thumb(list[i].ImageURL)
	}
	return list
}
