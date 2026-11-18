package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/Ho3einK84/Nodexia/internal/http/middleware"
)

type csrfTokenHandler struct{}

func NewCSRFTokenHandler() http.Handler {
	return csrfTokenHandler{}
}

func (h csrfTokenHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token := middleware.GetCSRFToken(r.Context())
	if token == "" {
		http.Error(w, "csrf: no token available", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]string{"csrf_token": token})
}
