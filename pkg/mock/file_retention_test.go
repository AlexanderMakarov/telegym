package mock

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func retentionRequest(srv *Server, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func retentionUpload(t *testing.T, srv *Server, token, payload string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if err := w.WriteField("chat_id", "42"); err != nil {
		t.Fatal(err)
	}
	part, err := w.CreateFormFile("photo", "photo.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/bot"+token+"/sendPhoto", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func retainedPhotoID(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var response struct {
		OK     bool    `json:"ok"`
		Result Message `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || !response.OK || len(response.Result.Photo) == 0 || response.Result.Photo[0].FileID == "" {
		t.Fatalf("upload status=%d body=%s", rec.Code, rec.Body.String())
	}
	return response.Result.Photo[0].FileID
}

func TestFileRetentionConfiguration(t *testing.T) {
	for _, limit := range []int64{0, 5, 200 * 1024 * 1024} {
		srv := New(Config{Quiet: true, FileStoreMaxBytes: limit})
		rec := retentionRequest(srv, http.MethodGet, "/debug/files")
		var stats struct {
			MaxBytes int64 `json:"max_bytes"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &stats); err != nil {
			t.Fatal(err)
		}
		want := limit
		if want == 0 {
			want = 100 * 1024 * 1024
		}
		if rec.Code != http.StatusOK || stats.MaxBytes != want {
			t.Fatalf("limit=%d status=%d body=%s, want max_bytes=%d", limit, rec.Code, rec.Body.String(), want)
		}
	}
}

func TestFileRetentionRejectsUploadWithoutEvicting(t *testing.T) {
	srv := New(Config{Quiet: true, FileStoreMaxBytes: 3})
	id := retainedPhotoID(t, retentionUpload(t, srv, "741:retention", "abc"))
	rec := retentionUpload(t, srv, "741:retention", "abcd")
	var response APIResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusRequestEntityTooLarge || response.OK || response.ErrorCode != http.StatusRequestEntityTooLarge ||
		!strings.Contains(response.Description, "file store capacity") {
		t.Fatalf("oversized upload status=%d body=%s, want 413", rec.Code, rec.Body.String())
	}
	rec = retentionRequest(srv, http.MethodGet, "/debug/files/"+id)
	if rec.Code != http.StatusOK || rec.Body.String() != "abc" {
		t.Fatalf("rejected upload evicted an existing file: status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = retentionRequest(srv, http.MethodGet, "/debug/messages/741:retention?chat_id=42")
	var messages struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &messages); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || messages.Count != 1 {
		t.Fatalf("rejected upload was recorded as a message: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestFileRetentionEvictsOldestAcrossBots(t *testing.T) {
	srv := New(Config{Quiet: true, FileStoreMaxBytes: 5})
	first := retainedPhotoID(t, retentionUpload(t, srv, "741:first", "abc"))
	second := retainedPhotoID(t, retentionUpload(t, srv, "742:second", "de"))
	third := retainedPhotoID(t, retentionUpload(t, srv, "742:second", "fgh"))
	if rec := retentionRequest(srv, http.MethodGet, "/debug/files/"+first); rec.Code != http.StatusNotFound {
		t.Fatalf("oldest upload status=%d, want 404", rec.Code)
	}
	for id, payload := range map[string]string{second: "de", third: "fgh"} {
		rec := retentionRequest(srv, http.MethodGet, "/debug/files/"+id)
		if rec.Code != http.StatusOK || rec.Body.String() != payload {
			t.Fatalf("retained upload %s status=%d body=%s", id, rec.Code, rec.Body.String())
		}
	}
	rec := retentionRequest(srv, http.MethodGet, "/debug/files")
	var stats struct {
		Count      int   `json:"count"`
		TotalBytes int64 `json:"total_bytes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &stats); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || stats.Count != 2 || stats.TotalBytes != 5 {
		t.Fatalf("unexpected retention stats: status=%d body=%s", rec.Code, rec.Body.String())
	}
}
