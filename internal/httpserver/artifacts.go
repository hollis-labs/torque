package httpserver

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
)

func (s *Server) listArtifacts(w http.ResponseWriter, r *http.Request) {
	// Accept task_id either from the URL (alias route /tasks/{id}/artifacts)
	// or from the query string (/artifacts?task_id=...). URL wins.
	taskID := chi.URLParam(r, "id")
	if taskID == "" {
		taskID = r.URL.Query().Get("task_id")
	}
	if taskID == "" {
		writeError(w, http.StatusBadRequest, "task_id is required")
		return
	}
	artifacts, err := s.svc.Artifact.List(taskID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if artifacts == nil {
		artifacts = []sqlstore.ArtifactRecord{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"artifacts": artifacts})
}

func (s *Server) getArtifact(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid artifact ID")
		return
	}
	art, err := s.svc.Artifact.Get(id)
	if err != nil {
		if errors.Is(err, sqlstore.ErrArtifactNotFound) {
			writeError(w, http.StatusNotFound, "artifact not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, art)
}

func (s *Server) deleteArtifact(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid artifact ID")
		return
	}
	if err := s.svc.Artifact.Delete(id); err != nil {
		if errors.Is(err, sqlstore.ErrArtifactNotFound) {
			writeError(w, http.StatusNotFound, "artifact not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) createArtifact(w http.ResponseWriter, r *http.Request) {
	fields, err := readJSONObjectUseNumber(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	taskID, err := rawStringField(fields, "task_id", false)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	typ, err := rawStringField(fields, "type", false)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if taskID == "" || typ == "" {
		writeError(w, http.StatusBadRequest, "task_id and type are required")
		return
	}
	content, err := rawStringField(fields, "content", true)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	url, err := rawStringField(fields, "url", true)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	filePath, err := rawStringField(fields, "file_path", true)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	rec := &sqlstore.ArtifactRecord{
		TaskID:   taskID,
		Type:     typ,
		Content:  content,
		URL:      url,
		FilePath: filePath,
	}
	if runID, present, err := rawNullableInt64Field(fields, "run_id"); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	} else if present && runID != nil {
		rec.RunID = sql.NullInt64{Int64: *runID, Valid: true}
	}
	if metadata, present, err := rawNullableMetadataField(fields, "metadata"); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	} else if present && metadata != nil {
		rec.Metadata = sql.NullString{String: string(metadata), Valid: true}
	}

	if err := s.svc.Artifact.Create(rec); err != nil {
		writeArtifactServiceError(w, err)
		return
	}
	s.sse.Broadcast("artifact.created", map[string]interface{}{"task_id": taskID, "artifact_id": rec.ID})
	writeJSON(w, http.StatusCreated, map[string]interface{}{"id": rec.ID})
}

func (s *Server) updateArtifact(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid artifact ID")
		return
	}
	fields, err := readJSONObjectUseNumber(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	var in service.ArtifactUpdateInput
	if v, ok := fields["type"]; ok {
		sv, err := rawString(v, "type")
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		in.Type = &sv
	}
	if v, ok := fields["content"]; ok {
		sv, err := rawString(v, "content")
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		in.Content = &sv
	}
	if v, ok := fields["url"]; ok {
		sv, err := rawString(v, "url")
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		in.URL = &sv
	}
	if v, ok := fields["file_path"]; ok {
		sv, err := rawString(v, "file_path")
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		in.FilePath = &sv
	}
	if runID, present, err := rawNullableInt64Field(fields, "run_id"); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	} else if present {
		if runID == nil {
			in.RunID = &sql.NullInt64{}
		} else {
			in.RunID = &sql.NullInt64{Int64: *runID, Valid: true}
		}
	}
	if metadata, present, err := rawNullableMetadataField(fields, "metadata"); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	} else if present {
		if metadata == nil {
			in.Metadata = &sql.NullString{}
		} else {
			in.Metadata = &sql.NullString{String: string(metadata), Valid: true}
		}
	}
	rec, err := s.svc.Artifact.Update(id, in)
	if err != nil {
		writeArtifactServiceError(w, err)
		return
	}
	s.sse.Broadcast("artifact.updated", map[string]interface{}{"task_id": rec.TaskID, "artifact_id": rec.ID})
	writeJSON(w, http.StatusOK, rec)
}

func readJSONObjectUseNumber(r *http.Request) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(r.Body)
	dec.UseNumber()
	var fields map[string]json.RawMessage
	if err := dec.Decode(&fields); err != nil {
		return nil, err
	}
	if fields == nil {
		return nil, errors.New("expected object")
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("expected single object")
	}
	return fields, nil
}

func rawStringField(fields map[string]json.RawMessage, name string, optional bool) (string, error) {
	raw, ok := fields[name]
	if !ok {
		return "", nil
	}
	if optional && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", nil
	}
	return rawString(raw, name)
}

func rawString(raw json.RawMessage, name string) (string, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", errors.New(name + " must be a string")
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", errors.New(name + " must be a string")
	}
	return s, nil
}

func rawNullableInt64Field(fields map[string]json.RawMessage, name string) (*int64, bool, error) {
	raw, ok := fields[name]
	if !ok {
		return nil, false, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, true, nil
	}
	var n json.Number
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&n); err == nil {
		ns := n.String()
		id, err := strconv.ParseInt(ns, 10, 64)
		if err != nil {
			return nil, true, errors.New(name + " must be an integer")
		}
		return &id, true, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil || strings.ContainsAny(s, ".eE") {
			return nil, true, errors.New(name + " must be an integer")
		}
		return &id, true, nil
	}
	return nil, true, errors.New(name + " must be an integer or null")
}

func rawNullableMetadataField(fields map[string]json.RawMessage, name string) (json.RawMessage, bool, error) {
	raw, ok := fields[name]
	if !ok {
		return nil, false, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if bytes.Equal(trimmed, []byte("null")) {
		return nil, true, nil
	}
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, true, errors.New(name + " must be a JSON object or null")
		}
		raw = json.RawMessage(s)
		trimmed = bytes.TrimSpace(raw)
	}
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, true, errors.New(name + " must be a JSON object or null")
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil {
		return nil, true, errors.New(name + " must be a JSON object or null")
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, true, errors.New(name + " must be a single JSON object or null")
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return nil, true, errors.New(name + " must be a JSON object or null")
	}
	return out, true, nil
}

func writeArtifactServiceError(w http.ResponseWriter, err error) {
	if errors.Is(err, sqlstore.ErrArtifactNotFound) {
		writeError(w, http.StatusNotFound, "artifact not found")
		return
	}
	var ve *service.ValidationError
	if errors.As(err, &ve) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}
