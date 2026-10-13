package httpserver

import (
	"encoding/json"
	"errors"
	"github.com/go-chi/chi/v5"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/hollis-labs/torque/internal/tetherclient"
	"io"
	"net/http"
	"net/url"
	"strings"
)

func (s *Server) forumRoutes(r chi.Router) {
	for _, route := range []struct {
		path    string
		methods []string
		fields  []string
	}{
		{"/forums", []string{"GET", "POST"}, []string{"id", "namespace", "title", "owner_type", "owner_id", "config_json", "metadata_json"}},
		{"/forums/{forum_id}", []string{"GET"}, nil},
		{"/forums/{forum_id}/topics", []string{"GET", "POST"}, []string{"title"}},
		{"/forums/topics/{topic_id}", []string{"GET"}, nil},
		{"/forums/topics/{topic_id}/posts", []string{"GET", "POST"}, []string{"body_md", "parent_post_id"}},
		{"/forums/posts/{post_id}", []string{"GET"}, nil},
		{"/forums/{forum_id}/search", []string{"GET"}, nil},
	} {
		for _, method := range route.methods {
			r.MethodFunc(method, route.path, s.forumHandler(route.path, route.fields))
		}
	}
}

func (s *Server) forumHandler(pattern string, fields []string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Gate before parsing or contacting Tether.
		if err := s.svc.Feature.Require("forums"); err != nil {
			writePlannerError(w, err)
			return
		}
		path := pattern
		for _, name := range []string{"forum_id", "topic_id", "post_id"} {
			if strings.Contains(path, "{"+name+"}") {
				id := chi.URLParam(r, name)
				if id == "" || strings.ContainsAny(id, "/\\") || id == "." || id == ".." {
					writeError(w, 400, "invalid forum identifier")
					return
				}
				path = strings.ReplaceAll(path, "{"+name+"}", url.PathEscape(id))
			}
		}
		q := url.Values{}
		for _, key := range []string{"cursor", "limit", "since_seq", "status", "author", "author_kind", "created_after", "created_before", "has_replies", "workstream_id", "project_id", "detail", "max_chars", "fields", "query"} {
			if vals, ok := r.URL.Query()[key]; ok {
				q[key] = vals
			}
		}
		if len(q) > 0 {
			path += "?" + q.Encode()
		}
		var body json.RawMessage
		if r.Method == http.MethodPost {
			var obj map[string]json.RawMessage
			dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
			if err := dec.Decode(&obj); err != nil || obj == nil {
				writeError(w, 400, "expected a JSON object")
				return
			}
			var extra any
			if dec.Decode(&extra) != io.EOF {
				writeError(w, 400, "expected one JSON object")
				return
			}
			for key := range obj {
				allowed := false
				for _, f := range fields {
					if key == f {
						allowed = true
						break
					}
				}
				if !allowed {
					writeError(w, 400, "unsupported forum field: "+key)
					return
				}
			}
			body, _ = json.Marshal(obj)
		}
		out, status, err := s.svc.Forums.Do(r.Context(), r.Method, path, body)
		if err != nil {
			var fe *tetherclient.ForumsError
			var disabled *service.FeatureDisabledError
			if errors.As(err, &fe) {
				writeJSON(w, fe.Status, map[string]string{"error": fe.Message, "code": fe.Code})
				return
			}
			if errors.As(err, &disabled) {
				writePlannerError(w, err)
				return
			}
			writeError(w, 503, "Forums unavailable")
			return
		}
		writeJSON(w, status, out)
	}
}
