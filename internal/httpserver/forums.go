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
	// Each method has its own field allowlist: moderation never accepts attribution.
	register := func(method, path string, fields ...string) {
		r.MethodFunc(method, path, s.forumHandler(path, fields))
	}
	register("GET", "/forums")
	register("GET", "/forums/{forum_id}/activity")
	register("POST", "/forums", "id", "namespace", "title", "description", "category_id", "sort_order", "owner_type", "owner_id", "config_json", "metadata_json")
	register("GET", "/forums/categories")
	register("POST", "/forums/categories", "id", "namespace", "title", "description", "sort_order")
	register("GET", "/forums/{forum_id}/topics")
	register("POST", "/forums/{forum_id}/topics", "title")
	register("GET", "/forums/topics/{topic_id}/posts")
	register("POST", "/forums/topics/{topic_id}/posts", "body_md", "parent_post_id", "subject", "attachments")
	register("GET", "/forums/{forum_id}/search")
	register("GET", "/forums/{forum_id}/participants")
	register("POST", "/forums/{forum_id}/participants", "principal_kind", "principal_id", "access")
	register("POST", "/forums/{forum_id}/participants/revoke", "principal_kind", "principal_id")
	register("POST", "/forums/{forum_id}/owner", "owner_type", "owner_id")
	for _, item := range []struct {
		path   string
		fields []string
	}{
		{"/forums/categories/{category_id}", []string{"title", "description", "sort_order"}},
		{"/forums/{forum_id}", []string{"title", "description", "sort_order", "config_json", "metadata_json"}},
		{"/forums/topics/{topic_id}", []string{"title", "status"}},
		{"/forums/posts/{post_id}", []string{"body_md", "subject", "attachments"}},
	} {
		register("GET", item.path)
		register("PATCH", item.path, item.fields...)
		register("DELETE", item.path, "cascade")
		if item.path != "/forums/posts/{post_id}" {
			register("POST", item.path+"/archive")
			register("POST", item.path+"/unarchive")
		}
	}
	register("POST", "/forums/{forum_id}/move", "destination_category_id", "sort_order")
	register("POST", "/forums/{forum_id}/clone", "id", "title", "destination_category_id", "sort_order")
	register("POST", "/forums/topics/{topic_id}/move", "destination_forum_id")
	register("POST", "/forums/topics/{topic_id}/clone", "destination_forum_id", "title")
	register("POST", "/forums/topics/{topic_id}/fork", "post_id", "destination_forum_id", "title")
	register("POST", "/forums/posts/{post_id}/move", "destination_topic_id", "parent_post_id")
	register("POST", "/forums/posts/{post_id}/clone", "destination_topic_id", "parent_post_id")
}

func (s *Server) forumHandler(pattern string, fields []string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Gate before parsing or contacting Tether.
		if err := s.svc.Feature.Require("forums"); err != nil {
			writePlannerError(w, err)
			return
		}
		path := pattern
		for _, name := range []string{"forum_id", "topic_id", "post_id", "category_id"} {
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
		for _, key := range []string{"cursor", "limit", "since_seq", "status", "author", "author_kind", "created_after", "created_before", "has_replies", "workstream_id", "project_id", "detail", "max_chars", "fields", "query", "namespace", "category_id", "archived"} {
			if vals, ok := r.URL.Query()[key]; ok {
				q[key] = vals
			}
		}
		if r.Method == http.MethodGet && (pattern == "/forums" || pattern == "/forums/categories") {
			q.Set("namespace", service.TorqueForumNamespace)
		}
		if len(q) > 0 {
			path += "?" + q.Encode()
		}
		var body json.RawMessage
		if r.Method == http.MethodPost || r.Method == http.MethodPatch || r.Method == http.MethodDelete {
			var obj map[string]json.RawMessage
			dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
			err := dec.Decode(&obj)
			if err == io.EOF && r.Method == http.MethodDelete {
				obj = map[string]json.RawMessage{}
			} else if err != nil || obj == nil {
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
			if r.Method == http.MethodPost && (pattern == "/forums" || pattern == "/forums/categories") {
				obj["namespace"], _ = json.Marshal(service.TorqueForumNamespace)
			}
			body, _ = json.Marshal(obj)
		}
		var out json.RawMessage
		var status int
		var err error
		if pattern == "/forums/{forum_id}/activity" {
			var activity service.ForumActivity
			activity, err = s.svc.Forums.Activity(r.Context(), chi.URLParam(r, "forum_id"))
			if err == nil {
				out, err = json.Marshal(activity)
				status = http.StatusOK
			}
		} else {
			out, status, err = s.svc.Forums.Do(r.Context(), r.Method, path, body)
		}
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
