package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
)

func registerBelloBuddyRoutes(apiRouter *gin.RouterGroup) {
	apiRouter.GET("/bellobuddy/typing/:operation", controller.BelloBuddyTypingPublic)
	apiRouter.POST("/bellobuddy/typing/:operation", controller.BelloBuddyTypingPublic)
	routes := apiRouter.Group("/bellobuddy")
	routes.Use(middleware.DisableCache(), middleware.UserAuth(), middleware.BelloBuddySessionRequired())
	routes.GET("/capabilities", controller.GetBelloBuddyCapabilities)
	routes.GET("/plans", controller.ListBelloBuddyPlans)
	routes.GET("/plans/:id", controller.GetBelloBuddyPlan)
	routes.POST("/plans", controller.SaveBelloBuddyPlan)
	routes.POST("/plans/:id/copy", controller.CopyBelloBuddyPlan)
	routes.PUT("/plans/:id", controller.SaveBelloBuddyPlan)
	routes.POST("/accounts/:accountKey/jobs/sync", controller.SyncBelloBuddyJobs)
	routes.GET("/accounts/:accountKey/jobs/config", controller.GetBelloBuddyJobs)
	routes.PUT("/accounts/:accountKey/jobs/:jobId/config", controller.SaveBelloBuddyJobConfig)
	routes.PUT("/accounts/:accountKey/jobs/config/batch", controller.SaveBelloBuddyJobConfigBatch)
	routes.POST("/accounts/:accountKey/runtime-config", controller.GetBelloBuddyRuntimeConfig)
	routes.POST("/accounts/:accountKey/runtime-snapshot/renew", controller.RenewBelloBuddyRuntimeSnapshot)
	archives := routes.Group("")
	archives.Use(middleware.BelloBuddyResumeSession())
	archives.POST("/accounts/:accountKey/jobs/:jobId/typing-invitations", middleware.UserCriticalRateLimit("bellobuddy_typing_invitation"), controller.PrepareBelloBuddyTypingInvitation)
	archives.POST("/accounts/:accountKey/job-catalog/syncs", controller.SyncBelloBuddyJobCatalog)
	archives.GET("/accounts/:accountKey/job-catalog/syncs/:syncId", controller.GetBelloBuddyJobCatalog)
	archives.POST("/accounts/:accountKey/job-catalog/syncs/:syncId/reconcile", controller.ReconcileBelloBuddyJobCatalog)
	archives.POST("/accounts/:accountKey/jobs/:jobId/resume-archives", middleware.BelloBuddyResumeWriteRateLimit(), controller.ReceiveBelloBuddyResume)
	archives.GET("/accounts/:accountKey/jobs/:jobId/resume-archives/by-upload/:uploadId", controller.GetBelloBuddyResumeUpload)
	archives.GET("/resume-archives", controller.ListBelloBuddyResumeArchives)
	archives.GET("/resume-archives/:archiveId", controller.GetBelloBuddyResumeArchive)
	archives.POST("/resume-archives/:archiveId/reconcile", middleware.BelloBuddyResumeWriteRateLimit(), controller.ReconcileBelloBuddyResumeArchive)

	ai := routes.Group("/ai")
	ai.Use(middleware.RouteTag("relay"), middleware.BodyStorageCleanup(), controller.BelloBuddyAIResponse(), middleware.SystemPerformanceCheck())
	ai.POST("/guidance/optimize", middleware.PrepareBelloBuddyGuidance(), middleware.TokenAuth(), middleware.ModelRequestRateLimit(), middleware.BelloBuddyAIChannelAccess(), middleware.Distribute(), middleware.BelloBuddyAISelectedChannel(), controller.RelayBelloBuddyAI)
	for _, operation := range []struct {
		path  string
		reply bool
	}{{"/screen", false}, {"/reply", true}} {
		ai.POST(operation.path, middleware.PrepareBelloBuddyAI(operation.reply), middleware.TokenAuth(), middleware.ModelRequestRateLimit(), middleware.BelloBuddyAIChannelAccess(), middleware.Distribute(), middleware.BelloBuddyAISelectedChannel(), controller.RelayBelloBuddyAI)
	}

	admin := routes.Group("/admin")
	admin.Use(middleware.AdminAuth(), func(c *gin.Context) {
		c.Set("bello_public_admin", true)
		c.Next()
	})
	admin.GET("/plans", middleware.RequirePermission(authz.BelloBuddyPlanRead), controller.ListBelloBuddyPlans)
	registerBelloBuddyReleaseAdminRoutes(admin)
	admin.GET("/plans/:id", middleware.RequirePermission(authz.BelloBuddyPlanRead), controller.GetBelloBuddyPlan)
	admin.POST("/plans", middleware.RequirePermission(authz.BelloBuddyPlanWrite), controller.SaveBelloBuddyPlan)
	admin.POST("/plans/import", middleware.RequirePermission(authz.BelloBuddyPlanWrite), controller.ImportBelloBuddyPlans)
	admin.PUT("/plans/:id", middleware.RequirePermission(authz.BelloBuddyPlanWrite), controller.SaveBelloBuddyPlan)
	admin.GET("/job-bindings", middleware.RequirePermission(authz.BelloBuddyBindingRead), controller.ListBelloBuddyJobBindings)
	admin.GET("/job-bindings/candidates", middleware.RequirePermission(authz.BelloBuddyBindingRead), controller.ListBelloBuddyJobCandidates)
	admin.POST("/job-bindings", middleware.RequirePermission(authz.BelloBuddyBindingWrite), controller.SaveBelloBuddyJobBinding)
	admin.PUT("/job-bindings/:id", middleware.RequirePermission(authz.BelloBuddyBindingWrite), controller.SaveBelloBuddyJobBinding)
	admin.PUT("/job-bindings/:id/jobs", middleware.RequirePermission(authz.BelloBuddyBindingWrite), controller.MapBelloBuddyJobsToBinding)
	admin.GET("/resume-archives", middleware.BelloBuddyResumeSession(), middleware.RequirePermission(authz.BelloBuddyResumeRead), controller.AdminListBelloBuddyResumeArchives)
	admin.POST("/resume-archives/:archiveId/abandon", middleware.BelloBuddyResumeSession(), middleware.RequirePermission(authz.BelloBuddyResumeWrite), controller.AbandonBelloBuddyResumeArchive)
}
