package controller

import (
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// Public only through an opaque assessment ticket issued in the authenticated DPin flow.
func BelloBuddyTypingPublic(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	e := service.CurrentBelloBuddyTypingEngine()
	if e == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "测评服务暂时不可用，请稍后重试。"})
		return
	}
	if !e.OriginAllowed(c.GetHeader("Origin")) || c.GetHeader("Sec-Fetch-Site") == "cross-site" {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "请求来源无效。"})
		return
	}
	parts := strings.Fields(c.GetHeader("Authorization"))
	if len(parts) != 2 || parts[0] != "Bearer" {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "请从招聘会话中的个人测评链接进入。"})
		return
	}
	operation := c.Param("operation")
	if (c.Request.Method == http.MethodGet) != (operation == "info") {
		c.Status(http.StatusMethodNotAllowed)
		return
	}
	var body []byte
	if c.Request.Method == http.MethodPost {
		if c.ContentType() != "application/json" {
			c.Status(http.StatusUnsupportedMediaType)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 40000)
		var err error
		body, err = io.ReadAll(c.Request.Body)
		if err != nil {
			c.Status(http.StatusRequestEntityTooLarge)
			return
		}
	}
	result, err := e.PublicCall(parts[1], operation, body)
	if err != nil {
		if failure, ok := err.(*service.BelloBuddyError); ok {
			c.JSON(failure.Status, gin.H{"success": false, "message": failure.Message})
		} else {
			common.SysError("typing: assessment state could not be saved")
			c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "服务暂时繁忙，请保留本页并稍后重试。"})
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}
