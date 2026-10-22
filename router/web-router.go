package router

import (
	"embed"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/gin-contrib/gzip"
	"github.com/gin-contrib/static"
	"github.com/gin-gonic/gin"
)

// WebAssets holds the embedded dashboard frontend assets.
type WebAssets struct {
	BuildFS   embed.FS
	IndexPage []byte
}

func SetWebRouter(router *gin.Engine, assets WebAssets, pluginDispatcher gin.HandlerFunc) {
	frontendFS := common.EmbedFolder(assets.BuildFS, "web/dist")

	router.NoRoute(
		pluginDispatcher,
		middleware.RouteTag("web"),
		gzip.Gzip(gzip.DefaultCompression),
		middleware.AccessTokenAudit(),
		middleware.GlobalWebRateLimit(),
		middleware.Cache(),
		func(c *gin.Context) {
			if !strings.HasPrefix(c.Request.URL.Path, "/plan-editors/") {
				c.Next()
				return
			}
			// 直接返回独立编辑页，避免 FileServer 将 index.html 重定向到目录后
			// 被 SPA 回退接管。仅开放两个明确的静态页面，不做任意文件读取。
			for _, kind := range []string{"communication", "filter"} {
				base := "/plan-editors/" + kind + "/"
				if c.Request.URL.Path != base && c.Request.URL.Path != base+"index.html" {
					continue
				}
				page, err := assets.BuildFS.ReadFile("web/dist" + base + "index.html")
				if err == nil {
					c.Data(http.StatusOK, "text/html; charset=utf-8", page)
					c.Abort()
					return
				}
			}
			c.AbortWithStatus(http.StatusNotFound)
		},
		static.Serve("/", frontendFS),
		func(c *gin.Context) {
			if strings.HasPrefix(c.Request.RequestURI, "/v1") || strings.HasPrefix(c.Request.RequestURI, "/api") || strings.HasPrefix(c.Request.RequestURI, "/assets") {
				controller.RelayNotFound(c)
				return
			}
			c.Header("Cache-Control", "no-cache")
			c.Data(http.StatusOK, "text/html; charset=utf-8", assets.IndexPage)
		},
	)
}
