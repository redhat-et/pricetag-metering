package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/redhat-et/pricetag-metering/internal/config"
	"github.com/redhat-et/pricetag-metering/internal/maasapi"
	"github.com/redhat-et/pricetag-metering/internal/storage"
)

// UsersHandler exposes the SSO user directory to a trusted partner backend.
// The browser-facing dashboard does not receive this credential and must not
// call these endpoints directly.
type UsersHandler struct {
	store      *storage.Store
	maasClient *maasapi.Client
	cfg        config.Config
}

func NewUsersHandler(store *storage.Store, maasClient *maasapi.Client, cfg config.Config) *UsersHandler {
	return &UsersHandler{store: store, maasClient: maasClient, cfg: cfg}
}

type userRequest struct {
	UserID string            `json:"user_id"`
	ID     string            `json:"id,omitempty"` // accepted as a convenience alias
	Tags   map[string]string `json:"tags"`
}

func (in userRequest) resolvedID() string {
	if strings.TrimSpace(in.UserID) != "" {
		return strings.TrimSpace(in.UserID)
	}
	return strings.TrimSpace(in.ID)
}

type userKeyRequest struct {
	Name         string            `json:"name"`
	Description  string            `json:"description,omitempty"`
	Subscription string            `json:"subscription,omitempty"`
	ExpiresIn    string            `json:"expiresIn,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
}

func (h *UsersHandler) HandleUsers(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	const prefix = "/api/v1/users"
	if path != prefix && !strings.HasPrefix(path, prefix+"/") {
		http.NotFound(w, r)
		return
	}

	rest := strings.TrimPrefix(path, prefix)
	if rest == "" {
		h.handleCollection(w, r)
		return
	}
	parts := strings.Split(strings.TrimPrefix(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "user id is required", http.StatusBadRequest)
		return
	}
	userID, err := url.PathUnescape(parts[0])
	if err != nil || userID == "" || strings.Contains(userID, "/") {
		http.Error(w, "invalid user id", http.StatusBadRequest)
		return
	}

	if len(parts) == 2 && (parts[1] == "keys" || parts[1] == "key") {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		h.mintKey(w, r, userID)
		return
	}
	if len(parts) != 1 {
		http.NotFound(w, r)
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getUser(w, r, userID)
	case http.MethodPut:
		h.updateUser(w, r, userID, false)
	case http.MethodPatch:
		h.updateUser(w, r, userID, true)
	case http.MethodDelete:
		h.deleteUser(w, r, userID)
	default:
		w.Header().Set("Allow", "GET, PUT, PATCH, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *UsersHandler) handleCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		filters := map[string]string{}
		for key, values := range r.URL.Query() {
			if len(values) == 0 {
				continue
			}
			name := ""
			switch {
			case strings.HasPrefix(key, "tag."):
				name = strings.TrimPrefix(key, "tag.")
			case strings.HasPrefix(key, "tag_"):
				name = strings.TrimPrefix(key, "tag_")
			}
			if name != "" {
				filters[name] = values[0]
			}
		}
		users, err := h.store.ListUsers(r.Context(), filters, r.URL.Query().Get("search"))
		if err != nil {
			http.Error(w, "failed to list users", http.StatusInternalServerError)
			return
		}
		if users == nil {
			users = []storage.User{}
		}
		writeJSON(w, users)
	case http.MethodPost:
		var in userRequest
		if err := decodeUserJSON(r, &in); err != nil {
			http.Error(w, "invalid user body: "+err.Error(), http.StatusBadRequest)
			return
		}
		userID := in.resolvedID()
		if err := h.validateCreate(userID, in.Tags); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		user, err := h.store.CreateUser(r.Context(), userID, in.Tags)
		if err != nil {
			http.Error(w, userWriteError(err), userWriteStatus(err))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, user)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *UsersHandler) getUser(w http.ResponseWriter, r *http.Request, userID string) {
	user, err := h.store.GetUser(r.Context(), userID)
	if err == sql.ErrNoRows {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "failed to get user", http.StatusInternalServerError)
		return
	}
	writeJSON(w, user)
}

func (h *UsersHandler) updateUser(w http.ResponseWriter, r *http.Request, userID string, merge bool) {
	var in userRequest
	if err := decodeUserJSON(r, &in); err != nil {
		http.Error(w, "invalid user body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if in.Tags == nil {
		http.Error(w, "tags are required", http.StatusBadRequest)
		return
	}

	var user storage.User
	var err error
	if merge {
		if err := h.validateTags(in.Tags, false); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		user, err = h.store.PatchUser(r.Context(), userID, in.Tags)
	} else {
		if err := h.validateTags(in.Tags, true); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		user, err = h.store.UpdateUser(r.Context(), userID, in.Tags)
	}
	if err == sql.ErrNoRows {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, userWriteError(err), userWriteStatus(err))
		return
	}
	writeJSON(w, user)
}

func (h *UsersHandler) deleteUser(w http.ResponseWriter, r *http.Request, userID string) {
	err := h.store.DeleteUser(r.Context(), userID)
	if err == sql.ErrNoRows {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "failed to delete user", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *UsersHandler) mintKey(w http.ResponseWriter, r *http.Request, userID string) {
	user, err := h.store.GetUser(r.Context(), userID)
	if err == sql.ErrNoRows {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "failed to resolve user", http.StatusInternalServerError)
		return
	}
	if h.maasClient == nil {
		http.Error(w, "key service unavailable", http.StatusServiceUnavailable)
		return
	}

	var in userKeyRequest
	if err := decodeUserJSON(r, &in); err != nil {
		http.Error(w, "invalid key body: "+err.Error(), http.StatusBadRequest)
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	email := strings.TrimSpace(user.Tags["email"])
	if email == "" {
		http.Error(w, "user has no email tag", http.StatusPreconditionFailed)
		return
	}

	created, err := h.maasClient.CreateGEAPIKey(r.Context(), email, maasapi.APIKeyRequest{
		Name:         in.Name,
		Description:  in.Description,
		Subscription: in.Subscription,
		ExpiresIn:    in.ExpiresIn,
		Labels:       in.Labels,
	})
	if err != nil {
		// Do not include the upstream body: it may contain credentials or
		// provider-specific details that are not safe for partner callers.
		http.Error(w, "failed to mint key", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, created)
}

func (h *UsersHandler) validateCreate(userID string, tags map[string]string) error {
	if userID == "" {
		return fmt.Errorf("user_id is required")
	}
	if strings.Contains(userID, "/") || len(userID) > 256 {
		return fmt.Errorf("user_id must be a single path-safe value of at most 256 characters")
	}
	return h.validateTags(tags, true)
}

func (h *UsersHandler) validateTags(tags map[string]string, requireAll bool) error {
	if tags == nil {
		return fmt.Errorf("tags are required")
	}
	for key, value := range tags {
		if strings.TrimSpace(key) == "" || len(key) > 128 {
			return fmt.Errorf("tag names must be between 1 and 128 characters")
		}
		if len(value) > 4096 {
			return fmt.Errorf("tag %q is too long", key)
		}
	}
	if requireAll {
		for _, key := range h.cfg.RequiredUserTags {
			if strings.TrimSpace(tags[key]) == "" {
				return fmt.Errorf("required tag %q is missing", key)
			}
		}
	}
	return nil
}

func decodeUserJSON(r *http.Request, target any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	return decoder.Decode(target)
}

func userWriteStatus(err error) int {
	if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}

func userWriteError(err error) string {
	if userWriteStatus(err) == http.StatusConflict {
		return "user already exists"
	}
	return "user operation failed"
}
