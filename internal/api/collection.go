package api

import (
	"errors"
	"log"
	"net/http"

	"github.com/google/uuid"

	"github.com/reijiokito/jigsaw-backend/internal/httpx"
	"github.com/reijiokito/jigsaw-backend/internal/models"
	"github.com/reijiokito/jigsaw-backend/internal/repository"
)

const maxUploadBytes = 15 << 20 // 15 MB

// maxLibraryBatchBytes is the total request-size cap for the multi-file
// library upload endpoint (individual files are still capped at
// maxUploadBytes each).
const maxLibraryBatchBytes = 100 << 20 // 100 MB

// handleListCollection returns the user's saved images.
//
// @Summary      List collection
// @Description  Returns the authenticated user's saved images.
// @Tags         collection
// @Produce      json
// @Security     BearerAuth
// @Param        page   query  int  false  "Page number (1-based); needs limit or defaults to 8"
// @Param        limit  query  int  false  "Rows per page; omit for the whole list"
// @Success      200  {object}  collectionResponse
// @Failure      401  {object}  httpx.ErrorBody
// @Failure      500  {object}  httpx.ErrorBody
// @Router       /api/v1/collection [get]
func (s *Server) handleListCollection(w http.ResponseWriter, r *http.Request) {
	uid := httpx.UserIDFrom(r.Context())
	page := httpx.ParsePage(r, maxPageLimit)
	log.Printf("[DEBUG] handleListCollection: start userID=%s page=%d limit=%d", uid, page.Number, page.Limit)
	items, total, err := s.store.ListCollection(r.Context(), uid, page.Limit, page.Offset)
	if err != nil {
		log.Printf("[DEBUG] handleListCollection: error ListCollection userID=%s: %v", uid, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load collection")
		return
	}
	log.Printf("[DEBUG] handleListCollection: success userID=%s count=%d total=%d", uid, len(items), total)
	httpx.JSON(w, http.StatusOK, collectionResponse{Items: s.decorateCollection(items), pageMeta: metaFor(page, total)})
}

// handleUploadCollection uploads a new image to Cloudinary and adds it to the
// user's collection.
//
// @Summary      Upload image to collection
// @Description  Uploads an image to Cloudinary and adds it to the user's collection.
// @Tags         collection
// @Accept       multipart/form-data
// @Produce      json
// @Security     BearerAuth
// @Param        file   formData  file    true   "Image file (max 15MB)"
// @Param        title  formData  string  false  "Optional title"
// @Success      201  {object}  models.Image
// @Failure      400  {object}  httpx.ErrorBody
// @Failure      401  {object}  httpx.ErrorBody
// @Failure      502  {object}  httpx.ErrorBody
// @Router       /api/v1/collection/upload [post]
func (s *Server) handleUploadCollection(w http.ResponseWriter, r *http.Request) {
	userID := httpx.UserIDFrom(r.Context())
	log.Printf("[DEBUG] handleUploadCollection: start userID=%s", userID)
	img, ok := s.uploadImageFromRequest(w, r, false)
	if !ok {
		log.Printf("[DEBUG] handleUploadCollection: uploadImageFromRequest failed userID=%s", userID)
		return
	}
	if err := s.store.AddToCollection(r.Context(), userID, img.ID); err != nil {
		log.Printf("[DEBUG] handleUploadCollection: error AddToCollection userID=%s imageID=%s: %v", userID, img.ID, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not add to collection")
		return
	}
	log.Printf("[DEBUG] handleUploadCollection: success userID=%s imageID=%s", userID, img.ID)
	httpx.JSON(w, http.StatusCreated, img)
}

type addToCollectionRequest struct {
	ImageID string `json:"imageId"`
}

// handleAddToCollection adds an existing image (e.g. a completed challenge
// image) to the user's collection so it can be replayed anytime.
//
// @Summary      Add existing image to collection
// @Description  Saves an already-stored image (e.g. a completed challenge) to the user's collection.
// @Tags         collection
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        request  body  addToCollectionRequest  true  "Image id"
// @Success      204  "No Content"
// @Failure      400  {object}  httpx.ErrorBody
// @Failure      401  {object}  httpx.ErrorBody
// @Failure      404  {object}  httpx.ErrorBody
// @Router       /api/v1/collection [post]
func (s *Server) handleAddToCollection(w http.ResponseWriter, r *http.Request) {
	userID := httpx.UserIDFrom(r.Context())
	var req addToCollectionRequest
	if !decodeJSON(w, r, &req) {
		log.Printf("[DEBUG] handleAddToCollection: error decoding request body userID=%s", userID)
		return
	}
	imageID, err := uuid.Parse(req.ImageID)
	if err != nil {
		log.Printf("[DEBUG] handleAddToCollection: error invalid imageId %q: %v", req.ImageID, err)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid imageId")
		return
	}
	log.Printf("[DEBUG] handleAddToCollection: start userID=%s imageID=%s", userID, imageID)

	if _, err := s.store.GetImage(r.Context(), imageID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			log.Printf("[DEBUG] handleAddToCollection: image not found imageID=%s", imageID)
			httpx.Error(w, http.StatusNotFound, "not_found", "image not found")
			return
		}
		log.Printf("[DEBUG] handleAddToCollection: error GetImage imageID=%s: %v", imageID, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load image")
		return
	}

	if err := s.store.AddToCollection(r.Context(), userID, imageID); err != nil {
		log.Printf("[DEBUG] handleAddToCollection: error AddToCollection userID=%s imageID=%s: %v", userID, imageID, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not add to collection")
		return
	}
	log.Printf("[DEBUG] handleAddToCollection: success userID=%s imageID=%s", userID, imageID)
	httpx.JSON(w, http.StatusNoContent, nil)
}

// handleRemoveFromCollection removes an image from the user's collection.
//
// @Summary      Remove image from collection
// @Tags         collection
// @Produce      json
// @Security     BearerAuth
// @Param        imageId  path  string  true  "Image id (UUID)"
// @Success      204  "No Content"
// @Failure      400  {object}  httpx.ErrorBody
// @Failure      401  {object}  httpx.ErrorBody
// @Router       /api/v1/collection/{imageId} [delete]
func (s *Server) handleRemoveFromCollection(w http.ResponseWriter, r *http.Request) {
	imageID, err := uuid.Parse(r.PathValue("imageId"))
	if err != nil {
		log.Printf("[DEBUG] handleRemoveFromCollection: error invalid imageId %q: %v", r.PathValue("imageId"), err)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid imageId")
		return
	}
	userID := httpx.UserIDFrom(r.Context())
	log.Printf("[DEBUG] handleRemoveFromCollection: start userID=%s imageID=%s", userID, imageID)
	if err := s.store.RemoveFromCollection(r.Context(), userID, imageID); err != nil {
		log.Printf("[DEBUG] handleRemoveFromCollection: error RemoveFromCollection userID=%s imageID=%s: %v", userID, imageID, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not remove from collection")
		return
	}
	log.Printf("[DEBUG] handleRemoveFromCollection: success userID=%s imageID=%s", userID, imageID)
	httpx.JSON(w, http.StatusNoContent, nil)
}

// uploadImageFromRequest parses a multipart "file" field, uploads it to
// Cloudinary, and persists an images row. On any error it writes the response
// and returns ok=false.
func (s *Server) uploadImageFromRequest(w http.ResponseWriter, r *http.Request, isChallenge bool) (models.Image, bool) {
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		log.Printf("[DEBUG] uploadImageFromRequest: error ParseMultipartForm: %v", err)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid multipart form")
		return models.Image{}, false
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		log.Printf("[DEBUG] uploadImageFromRequest: error missing file field: %v", err)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "missing file field")
		return models.Image{}, false
	}
	defer file.Close()

	if header.Size > maxUploadBytes {
		log.Printf("[DEBUG] uploadImageFromRequest: error file too large size=%d", header.Size)
		httpx.Error(w, http.StatusRequestEntityTooLarge, "too_large", "file exceeds 15MB limit")
		return models.Image{}, false
	}

	contentType, err := sniffImageType(file)
	if err != nil {
		log.Printf("[DEBUG] uploadImageFromRequest: rejected non-image filename=%q: %v", header.Filename, err)
		httpx.Error(w, http.StatusBadRequest, "not_an_image", "file must be an image ("+acceptedImageFormats+")")
		return models.Image{}, false
	}

	uploaded, err := s.cloud.Upload(r.Context(), file)
	if err != nil {
		log.Printf("[DEBUG] uploadImageFromRequest: error Cloudinary upload: %v", err)
		httpx.Error(w, http.StatusBadGateway, "upload_failed", "could not upload image")
		return models.Image{}, false
	}

	title := r.FormValue("title")
	if title == "" {
		title = header.Filename
	}

	uploadedBy := uuid.NullUUID{}
	if uid := httpx.UserIDFrom(r.Context()); uid != uuid.Nil {
		uploadedBy = uuid.NullUUID{UUID: uid, Valid: true}
	}

	img, err := s.store.CreateImage(r.Context(), repository.CreateImageParams{
		PublicID:    uploaded.PublicID,
		URL:         uploaded.URL,
		Width:       uploaded.Width,
		Height:      uploaded.Height,
		Title:       title,
		UploadedBy:  uploadedBy,
		IsChallenge: isChallenge,
	})
	if err != nil {
		log.Printf("[DEBUG] uploadImageFromRequest: error CreateImage: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not save image")
		return models.Image{}, false
	}
	log.Printf("[DEBUG] uploadImageFromRequest: success imageID=%s title=%q type=%s isChallenge=%t",
		img.ID, title, contentType, isChallenge)
	return img, true
}

// resolveImageID returns the image to use for a create endpoint: an existing
// library image referenced by the "imageId" form field, or (fallback) a freshly
// uploaded "file". Parses the multipart form.
func (s *Server) resolveImageID(w http.ResponseWriter, r *http.Request, isChallenge bool) (uuid.UUID, bool) {
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		log.Printf("[DEBUG] resolveImageID: error ParseMultipartForm: %v", err)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid multipart form")
		return uuid.Nil, false
	}
	if v := r.FormValue("imageId"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			log.Printf("[DEBUG] resolveImageID: error invalid imageId %q: %v", v, err)
			httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid imageId")
			return uuid.Nil, false
		}
		if _, err := s.store.GetImage(r.Context(), id); err != nil {
			log.Printf("[DEBUG] resolveImageID: image not found imageID=%s: %v", id, err)
			httpx.Error(w, http.StatusBadRequest, "bad_request", "image not found")
			return uuid.Nil, false
		}
		log.Printf("[DEBUG] resolveImageID: using existing library image imageID=%s", id)
		return id, true
	}
	img, ok := s.uploadImageFromRequest(w, r, isChallenge)
	if !ok {
		return uuid.Nil, false
	}
	return img.ID, true
}

