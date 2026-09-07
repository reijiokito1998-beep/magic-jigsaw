package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/google/uuid"

	"github.com/reijiokito/jigsaw-backend/internal/httpx"
	"github.com/reijiokito/jigsaw-backend/internal/models"
	"github.com/reijiokito/jigsaw-backend/internal/repository"
)

type libraryResponse struct {
	Images []models.Image `json:"images"`
	pageMeta
}

// handleAdminListLibrary lists library images (newest first), filtered by ?q=.
//
// @Summary      List image library (admin)
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Param        q      query  string  false  "Search by name"
// @Param        page   query  int  false  "Page number (1-based); needs limit or defaults to 8"
// @Param        limit  query  int  false  "Rows per page; omit for the whole list"
// @Success      200  {object}  libraryResponse
// @Router       /api/v1/admin/library [get]
func (s *Server) handleAdminListLibrary(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	// The search term is applied in SQL, so paging works on matches rather than
	// on a window the client then filters down to nothing.
	page := httpx.ParsePage(r, maxPageLimit)
	log.Printf("[DEBUG] handleAdminListLibrary: start q=%q page=%d limit=%d adminID=%s",
		q, page.Number, page.Limit, httpx.UserIDFrom(r.Context()))
	imgs, total, err := s.store.ListLibraryImages(r.Context(), q, page.Limit, page.Offset)
	if err != nil {
		log.Printf("[DEBUG] handleAdminListLibrary: error ListLibraryImages: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load library")
		return
	}
	log.Printf("[DEBUG] handleAdminListLibrary: success count=%d total=%d", len(imgs), total)
	httpx.JSON(w, http.StatusOK, libraryResponse{Images: s.decorateImages(imgs), pageMeta: metaFor(page, total)})
}

// handleAdminUploadLibrary uploads an image to the reusable library.
//
// @Summary      Upload library image (admin)
// @Tags         admin
// @Accept       multipart/form-data
// @Produce      json
// @Security     BearerAuth
// @Param        file   formData  file    true   "Image (max 15MB)"
// @Param        title  formData  string  false  "Image name (defaults to filename)"
// @Success      201  {object}  models.Image
// @Failure      400  {object}  httpx.ErrorBody
// @Router       /api/v1/admin/library [post]
func (s *Server) handleAdminUploadLibrary(w http.ResponseWriter, r *http.Request) {
	adminID := httpx.UserIDFrom(r.Context())
	log.Printf("[DEBUG] handleAdminUploadLibrary: start adminID=%s", adminID)
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		log.Printf("[DEBUG] handleAdminUploadLibrary: error ParseMultipartForm: %v", err)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid multipart form")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		log.Printf("[DEBUG] handleAdminUploadLibrary: error missing file field: %v", err)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "missing file field")
		return
	}
	defer file.Close()
	if header.Size > maxUploadBytes {
		log.Printf("[DEBUG] handleAdminUploadLibrary: error file too large size=%d", header.Size)
		httpx.Error(w, http.StatusRequestEntityTooLarge, "too_large", "file exceeds 15MB limit")
		return
	}
	contentType, err := sniffImageType(file)
	if err != nil {
		log.Printf("[DEBUG] handleAdminUploadLibrary: rejected non-image filename=%q: %v", header.Filename, err)
		httpx.Error(w, http.StatusBadRequest, "not_an_image", "file must be an image ("+acceptedImageFormats+")")
		return
	}
	uploaded, err := s.cloud.Upload(r.Context(), file)
	if err != nil {
		log.Printf("[DEBUG] handleAdminUploadLibrary: error Cloudinary upload: %v", err)
		httpx.Error(w, http.StatusBadGateway, "upload_failed", "could not upload image")
		return
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
		PublicID:   uploaded.PublicID,
		URL:        uploaded.URL,
		Width:      uploaded.Width,
		Height:     uploaded.Height,
		Title:      title,
		UploadedBy: uploadedBy,
		IsLibrary:  true,
	})
	if err != nil {
		log.Printf("[DEBUG] handleAdminUploadLibrary: error CreateImage: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not save image")
		return
	}
	log.Printf("[DEBUG] handleAdminUploadLibrary: success imageID=%s title=%q type=%s", img.ID, title, contentType)
	httpx.JSON(w, http.StatusCreated, img)
}

