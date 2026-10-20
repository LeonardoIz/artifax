package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/LeonardoIz/artifax/internal/docs"
	"github.com/LeonardoIz/artifax/internal/media"
	"github.com/LeonardoIz/artifax/internal/store"
)

// docJSON is the API representation of a document.
type docJSON struct {
	ID             string    `json:"id"`
	URL            string    `json:"url"`
	Title          string    `json:"title"`
	Description    string    `json:"description"`
	Visibility     string    `json:"visibility"`
	ByteSize       int64     `json:"byteSize"`
	SHA256         string    `json:"sha256"`
	SourceFilename string    `json:"sourceFilename"`
	ContentType    string    `json:"contentType"`
	Kind           string    `json:"kind"`
	CreatedBy      string    `json:"createdBy"`
	UpdatedBy      string    `json:"updatedBy"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

func (s *Server) toJSON(d *store.Doc) docJSON {
	return docJSON{
		ID:             d.ID,
		URL:            s.docURL(d.ID),
		Title:          d.Title,
		Description:    d.Description,
		Visibility:     string(d.Visibility),
		ByteSize:       d.ByteSize,
		SHA256:         d.BlobSHA256,
		SourceFilename: d.SourceFilename,
		ContentType:    d.ContentType,
		Kind:           string(media.Classify(d.ContentType)),
		CreatedBy:      d.CreatedBy,
		UpdatedBy:      d.UpdatedBy,
		CreatedAt:      d.CreatedAt,
		UpdatedAt:      d.UpdatedAt,
	}
}

func (s *Server) docURL(id string) string { return s.cfg.URL("/d/" + id) }

func (s *Server) apiCreate(w http.ResponseWriter, r *http.Request) {
	up, done, err := s.readUpload(w, r)
	defer done()
	if err != nil {
		s.writeError(w, err)
		return
	}
	d, err := s.docs.Create(r.Context(), up, actorFrom(r.Context()))
	if err != nil {
		s.writeError(w, err)
		return
	}
	w.Header().Set("Location", "/api/docs/"+d.ID)
	writeJSON(w, http.StatusCreated, s.toJSON(d))
}

func (s *Server) apiReplace(w http.ResponseWriter, r *http.Request) {
	// Check first so an unknown id fails fast instead of after reading the whole body.
	if _, err := s.docs.Get(r.Context(), r.PathValue("id")); err != nil {
		s.writeError(w, err)
		return
	}
	up, done, err := s.readUpload(w, r)
	defer done()
	if err != nil {
		s.writeError(w, err)
		return
	}
	d, err := s.docs.Replace(r.Context(), r.PathValue("id"), up, actorFrom(r.Context()))
	if err != nil {
		s.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.toJSON(d))
}

func (s *Server) apiGet(w http.ResponseWriter, r *http.Request) {
	d, err := s.docs.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.toJSON(d))
}

func (s *Server) apiList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p := store.ListParams{Query: q.Get("q"), Cursor: q.Get("cursor"), Limit: 50}
	if v := q.Get("visibility"); v != "" {
		vis := store.Visibility(v)
		if !vis.Valid() {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "visibility must be public, unlisted or private")
			return
		}
		p.Visibilities = []store.Visibility{vis}
	}
	if l := q.Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 1 || n > 100 {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "limit must be an integer from 1 to 100")
			return
		}
		p.Limit = n
	}
	list, next, err := s.docs.List(r.Context(), p)
	if err != nil {
		s.writeError(w, err)
		return
	}
	items := make([]docJSON, len(list))
	for i := range list {
		items[i] = s.toJSON(&list[i])
	}
	resp := struct {
		Items      []docJSON `json:"items"`
		NextCursor *string   `json:"nextCursor"`
	}{Items: items}
	if next != "" {
		resp.NextCursor = &next
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) apiUpdate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title       *string `json:"title"`
		Description *string `json:"description"`
		Visibility  *string `json:"visibility"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad Request", "body must be a JSON object with title, description and/or visibility")
		return
	}
	f := docs.Fields{Title: body.Title, Description: body.Description}
	if body.Visibility != nil {
		v := store.Visibility(*body.Visibility)
		f.Visibility = &v
	}
	d, err := s.docs.Update(r.Context(), r.PathValue("id"), f, actorFrom(r.Context()))
	if err != nil {
		s.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.toJSON(d))
}

