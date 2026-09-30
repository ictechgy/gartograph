// Package muxapp은 net/http ServeMux(Go 1.22+ 패턴) 합성 서버다.
package muxapp

import (
	"net/http"

	"example.com/routesoracle/fixtures/mark"
)

// Server는 핸들러 메서드를 모은 서버다.
type Server struct{ name string }

func (s *Server) listItems(w http.ResponseWriter, r *http.Request)  { mark.HTTP(w) }
func (s *Server) getItem(w http.ResponseWriter, r *http.Request)    { mark.HTTP(w) }
func (s *Server) createItem(w http.ResponseWriter, r *http.Request) { mark.HTTP(w) }
func (s *Server) files(w http.ResponseWriter, r *http.Request)      { mark.HTTP(w) }
func (s *Server) static(w http.ResponseWriter, r *http.Request)     { mark.HTTP(w) }
func (s *Server) status(w http.ResponseWriter, r *http.Request)     { mark.HTTP(w) }
func (s *Server) wrapped(w http.ResponseWriter, r *http.Request)    { mark.HTTP(w) }
func (s *Server) report(w http.ResponseWriter, r *http.Request)     { mark.HTTP(w) }

func home(w http.ResponseWriter, r *http.Request)       { mark.HTTP(w) }
func adminUsers(w http.ResponseWriter, r *http.Request) { mark.HTTP(w) }
func adminHome(w http.ResponseWriter, r *http.Request)  { mark.HTTP(w) }
func ping(w http.ResponseWriter, r *http.Request)       { mark.HTTP(w) }
func fallback(w http.ResponseWriter, r *http.Request)   { mark.HTTP(w) }

// objHandler는 ServeHTTP를 가진 값 핸들러다.
type objHandler struct{}

func (objHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) { mark.HTTP(w) }

// makeMetrics는 클로저를 돌려주는 핸들러 생성 함수다.
func makeMetrics(label string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { mark.HTTP(w) }
}

// logging은 핸들러를 감싸는 미들웨어다.
func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { next.ServeHTTP(w, r) })
}

// NewHandler는 서버의 최상위 mux를 만든다.
func NewHandler() http.Handler {
	s := &Server{name: "items"}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items", s.listItems)
	mux.HandleFunc("GET /items/{id}", s.getItem)
	mux.HandleFunc("POST /items", s.createItem)
	mux.HandleFunc("/files/{path...}", s.files)
	mux.HandleFunc("GET /{$}", home)
	mux.Handle("GET /static/", http.HandlerFunc(s.static))
	mux.HandleFunc("api.example.com/v2/status", s.status)
	mux.Handle("/admin/", http.StripPrefix("/admin", AdminMux()))
	mux.HandleFunc("DELETE /items/{id}", func(w http.ResponseWriter, r *http.Request) { mark.HTTP(w) })
	mux.Handle("/metrics", makeMetrics("m"))
	mux.Handle("/wrapped", logging(http.HandlerFunc(s.wrapped)))
	mux.Handle("PUT /obj/{id}", &objHandler{})
	registerReports(mux, s)
	mux.HandleFunc("/", fallback)
	return mux
}

// registerReports는 파라미터로 받은 mux에 등록한다.
func registerReports(mux *http.ServeMux, s *Server) {
	mux.HandleFunc("GET /reports/{year}/{month}", s.report)
}

// AdminMux는 /admin 아래에 StripPrefix로 붙는 mux다.
func AdminMux() *http.ServeMux {
	m := http.NewServeMux()
	m.HandleFunc("GET /users", adminUsers)
	m.HandleFunc("GET /{$}", adminHome)
	return m
}

// Register는 DefaultServeMux에 등록한다.
func Register() {
	http.HandleFunc("/legacy/ping", ping)
}
