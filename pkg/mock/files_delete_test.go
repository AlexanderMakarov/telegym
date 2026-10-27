package mock

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func fileRequest(srv *Server, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func assertFileStats(t *testing.T, srv *Server, wantCount int, wantBytes int64) {
	t.Helper()
	rec := fileRequest(srv, http.MethodGet, "/debug/files")
	var stats struct {
		Count      int   `json:"count"`
		TotalBytes int64 `json:"total_bytes"`
		MaxBytes   int64 `json:"max_bytes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &stats); err != nil {
		t.Fatalf("decode file stats: %v", err)
	}
	if rec.Code != http.StatusOK || stats.Count != wantCount || stats.TotalBytes != wantBytes || stats.MaxBytes != 100*1024*1024 {
		t.Fatalf("unexpected file stats: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDebugDeleteFile(t *testing.T) {
	srv := New(Config{Quiet: true})
	files := srv.Store().Files
	first := files.Put("photo", "image/jpeg", "one.jpg", []byte("one"))
	deleted := files.Put("video", "video/mp4", "two.mp4", []byte("two-two"))
	last := files.Put("photo", "image/jpeg", "three.jpg", []byte("three"))

	rec := fileRequest(srv, http.MethodDelete, "/debug/files/"+deleted)
	var response struct {
		OK     bool   `json:"ok"`
		FileID string `json:"file_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode delete response: %v", err)
	}
	if rec.Code != http.StatusOK || !response.OK || response.FileID != deleted {
		t.Fatalf("unexpected delete response: status=%d body=%s", rec.Code, rec.Body.String())
	}
	assertFileStats(t, srv, 2, 8)

	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		for _, id := range []string{deleted, "missing"} {
			rec := fileRequest(srv, method, "/debug/files/"+id)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("%s %s status=%d body=%s, want 404", method, id, rec.Code, rec.Body.String())
			}
		}
	}
	for id, want := range map[string]string{first: "one", last: "three"} {
		rec := fileRequest(srv, http.MethodGet, "/debug/files/"+id)
		if rec.Code != http.StatusOK || rec.Body.String() != want {
			t.Fatalf("remaining file %s status=%d body=%q, want %q", id, rec.Code, rec.Body.String(), want)
		}
	}
	assertFileStats(t, srv, 2, 8)
}

func TestDebugDeleteFiles(t *testing.T) {
	srv := New(Config{Quiet: true})
	files := srv.Store().Files
	first := files.Put("photo", "image/jpeg", "one.jpg", []byte("one"))
	second := files.Put("video", "video/mp4", "two.mp4", []byte("two-two"))

	for _, wantCount := range []int{2, 0} {
		rec := fileRequest(srv, http.MethodDelete, "/debug/files")
		var response struct {
			OK           bool  `json:"ok"`
			Deleted      int   `json:"deleted"`
			DeletedBytes int64 `json:"deleted_bytes"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode bulk delete response: %v", err)
		}
		wantBytes := int64(0)
		if wantCount != 0 {
			wantBytes = 10
		}
		if rec.Code != http.StatusOK || !response.OK || response.Deleted != wantCount || response.DeletedBytes != wantBytes {
			t.Fatalf("unexpected bulk delete response: status=%d body=%s", rec.Code, rec.Body.String())
		}
		assertFileStats(t, srv, 0, 0)
	}
	for _, id := range []string{first, second} {
		rec := fileRequest(srv, http.MethodGet, "/debug/files/"+id)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("cleared file %s status=%d body=%s, want 404", id, rec.Code, rec.Body.String())
		}
	}

	// Cleanup must allow new uploads without reusing a deleted file's ID.
	const token = "321:cleanup"
	sendTestPhotoUpload(t, srv, token, "image/jpeg", []byte("new photo"))
	messages := srv.Store().Bot(token).Messages(42, 1)
	if len(messages) != 1 || len(messages[0].Photo) == 0 {
		t.Fatalf("upload after cleanup lost its photo: %+v", messages)
	}
	id := messages[0].Photo[0].FileID
	if id == first || id == second {
		t.Fatalf("upload after cleanup reused deleted file ID %s", id)
	}
	rec := fileRequest(srv, http.MethodGet, "/debug/files/"+id)
	if rec.Code != http.StatusOK || rec.Body.String() != "new photo" {
		t.Fatalf("upload after cleanup status=%d body=%s", rec.Code, rec.Body.String())
	}
	assertFileStats(t, srv, 1, 9)
}