// optionalImageID resolves an optional image for update endpoints: the imageId
// field, else an uploaded file, else "no change" (invalid NullUUID).
func (s *Server) optionalImageID(w http.ResponseWriter, r *http.Request, isChallenge bool) (uuid.NullUUID, bool) {
	if v := r.FormValue("imageId"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			log.Printf("[DEBUG] optionalImageID: error invalid imageId %q: %v", v, err)
			httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid imageId")
			return uuid.NullUUID{}, false
		}
		if _, err := s.store.GetImage(r.Context(), id); err != nil {
			log.Printf("[DEBUG] optionalImageID: image not found imageID=%s: %v", id, err)
			httpx.Error(w, http.StatusBadRequest, "bad_request", "image not found")
			return uuid.NullUUID{}, false
		}
		log.Printf("[DEBUG] optionalImageID: using existing library image imageID=%s", id)
		return uuid.NullUUID{UUID: id, Valid: true}, true
	}
	if _, _, ferr := r.FormFile("file"); ferr == nil {
		img, ok := s.uploadImageFromRequest(w, r, isChallenge)
		if !ok {
			return uuid.NullUUID{}, false
		}
		return uuid.NullUUID{UUID: img.ID, Valid: true}, true
	}
	log.Printf("[DEBUG] optionalImageID: no new image provided, keeping existing")
	return uuid.NullUUID{}, true // no change
}
