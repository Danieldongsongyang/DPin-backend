package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

func TestBelloBuddyResumeWriteLimitDoesNotConsumeLogin(t *testing.T) {
	oldEnable, oldNum, oldDuration, oldRedis := common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration, common.RedisEnabled
	t.Cleanup(func() {
		common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration, common.RedisEnabled = oldEnable, oldNum, oldDuration, oldRedis
	})
	common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration, common.RedisEnabled = true, 2, 60, false
	router := gin.New()
	success := func(c *gin.Context) { c.Status(http.StatusNoContent) }
	router.POST("/resume", BelloBuddyResumeWriteRateLimit(), success)
	router.POST("/reconcile", BelloBuddyResumeWriteRateLimit(), success)
	router.POST("/login", CriticalRateLimit(), success)
	router.GET("/status", success)
	call := func(method, route string, expected int) {
		t.Helper()
		request := httptest.NewRequest(method, route, nil)
		request.RemoteAddr = "192.0.2.239:12345"
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != expected {
			t.Fatalf("%s %s: got %d want %d", method, route, response.Code, expected)
		}
		if expected == http.StatusTooManyRequests && response.Header().Get("Retry-After") != "60" {
			t.Fatal("限流必须保留 Retry-After")
		}
	}
	call("POST", "/resume", http.StatusNoContent)
	call("POST", "/reconcile", http.StatusNoContent)
	call("POST", "/resume", http.StatusTooManyRequests)
	call("GET", "/status", http.StatusNoContent)
	call("POST", "/login", http.StatusNoContent)
	call("POST", "/login", http.StatusNoContent)
	call("POST", "/login", http.StatusTooManyRequests)
}