// libraryBatchResponse is the response body for the batch library upload
// endpoint.
type libraryBatchResponse struct {
	Images []models.Image         `json:"images"`
	Failed []libraryUploadFailure `json:"failed"`
}

// libraryUploadFailure describes a single file that failed to upload as
// part of a batch request.
type libraryUploadFailure struct {
	Filename string `json:"filename"`
	Error    string `json:"error"`
}

// handleAdminUploadLibraryBatch uploads multiple images to the reusable
// library in a single request. Per-file failures do not abort the batch.
//
// @Summary      Upload library images in batch (admin)
// @Tags         admin
// @Accept       multipart/form-data
// @Produce      json
// @Security     BearerAuth
// @Param        files  formData  file  true  "Images (multiple, max 15MB each, 100MB total)"
// @Success      200  {object}  libraryBatchResponse
// @Failure      400  {object}  httpx.ErrorBody
// @Router       /api/v1/admin/library/batch [post]
func (s *Server) handleAdminUploadLibraryBatch(w http.ResponseWriter, r *http.Request) {
	adminID := httpx.UserIDFrom(r.Context())
	log.Printf("[DEBUG] handleAdminUploadLibraryBatch: start adminID=%s", adminID)
	if err := r.ParseMultipartForm(maxLibraryBatchBytes); err != nil {
		log.Printf("[DEBUG] handleAdminUploadLibraryBatch: error ParseMultipartForm: %v", err)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid multipart form")
		return
	}
	headers := r.MultipartForm.File["files"]
	if len(headers) == 0 {
		log.Printf("[DEBUG] handleAdminUploadLibraryBatch: error no files provided")
		httpx.Error(w, http.StatusBadRequest, "bad_request", "no files provided")
		return
	}

	uploadedBy := uuid.NullUUID{}
	if adminID != uuid.Nil {
		uploadedBy = uuid.NullUUID{UUID: adminID, Valid: true}
	}

	resp := libraryBatchResponse{
		Images: []models.Image{},
		Failed: []libraryUploadFailure{},
	}

	for _, header := range headers {
		func() {
			if header.Size > maxUploadBytes {
				log.Printf("[DEBUG] handleAdminUploadLibraryBatch: error file too large filename=%q size=%d", header.Filename, header.Size)
				resp.Failed = append(resp.Failed, libraryUploadFailure{
					Filename: header.Filename,
					Error:    "file exceeds 15MB limit",
				})
				return
			}
			file, err := header.Open()
			if err != nil {
				log.Printf("[DEBUG] handleAdminUploadLibraryBatch: error opening file filename=%q: %v", header.Filename, err)
				resp.Failed = append(resp.Failed, libraryUploadFailure{
					Filename: header.Filename,
					Error:    "could not read file",
				})
				return
			}
			defer file.Close()

			if _, err := sniffImageType(file); err != nil {
				log.Printf("[DEBUG] handleAdminUploadLibraryBatch: rejected non-image filename=%q: %v", header.Filename, err)
				resp.Failed = append(resp.Failed, libraryUploadFailure{
					Filename: header.Filename,
					Error:    "file must be an image (" + acceptedImageFormats + ")",
				})
				return
			}

			uploaded, err := s.cloud.Upload(r.Context(), file)
			if err != nil {
				log.Printf("[DEBUG] handleAdminUploadLibraryBatch: error Cloudinary upload filename=%q: %v", header.Filename, err)
				resp.Failed = append(resp.Failed, libraryUploadFailure{
					Filename: header.Filename,
					Error:    "could not upload image",
				})
				return
			}
			img, err := s.store.CreateImage(r.Context(), repository.CreateImageParams{
				PublicID:   uploaded.PublicID,
				URL:        uploaded.URL,
				Width:      uploaded.Width,
				Height:     uploaded.Height,
				Title:      header.Filename,
				UploadedBy: uploadedBy,
				IsLibrary:  true,
			})
			if err != nil {
				log.Printf("[DEBUG] handleAdminUploadLibraryBatch: error CreateImage filename=%q: %v", header.Filename, err)
				resp.Failed = append(resp.Failed, libraryUploadFailure{
					Filename: header.Filename,
					Error:    "could not save image",
				})
				return
			}
			log.Printf("[DEBUG] handleAdminUploadLibraryBatch: success imageID=%s filename=%q", img.ID, header.Filename)
			resp.Images = append(resp.Images, img)
		}()
	}

	log.Printf("[DEBUG] handleAdminUploadLibraryBatch: done uploaded=%d failed=%d", len(resp.Images), len(resp.Failed))
	httpx.JSON(w, http.StatusOK, resp)
}

