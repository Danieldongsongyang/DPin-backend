package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// multipart 可由跨站表单提交：要求显式会话 Bearer，拒绝仅 Cookie 的环境认证。
func BelloBuddyResumeSession() gin.HandlerFunc {
	return func(c *gin.Context) {
		header := strings.Fields(c.GetHeader("Authorization"))
		if len(header) != 2 || !strings.EqualFold(header[0], "Bearer") {
			c.AbortWithStatusJSON(401, gin.H{"success": false, "code": "AUTH_SESSION_REQUIRED", "message": "简历归档需要显式的产品会话凭据"})
			return
		}
		if len(c.Request.Header.Values("Origin")) > 0 || len(c.Request.Header.Values("Referer")) > 0 {
			origin, ok := requestBrowserOrigin(c.Request)
			if !ok || !isAllowedSessionOrigin(c.Request, origin) {
				c.AbortWithStatusJSON(403, gin.H{"success": false, "code": "AUTH_ORIGIN_FORBIDDEN", "message": "请求来源不受信任"})
				return
			}
		}
		c.Next()
	}
}
