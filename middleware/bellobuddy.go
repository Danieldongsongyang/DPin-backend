package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// 在 UserAuth 之后执行，拒绝个人访问 Token、模型 Key 和未完成认证的身份。
func BelloBuddySessionRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := GetSessionAuthIdentity(c); !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"success": false, "code": "AUTH_SESSION_REQUIRED", "message": "请完成 DPin 产品账号登录", "source": "dpin"})
			return
		}
		c.Next()
	}
}
