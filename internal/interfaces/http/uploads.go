package http

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/iBlog/iblog-monolith-go/internal/application"
	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/internal/infrastructure/images"
)

const maxUpload = 5 << 20 // 5 MB

// Allowed image types, detected from content (not the client's header).
var imageExt = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

type uploaded struct {
	URL          string `json:"url"`
	ThumbnailURL string `json:"thumbnail_url"` // ready a moment after the upload
	// Variants are WebP copies by width (320, 640, 1280; never upscaled),
	// ready with the thumbnail. SrcSet is them as an <img srcset> value.
	Variants map[string]string `json:"variants"`
	SrcSet   string            `json:"srcset"`
}

func newUploaded(key string) uploaded {
	u := uploaded{URL: "/api/uploads/" + key, ThumbnailURL: "/api/uploads/" + application.ThumbKey(key), Variants: map[string]string{}}
	var set []string
	for _, w := range application.VariantWidths {
		url := "/api/uploads/" + application.VariantKey(key, w)
		u.Variants[strconv.Itoa(w)] = url
		set = append(set, url+" "+strconv.Itoa(w)+"w")
	}
	u.SrcSet = strings.Join(set, ", ")
	return u
}

// upload stores an image in MinIO and queues a JPEG thumbnail and WebP
// variants (320, 640, 1280 wide) for responsive images.
//
// Multipart field "file". JPEG, PNG, GIF or WebP up to 5 MB, detected from
// content. Use url as cover_url or avatar_url, and srcset in <img srcset>.
// Rate limit: 10 per minute.
// Auth: upload.create.
//
// spector:tags uploads
func (h *Handler) upload(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireUser(w, r); !ok || !h.allow(w, r, "upload", 10, time.Minute) {
		return
	}
	if err := h.Blog.Authz.Can(r.Context(), "upload", "create", 0, 0); err != nil {
		respond(w, r, 0, nil, err, "")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload+1<<10)
	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "file required (max 5 MB)")
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxUpload+1))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "file required (max 5 MB)")
		return
	}
	if len(data) > maxUpload {
		writeError(w, r, http.StatusBadRequest, "file too large (max 5 MB)")
		return
	}
	ctype := http.DetectContentType(data)
	ext, ok := imageExt[ctype]
	if !ok {
		writeError(w, r, http.StatusBadRequest, "only jpeg, png, gif, webp images")
		return
	}
	if err := images.Validate(data); err != nil {
		writeError(w, r, http.StatusBadRequest, "not a valid image")
		return
	}
	name := make([]byte, 16)
	if _, err := rand.Read(name); err != nil {
		respond(w, r, 0, nil, err, "")
		return
	}
	key := hex.EncodeToString(name) + ext
	if err := h.Storage.Put(r.Context(), key, bytes.NewReader(data), int64(len(data)), ctype); err != nil {
		respond(w, r, 0, nil, err, "")
		return
	}
	if err := h.Jobs.Thumbnail(r.Context(), key); err != nil {
		// The original is stored; only derived sizes are missing. Clients fall
		// back to the original, so do not fail (and orphan) the upload.
		zap.L().Warn("thumbnail enqueue", zap.Any("ctx", r.Context()), zap.String("key", key), zap.Error(err))
	}
	writeJSON(w, http.StatusCreated, newUploaded(key))
}

// download serves an uploaded image or its thumbnail from MinIO.
//
// Auth: none.
//
// spector:tags uploads
func (h *Handler) download(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if key == "" || strings.Contains(key, "..") {
		writeError(w, r, http.StatusNotFound, "not found")
		return
	}
	obj, err := h.Storage.Get(r.Context(), key)
	if errors.Is(err, domain.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		respond(w, r, 0, nil, err, "")
		return
	}
	defer obj.Body.Close()
	w.Header().Set("Content-Type", obj.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(obj.Size, 10))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	if _, err := io.Copy(w, obj.Body); err != nil {
		zap.L().Debug("download aborted", zap.String("key", key), zap.Error(err))
	}
}
