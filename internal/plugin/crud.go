package plugin

import (
	"context"
	"encoding/json"
	"net/http"

	goplugin "github.com/hollis-labs/plugin"
)

// wireCRUDRoutes registers HTTP routes for a CRUD handler on the given mux.
// Routes registered:
//
//	GET    /api/plugins/{resourceType}       → List
//	POST   /api/plugins/{resourceType}       → Create
//	GET    /api/plugins/{resourceType}/{id}  → Read
//	PUT    /api/plugins/{resourceType}/{id}  → Update
//	DELETE /api/plugins/{resourceType}/{id}  → Delete
func wireCRUDRoutes(mux *http.ServeMux, resourceType string, handler goplugin.CRUDHandler) {
	base := "/api/plugins/" + resourceType
	item := base + "/"

	mux.HandleFunc(base, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			results, err := handler.List(r.Context(), nil)
			writeJSON(w, results, err)
		case http.MethodPost:
			var body interface{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			result, err := handler.Create(r.Context(), body)
			writeJSON(w, result, err)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc(item, func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Path[len(item):]
		if id == "" {
			http.Error(w, "missing id", http.StatusBadRequest)
			return
		}
		switch r.Method {
		case http.MethodGet:
			result, err := handler.Read(r.Context(), id)
			writeJSON(w, result, err)
		case http.MethodPut:
			var body interface{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			result, err := handler.Update(r.Context(), id, body)
			writeJSON(w, result, err)
		case http.MethodDelete:
			err := handler.Delete(r.Context(), id)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

func writeJSON(w http.ResponseWriter, data interface{}, err error) {
	if err != nil {
		if pe, ok := err.(*goplugin.PluginError); ok {
			http.Error(w, pe.Error(), pe.Code)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(data)
}

// contextKey is unexported to avoid collisions.
type contextKey string

const resourceIDKey contextKey = "resource_id"

func withResourceID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, resourceIDKey, id)
}
