package mock

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSendMediaUploadSizeLimit(t *testing.T) {
	const maxFile = 50 * 1024 * 1024
	for _, tc := range []struct {
		name   string
		size   int
		status int
	}{
		{name: "at limit", size: maxFile, status: http.StatusOK},
		{name: "over limit", size: maxFile + 1, status: http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := New(Config{Quiet: true})
			data := make([]byte, tc.size)
			var body bytes.Buffer
			w := multipart.NewWriter(&body)
			if err := w.WriteField("chat_id", "42"); err != nil {
				t.Fatal(err)
			}
			part, err := w.CreateFormFile("photo", "large.jpg")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.Copy(part, bytes.NewReader(data)); err != nil {
				t.Fatal(err)
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/bot741:upload/sendPhoto", &body)
			req.Header.Set("Content-Type", w.FormDataContentType())
			t.Cleanup(func() {
				// httptest calls the handler directly, without net/http's form cleanup.
				if req.MultipartForm != nil {
					if err := req.MultipartForm.RemoveAll(); err != nil {
						t.Error(err)
					}
				}
			})
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			var response struct {
				OK          bool    `json:"ok"`
				Result      Message `json:"result"`
				Description string  `json:"description"`
				ErrorCode   int     `json:"error_code"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if rec.Code != tc.status || response.OK != (tc.status == http.StatusOK) {
				t.Fatalf("upload status=%d body=%s, want %d", rec.Code, rec.Body.String(), tc.status)
			}
			if tc.status == http.StatusRequestEntityTooLarge {
				if response.ErrorCode != tc.status || !strings.Contains(response.Description, "50 MB") {
					t.Fatalf("unexpected upload error: %s", rec.Body.String())
				}
				if count, size := srv.Store().Files.Stats(); count != 0 || size != 0 {
					t.Fatalf("rejected upload retained files: count=%d bytes=%d", count, size)
				}
				if messages := srv.Store().Bot("741:upload").Messages(42, 100); len(messages) != 0 {
					t.Fatalf("rejected upload recorded messages: %+v", messages)
				}
				return
			}

			if len(response.Result.Photo) != 1 || response.Result.Photo[0].FileID == "" {
				t.Fatalf("upload returned no photo: %s", rec.Body.String())
			}
			file := httptest.NewRecorder()
			srv.Handler().ServeHTTP(file, httptest.NewRequest(http.MethodGet, "/debug/files/"+response.Result.Photo[0].FileID, nil))
			if file.Code != http.StatusOK || !bytes.Equal(file.Body.Bytes(), data) {
				t.Fatalf("upload at limit did not retain all bytes: status=%d bytes=%d", file.Code, file.Body.Len())
			}
		})
	}
}

func TestSendMediaWithoutUpload(t *testing.T) {
	for _, multipartRequest := range []bool{false, true} {
		srv := New(Config{Quiet: true})
		body := bytes.NewBufferString(`{"chat_id":42,"photo":"cached-photo"}`)
		contentType := "application/json"
		if multipartRequest {
			body.Reset()
			w := multipart.NewWriter(body)
			for key, value := range map[string]string{"chat_id": "42", "photo": "cached-photo"} {
				if err := w.WriteField(key, value); err != nil {
					t.Fatal(err)
				}
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			contentType = w.FormDataContentType()
		}
		req := httptest.NewRequest(http.MethodPost, "/bot741:upload/sendPhoto", body)
		req.Header.Set("Content-Type", contentType)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		var response struct {
			OK     bool    `json:"ok"`
			Result Message `json:"result"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if rec.Code != http.StatusOK || !response.OK || len(response.Result.Photo) != 1 || response.Result.Photo[0].FileID != "cached-photo" {
			t.Fatalf("file reference changed: status=%d body=%s", rec.Code, rec.Body.String())
		}
	}
}
