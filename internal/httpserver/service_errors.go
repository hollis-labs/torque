package httpserver

import (
	"errors"
	"net/http"

	"github.com/hollis-labs/torque/internal/service"
)

func browserActor(r *http.Request) string {
	if r.Header.Get("X-Client") == "browser" {
		return "chrispian"
	}
	return ""
}

func writeServiceError(w http.ResponseWriter, err error) {
	var ve *service.ValidationError
	var ce *service.ConflictError
	var nf *service.NotFoundError
	var fd *service.FeatureDisabledError
	body := map[string]any{"error": err.Error()}
	status := http.StatusInternalServerError
	switch {
	case errors.As(err, &ve):
		status, body["code"] = http.StatusBadRequest, "invalid"
	case errors.As(err, &ce):
		status, body["code"] = http.StatusConflict, "conflict"
	case errors.As(err, &nf):
		status, body["code"] = http.StatusNotFound, "not_found"
	case errors.As(err, &fd):
		status, body["code"] = http.StatusNotFound, "feature_disabled"
	default:
		body["code"] = "internal"
	}
	writeJSON(w, status, body)
}
