// Package chiapp은 chi v5 합성 서버다.
package chiapp

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"example.com/routesoracle/fixtures/mark"
)

// Handler는 핸들러 메서드를 모은다.
type Handler struct{ db string }

func (h *Handler) home(w http.ResponseWriter, r *http.Request)          { mark.HTTP(w) }
func (h *Handler) health(w http.ResponseWriter, r *http.Request)        { mark.HTTP(w) }
func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request)     { mark.HTTP(w) }
func (h *Handler) getUser(w http.ResponseWriter, r *http.Request)       { mark.HTTP(w) }
func (h *Handler) getUserBySlug(w http.ResponseWriter, r *http.Request) { mark.HTTP(w) }
func (h *Handler) createUser(w http.ResponseWriter, r *http.Request)    { mark.HTTP(w) }
func (h *Handler) getOrder(w http.ResponseWriter, r *http.Request)      { mark.HTTP(w) }
func (h *Handler) putOrderItem(w http.ResponseWriter, r *http.Request)  { mark.HTTP(w) }
func (h *Handler) search(w http.ResponseWriter, r *http.Request)        { mark.HTTP(w) }
func (h *Handler) deleteUser(w http.ResponseWriter, r *http.Request)    { mark.HTTP(w) }
func (h *Handler) fileJSON(w http.ResponseWriter, r *http.Request)      { mark.HTTP(w) }
func (h *Handler) adminHome(w http.ResponseWriter, r *http.Request)     { mark.HTTP(w) }
func (h *Handler) adminStats(w http.ResponseWriter, r *http.Request)    { mark.HTTP(w) }
func (h *Handler) assets(w http.ResponseWriter, r *http.Request)        { mark.HTTP(w) }
func (h *Handler) patchSettings(w http.ResponseWriter, r *http.Request) { mark.HTTP(w) }
func (h *Handler) hook(w http.ResponseWriter, r *http.Request)          { mark.HTTP(w) }
func (h *Handler) report(w http.ResponseWriter, r *http.Request)        { mark.HTTP(w) }
func (h *Handler) docs(w http.ResponseWriter, r *http.Request)          { mark.HTTP(w) }
func (h *Handler) debug(w http.ResponseWriter, r *http.Request)         { mark.HTTP(w) }
func (h *Handler) export(w http.ResponseWriter, r *http.Request)        { mark.HTTP(w) }

// noop은 inline 미들웨어다.
func noop(next http.Handler) http.Handler { return next }

// NewRouter는 서버의 최상위 라우터를 만든다.
func NewRouter() http.Handler {
	h := &Handler{db: "x"}
	r := chi.NewRouter()
	r.Get("/", h.home)
	r.Get("/health", h.health)
	r.Route("/api", func(r chi.Router) {
		r.Get("/users", h.listUsers)
		r.Get("/users/{id:[0-9]+}", h.getUser)
		r.Get("/users/{slug}", h.getUserBySlug)
		r.Post("/users", h.createUser)
		r.Route("/orders/{orderID}", func(r chi.Router) {
			r.Get("/", h.getOrder)
			r.Put("/items/{itemID}", h.putOrderItem)
		})
		r.With(noop).Get("/search", h.search)
		r.Group(func(r chi.Router) {
			r.Delete("/users/{id}", h.deleteUser)
		})
		r.Get("/files/{name}.json", h.fileJSON)
	})
	r.Mount("/admin", adminRouter(h))
	r.Handle("/assets/*", http.HandlerFunc(h.assets))
	r.Method("PATCH", "/settings", http.HandlerFunc(h.patchSettings))
	r.HandleFunc("POST /hooks", h.hook)
	r.Get("/docs*", h.docs)
	r.Mount("/debug", http.HandlerFunc(h.debug))
	r.With(noop, noop).Get("/export/{id:[0-9]+}.csv", h.export)
	registerReports(r, h)
	return r
}

// adminRouter는 /admin에 Mount되는 하위 라우터다.
func adminRouter(h *Handler) http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.adminHome)
	r.Get("/stats", h.adminStats)
	return r
}

// registerReports는 인터페이스 파라미터로 받은 라우터에 등록한다.
func registerReports(r chi.Router, h *Handler) {
	r.Get("/reports/{year}", h.report)
}
