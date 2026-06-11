package handlers

import (
	"net/http"

	"github.com/Ho3einK84/Nodexia/internal/config"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

type ErrorHandler struct {
	config        config.Config
	renderer      *view.Renderer
	statusCode    int
	title         string
	message       string
	activeNavHref string
}

func NewErrorHandler(cfg config.Config, renderer *view.Renderer, statusCode int, title, message string) ErrorHandler {
	return ErrorHandler{
		config:        cfg,
		renderer:      renderer,
		statusCode:    statusCode,
		title:         title,
		message:       message,
		activeNavHref: "",
	}
}

func (h ErrorHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	page := view.NewErrorPageData(h.config, h.statusCode, h.title, h.message)
	page.ActiveNav = h.activeNavHref

	if err := h.renderer.Render(w, h.statusCode, page); err != nil {
		http.Error(w, h.message, h.statusCode)
	}
}