func (s *Server) apiDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.docs.Delete(r.Context(), r.PathValue("id")); err != nil {
		s.writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) apiRaw(w http.ResponseWriter, r *http.Request) {
	d, err := s.docs.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.serveContent(w, r, d, true) // original bytes and type, never rendered
}

// readUpload accepts either multipart/form-data (field "file" plus metadata fields)
// or a raw body with metadata in the query string. The content is not read here:
// Upload.Body streams it to the blob store. Call done when the upload is handled.
func (s *Server) readUpload(w http.ResponseWriter, r *http.Request) (up docs.Upload, done func(), err error) {
	done = func() {}
	limit := s.docs.MaxBytes() + 64<<10
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	// Large uploads over slow links outlast the server's ReadTimeout: allow 2 minutes
	// plus 1 per 5 MB of the upload limit, so the body still cannot trickle forever.
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(uploadTimeout(s.docs.MaxBytes())))

	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	var meta url.Values
	if mediaType == "multipart/form-data" {
		// Parts beyond 1 MB go to temporary files, which net/http removes afterwards.
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			return up, done, bodyError(err)
		}
		file, hdr, err := r.FormFile("file")
		if err != nil {
			return up, done, &docs.ValidationError{Msg: `multipart field "file" is required`}
		}
		done = func() { file.Close() }
		up.Body = bodyReader{file}
		up.Filename = hdr.Filename
		up.ContentType = hdr.Header.Get("Content-Type")
		meta = r.MultipartForm.Value
	} else {
		up.Body = bodyReader{r.Body}
		meta = r.URL.Query()
		up.Filename = meta.Get("filename")
		up.ContentType = r.Header.Get("Content-Type")
	}

	up.Title = optionalValue(meta, "title")
	up.Description = optionalValue(meta, "description")
	if v := optionalValue(meta, "visibility"); v != nil && *v != "" {
		vis := store.Visibility(*v)
		up.Visibility = &vis
	}
	return up, done, nil
}

func uploadTimeout(maxBytes int64) time.Duration {
	return 2*time.Minute + time.Duration(maxBytes/(5<<20))*time.Minute
}

// bodyReader reports failures reading the request body as client errors.
type bodyReader struct{ r io.Reader }

func (b bodyReader) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	if err != nil && err != io.EOF {
		err = bodyError(err)
	}
	return n, err
}

func bodyError(err error) error {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return docs.ErrTooLarge
	}
	return &docs.ValidationError{Msg: "could not read request body"}
}

func optionalValue(v url.Values, key string) *string {
	vals, ok := v[key]
	if !ok || len(vals) == 0 {
		return nil
	}
	return &vals[0]
}

// writeError maps service errors to problem responses.
func (s *Server) writeError(w http.ResponseWriter, err error) {
	var verr *docs.ValidationError
	switch {
	case errors.As(err, &verr):
		writeProblem(w, http.StatusBadRequest, "Bad Request", verr.Msg)
	case errors.Is(err, docs.ErrTooLarge):
		writeProblem(w, http.StatusRequestEntityTooLarge, "Payload Too Large",
			fmt.Sprintf("artifacts are limited to %d MB", s.docs.MaxBytes()>>20))
	case errors.Is(err, docs.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "Not Found", "document not found")
	case errors.Is(err, store.ErrBadCursor):
		writeProblem(w, http.StatusBadRequest, "Bad Request", "invalid cursor")
	default:
		s.log.Error("api error", "err", err)
		writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "unexpected error")
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func writeProblem(w http.ResponseWriter, status int, title, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type":   "about:blank",
		"title":  title,
		"status": status,
		"detail": detail,
	})
}
