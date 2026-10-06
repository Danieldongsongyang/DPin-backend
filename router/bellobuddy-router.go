package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
)

func registerBelloBuddyRoutes(apiRouter *gin.RouterGroup) {
	routes := apiRouter.Group("/bellobuddy")
	routes.Use(middleware.DisableCache(), middleware.UserAuth(), middleware.BelloBuddySessionRequired())
	routes.GET("/capabilities", controller.GetBelloBuddyCapabilities)
	routes.GET("/plans", controller.ListBelloBuddyPlans)
	routes.GET("/plans/:id", controller.GetBelloBuddyPlan)
	routes.POST("/plans", controller.SaveBelloBuddyPlan)
	routes.PUT("/plans/:id", controller.SaveBelloBuddyPlan)
	routes.POST("/accounts/:accountKey/jobs/sync", controller.SyncBelloBuddyJobs)
	routes.GET("/accounts/:accountKey/jobs/config", controller.GetBelloBuddyJobs)
	routes.PUT("/accounts/:accountKey/jobs/:jobId/config", controller.SaveBelloBuddyJobConfig)
	routes.PUT("/accounts/:accountKey/jobs/config/batch", controller.SaveBelloBuddyJobConfigBatch)
	routes.POST("/accounts/:accountKey/runtime-config", controller.GetBelloBuddyRuntimeConfig)

	ai := routes.Group("/ai")
	ai.Use(middleware.RouteTag("relay"), middleware.BodyStorageCleanup(), controller.BelloBuddyAIResponse(), middleware.SystemPerformanceCheck())
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
	admin.GET("/plans/:id", middleware.RequirePermission(authz.BelloBuddyPlanRead), controller.GetBelloBuddyPlan)
	admin.POST("/plans", middleware.RequirePermission(authz.BelloBuddyPlanWrite), controller.SaveBelloBuddyPlan)
	admin.POST("/plans/import", middleware.RequirePermission(authz.BelloBuddyPlanWrite), controller.ImportBelloBuddyPlans)
	admin.PUT("/plans/:id", middleware.RequirePermission(authz.BelloBuddyPlanWrite), controller.SaveBelloBuddyPlan)
	admin.GET("/job-bindings", middleware.RequirePermission(authz.BelloBuddyBindingRead), controller.ListBelloBuddyJobBindings)
	admin.GET("/job-bindings/candidates", middleware.RequirePermission(authz.BelloBuddyBindingRead), controller.ListBelloBuddyJobCandidates)
	admin.POST("/job-bindings", middleware.RequirePermission(authz.BelloBuddyBindingWrite), controller.SaveBelloBuddyJobBinding)
	admin.PUT("/job-bindings/:id", middleware.RequirePermission(authz.BelloBuddyBindingWrite), controller.SaveBelloBuddyJobBinding)
	admin.PUT("/job-bindings/:id/jobs", middleware.RequirePermission(authz.BelloBuddyBindingWrite), controller.MapBelloBuddyJobsToBinding)
}
