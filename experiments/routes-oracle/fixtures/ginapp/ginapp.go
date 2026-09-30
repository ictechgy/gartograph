// Package ginapp은 gin v1 합성 서버다.
package ginapp

import (
	"github.com/gin-gonic/gin"

	"example.com/routesoracle/fixtures/mark"
)

// Handler는 핸들러 메서드를 모은다.
type Handler struct{ db string }

// markGin은 gin 핸들러의 이름을 응답 헤더에 싣는다.
func markGin(c *gin.Context) {
	c.Header(mark.Header, mark.Name(1))
	c.Status(200)
}

func (h *Handler) ping(c *gin.Context)        { markGin(c) }
func (h *Handler) getUser(c *gin.Context)     { markGin(c) }
func (h *Handler) userPosts(c *gin.Context)   { markGin(c) }
func (h *Handler) createUser(c *gin.Context)  { markGin(c) }
func (h *Handler) files(c *gin.Context)       { markGin(c) }
func (h *Handler) putSettings(c *gin.Context) { markGin(c) }
func (h *Handler) proxy(c *gin.Context)       { markGin(c) }
func (h *Handler) patchItem(c *gin.Context)   { markGin(c) }
func (h *Handler) match(c *gin.Context)       { markGin(c) }
func (h *Handler) report(c *gin.Context)      { markGin(c) }
func (h *Handler) avatar(c *gin.Context)      { markGin(c) }

// noopMiddleware는 그룹 미들웨어다.
func noopMiddleware(c *gin.Context) { c.Next() }

// NewRouter는 서버의 엔진을 만든다.
func NewRouter() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	h := &Handler{db: "x"}
	r := gin.New()
	r.GET("/ping", h.ping)
	v1 := r.Group("/v1")
	{
		v1.GET("/users/:id", h.getUser)
		v1.GET("/users/:id/posts", h.userPosts)
		v1.POST("/users", h.createUser)
		v1.GET("/files/*filepath", h.files)
		v1.GET("/avatar_:name", h.avatar)
		admin := v1.Group("/admin", noopMiddleware)
		admin.PUT("/settings/", h.putSettings)
		admin.Any("/proxy", h.proxy)
	}
	r.Handle("PATCH", "/items/:id", h.patchItem)
	r.Match([]string{"GET", "POST"}, "/match", h.match)
	r.StaticFile("/favicon.ico", "./favicon.ico")
	r.Static("/assets", "./assets")
	registerReports(v1.Group("/reports"), h)
	r.GET("/closure", func(c *gin.Context) { markGin(c) })
	return r
}

// registerReports는 그룹 파라미터로 받은 라우터에 등록한다.
func registerReports(g *gin.RouterGroup, h *Handler) {
	g.GET("/:year", h.report)
}
