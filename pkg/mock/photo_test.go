package mock

import (
	"bytes"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"regexp"
	"strings"
	"testing"
)

func sendTestPhotoUpload(t *testing.T, srv *Server, token, partType string, data []byte) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("chat_id", "42")
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="photo"; filename="table.png"`)
	h.Set("Content-Type", partType)
	part, err := w.CreatePart(h)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(data)
	_ = w.Close()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bot"+token+"/sendPhoto", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("sendPhoto status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDebugChatShowsUploadedPhotos(t *testing.T) {
	const token = "741:photo"
	srv := New(Config{Quiet: true})
	var pngBytes bytes.Buffer
	if err := png.Encode(&pngBytes, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	sendTestPhotoUpload(t, srv, token, "image/png", pngBytes.Bytes())
	sendTestPhotoUpload(t, srv, token, "application/octet-stream", pngBytes.Bytes())
	sendTestPhotoUpload(t, srv, token, "application/octet-stream", []byte("not an image"))
	sendTestPhotoUpload(t, srv, token, "image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`))
	// file_id-only send: no bytes in the mock.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bot"+token+"/sendPhoto", strings.NewReader(`{"chat_id":42,"photo":"AgACcached"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rec, req)

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/chat/"+token+"/42/messages", nil))
	html := rec.Body.String()
	srcs := regexp.MustCompile(`<img class="msg-photo" src="(/debug/files/[^"]+)"`).FindAllStringSubmatch(html, -1)
	if len(srcs) != 2 {
		t.Fatalf("want 2 rendered photos (png + sniffed octet-stream), got %d:\n%s", len(srcs), html)
	}
	if n := strings.Count(html, `<span class="media-tag">photo</span>`); n != 3 {
		t.Fatalf("want 3 photo tags for non-image / SVG / file_id sends, got %d:\n%s", n, html)
	}

	img := httptest.NewRecorder()
	srv.Handler().ServeHTTP(img, httptest.NewRequest(http.MethodGet, srcs[0][1], nil))
	if img.Code != http.StatusOK || !bytes.Equal(img.Body.Bytes(), pngBytes.Bytes()) {
		t.Fatalf("photo URL must serve the uploaded bytes, status=%d", img.Code)
	}
}