// handleAdminRenameLibrary renames a library image.
//
// @Summary      Rename library image (admin)
// @Tags         admin
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id       path  string  true  "Image id (UUID)"
// @Param        request  body  object  true  "New title {title}"
// @Success      200  {object}  map[string]bool
// @Failure      404  {object}  httpx.ErrorBody
// @Router       /api/v1/admin/library/{id} [patch]
func (s *Server) handleAdminRenameLibrary(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		log.Printf("[DEBUG] handleAdminRenameLibrary: error invalid image id %q: %v", r.PathValue("id"), err)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid image id")
		return
	}
	log.Printf("[DEBUG] handleAdminRenameLibrary: start imageID=%s adminID=%s", id, httpx.UserIDFrom(r.Context()))
	var body struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		log.Printf("[DEBUG] handleAdminRenameLibrary: error decoding request body imageID=%s: %v", id, err)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if err := s.store.SetImageTitle(r.Context(), id, body.Title); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			log.Printf("[DEBUG] handleAdminRenameLibrary: image not found imageID=%s", id)
			httpx.Error(w, http.StatusNotFound, "not_found", "image not found")
			return
		}
		log.Printf("[DEBUG] handleAdminRenameLibrary: error SetImageTitle imageID=%s: %v", id, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not rename")
		return
	}
	log.Printf("[DEBUG] handleAdminRenameLibrary: success imageID=%s newTitle=%q", id, body.Title)
	httpx.JSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleAdminDeleteLibrary removes an image from the library (games that
// already use it keep working).
//
// @Summary      Remove library image (admin)
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Param        id  path  string  true  "Image id (UUID)"
// @Success      200  {object}  map[string]bool
// @Failure      404  {object}  httpx.ErrorBody
// @Router       /api/v1/admin/library/{id} [delete]
func (s *Server) handleAdminDeleteLibrary(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		log.Printf("[DEBUG] handleAdminDeleteLibrary: error invalid image id %q: %v", r.PathValue("id"), err)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid image id")
		return
	}
	log.Printf("[DEBUG] handleAdminDeleteLibrary: start imageID=%s adminID=%s", id, httpx.UserIDFrom(r.Context()))
	if err := s.store.SetImageLibrary(r.Context(), id, false); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			log.Printf("[DEBUG] handleAdminDeleteLibrary: image not found imageID=%s", id)
			httpx.Error(w, http.StatusNotFound, "not_found", "image not found")
			return
		}
		log.Printf("[DEBUG] handleAdminDeleteLibrary: error SetImageLibrary imageID=%s: %v", id, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not remove")
		return
	}
	log.Printf("[DEBUG] handleAdminDeleteLibrary: success imageID=%s", id)
	httpx.JSON(w, http.StatusOK, map[string]bool{"ok": true})
}
