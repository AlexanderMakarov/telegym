package mock

import (
	"bytes"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

const defaultMaxRequests = 5000

// APIRequest records a Bot API invocation. Parameters exclude uploaded file
// contents; captured bytes remain available through /debug/files/:file_id.
type APIRequest struct {
	Seq        int64          `json:"seq"`
	Method     string         `json:"method"`
	Parameters map[string]any `json:"parameters"`
	Status     int            `json:"status"`
	At         int64          `json:"at"`
}

func (s *Server) requestJournalMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		token, method, ok := parseBotPath(c.Request.URL.Path)
		if !ok {
			c.Next()
			return
		}

		request := APIRequest{
			Seq:    s.store.requestIDSeed.Add(1),
			Method: method,
			At:     time.Now().Unix(),
		}
		contentType := c.ContentType()
		captureBody := contentType == "application/json" || contentType == "application/x-www-form-urlencoded"
		var body bytes.Buffer
		if captureBody {
			// Tee the body so parsing and read errors still reach the handler.
			c.Request.Body = struct {
				io.Reader
				io.Closer
			}{io.TeeReader(c.Request.Body, &body), c.Request.Body}
		}

		c.Next()
		if captureBody {
			// Generic methods may return without reading their parameters.
			_, _ = io.Copy(io.Discard, c.Request.Body)
		}
		request.Parameters = captureRequestParameters(c.Request, contentType, body.Bytes())
		request.Status = c.Writer.Status()
		s.store.Bot(token).AppendRequest(request)
	}
}

func captureRequestParameters(r *http.Request, contentType string, body []byte) map[string]any {
	parameters := make(map[string]any)
	mergeRequestValues(parameters, r.URL.Query())

	switch contentType {
	case "multipart/form-data":
		// Reuse any form parsed by the handler, or parse it here for generic
		// methods. net/http cleans up temporary files when the request ends.
		if r.ParseMultipartForm(32<<20) == nil && r.MultipartForm != nil {
			mergeRequestValues(parameters, r.MultipartForm.Value)
		}
	case "application/json":
		var decoded map[string]any
		if json.Unmarshal(body, &decoded) == nil {
			maps.Copy(parameters, decoded)
		}
	case "application/x-www-form-urlencoded":
		values, _ := url.ParseQuery(string(body))
		mergeRequestValues(parameters, values)
	}
	return parameters
}

func mergeRequestValues(parameters map[string]any, values url.Values) {
	for key, entries := range values {
		if len(entries) == 1 {
			parameters[key] = entries[0]
		} else {
			parameters[key] = entries
		}
	}
}

// AppendRequest records a completed invocation in the bot's bounded journal.
func (b *botEntry) AppendRequest(request APIRequest) {
	b.requestMu.Lock()
	b.requests = appendCapped(b.requests, request, defaultMaxRequests)
	b.requestMu.Unlock()
}

// Requests returns the most recent matching requests in invocation order.
// The journal retains the last 5000 completed calls; limit defaults to 100.
func (b *botEntry) Requests(method string, limit int) []APIRequest {
	if limit <= 0 {
		limit = 100
	}
	b.requestMu.RLock()
	out := make([]APIRequest, 0)
	for _, request := range newest(b.requests, defaultMaxRequests) {
		if method == "" || request.Method == method {
			out = append(out, request)
		}
	}
	b.requestMu.RUnlock()

	// Concurrent calls may finish in a different order than they arrived.
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// ClearRequests drops this bot's journal without resetting sequence numbers.
func (b *botEntry) ClearRequests() int {
	b.requestMu.Lock()
	defer b.requestMu.Unlock()

	count := len(newest(b.requests, defaultMaxRequests))
	b.requests = nil
	return count
}

// DebugListRequests serves GET /debug/requests/:token with optional method
// and limit filters. Debug requests themselves are not journaled.
func (h *Handlers) DebugListRequests(c *gin.Context) {
	limit, _ := strconv.Atoi(c.Query("limit"))
	requests := h.Store.Bot(c.Param("token")).Requests(c.Query("method"), limit)
	c.JSON(http.StatusOK, gin.H{
		"count":    len(requests),
		"requests": requests,
	})
}

// DebugClearRequests serves POST /debug/requests/:token/clear and returns
// the number of retained requests removed.
func (h *Handlers) DebugClearRequests(c *gin.Context) {
	count := h.Store.Bot(c.Param("token")).ClearRequests()
	c.JSON(http.StatusOK, gin.H{"cleared": count})
}
