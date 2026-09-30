// Package echoapp은 echo v4 합성 서버다.
package echoapp

import (
	"github.com/labstack/echo/v4"

	"example.com/routesoracle/fixtures/mark"
)

// Handler는 핸들러 메서드를 모은다.
type Handler struct{ db string }

// markEcho는 echo 핸들러의 이름을 응답 헤더에 싣는다.
func markEcho(c echo.Context) error {
	c.Response().Header().Set(mark.Header, mark.Name(1))
	return c.NoContent(200)
}

func (h *Handler) home(c echo.Context) error       { return markEcho(c) }
func (h *Handler) getUser(c echo.Context) error    { return markEcho(c) }
func (h *Handler) userPosts(c echo.Context) error  { return markEcho(c) }
func (h *Handler) getOrder(c echo.Context) error   { return markEcho(c) }
func (h *Handler) createItem(c echo.Context) error { return markEcho(c) }
func (h *Handler) getItem(c echo.Context) error    { return markEcho(c) }
func (h *Handler) files(c echo.Context) error      { return markEcho(c) }
func (h *Handler) anyRoute(c echo.Context) error   { return markEcho(c) }
func (h *Handler) put(c echo.Context) error        { return markEcho(c) }
func (h *Handler) match(c echo.Context) error      { return markEcho(c) }
func (h *Handler) filePrefix(c echo.Context) error { return markEcho(c) }
func (h *Handler) dash(c echo.Context) error       { return markEcho(c) }
func (h *Handler) report(c echo.Context) error     { return markEcho(c) }

// passThrough는 그룹 미들웨어다(echo Group.Use는 RouteNotFound 경로를 더한다).
func passThrough(next echo.HandlerFunc) echo.HandlerFunc { return next }

// Hosts는 host 전용 라우터의 host다(오라클이 narrowed 사실을 탐침할 때 쓴다).
var Hosts = []string{"admin.example.com"}

// NewServer는 서버를 만든다.
func NewServer() *echo.Echo {
	h := &Handler{db: "x"}
	e := echo.New()
	e.GET("/", h.home)
	e.GET("/users/:id", h.getUser)
	e.GET("/users/:id/posts", h.userPosts)
	e.GET("/orders/:id", h.getOrder)
	g := e.Group("/api")
	g.Use(passThrough)
	g.POST("/items", h.createItem)
	g.GET("/items/:id", h.getItem)
	g.GET("/files/*", h.files)
	g.Any("/any", h.anyRoute)
	e.Add("PUT", "/put", h.put)
	e.Match([]string{"GET", "DELETE"}, "/match", h.match)
	e.GET("/file-:name", h.filePrefix)
	admin := e.Host(Hosts[0])
	admin.GET("/dash", h.dash)
	registerReports(g.Group("/reports"), h)
	e.Static("/static", "public")
	e.File("/robots.txt", "public/robots.txt")
	return e
}

// registerReports는 그룹 파라미터로 받은 라우터에 등록한다.
func registerReports(g *echo.Group, h *Handler) {
	g.GET("/:year", h.report)
}
