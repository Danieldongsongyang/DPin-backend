package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
)

func registerBelloBuddyReleaseAdminRoutes(admin *gin.RouterGroup) {
	releases := admin.Group("/releases")
	releases.GET("", middleware.RequirePermission(authz.BelloBuddyReleaseRead), controller.ListBelloBuddyReleases)
	releases.GET("/:id", middleware.RequirePermission(authz.BelloBuddyReleaseRead), controller.GetBelloBuddyRelease)
	releases.POST("", middleware.RequirePermission(authz.BelloBuddyReleaseWrite), controller.SaveBelloBuddyRelease)
	releases.PUT("/:id", middleware.RequirePermission(authz.BelloBuddyReleaseWrite), controller.SaveBelloBuddyRelease)
	releases.POST("/:id/artifacts/:kind", middleware.RequirePermission(authz.BelloBuddyReleaseWrite), controller.UploadBelloBuddyReleaseArtifact)
	releases.POST("/:id/validate", middleware.RequirePermission(authz.BelloBuddyReleaseWrite), controller.ValidateBelloBuddyRelease)
	releases.POST("/:id/recover-validation", middleware.RequirePermission(authz.BelloBuddyReleaseWrite), controller.RecoverBelloBuddyReleaseValidation)
	releases.POST("/:id/acceptance", middleware.RequirePermission(authz.BelloBuddyReleasePublish), controller.AcceptBelloBuddyRelease)
	for _, operation := range []struct {
		action     string
		permission authz.Permission
	}{{"publish", authz.BelloBuddyReleasePublish}, {"withdraw", authz.BelloBuddyReleaseWithdraw}, {"disable", authz.BelloBuddyReleaseWithdraw}} {
		releases.POST("/:id/"+operation.action, middleware.RequirePermission(operation.permission), func(c *gin.Context) {
			c.Set("bello_release_action", operation.action)
			controller.OperateBelloBuddyRelease(c)
		})
	}
}

// Register before relay middleware is attached to the engine. Downloads retain
// their raw bytes and have a separate concurrency limit, not AI/business quotas.
func SetBelloBuddyReleaseRouter(router *gin.Engine) {
	updates := router.Group("/desktop-updates/bellobuddy")
	updates.Use(middleware.RouteTag("desktop_updates"), middleware.DisableCache(), middleware.UserAuth(), middleware.BelloBuddySessionRequired())
	updates.GET("/stable/win32/x64/status", controller.GetBelloBuddyUpdateStatus)
	updates.GET("/stable/win32/x64/latest.yml", controller.GetBelloBuddyUpdateFeed)
	updates.GET("/releases/:id/win32/x64/status", controller.GetBelloBuddyFixedUpdateStatus)
	updates.GET("/releases/:id/win32/x64/latest.yml", controller.GetBelloBuddyUpdateFeed)
	updates.HEAD("/releases/:id/win32/x64/latest.yml", controller.GetBelloBuddyUpdateFeed)
	updates.GET("/artifacts/win32/x64/:filename", controller.DownloadBelloBuddyReleaseArtifact)
	updates.HEAD("/artifacts/win32/x64/:filename", controller.DownloadBelloBuddyReleaseArtifact)
}
