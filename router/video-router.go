package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"

	"github.com/gin-gonic/gin"
)

func SetVideoRouter(router *gin.Engine) {
	// Video proxy: accepts either session auth (dashboard) or token auth (API clients)
	videoProxyRouter := router.Group("/v1")
	videoProxyRouter.Use(middleware.RouteTag("relay"))
	videoProxyRouter.Use(middleware.TokenOrUserAuth())
	{
		videoProxyRouter.GET("/videos/:task_id/content", controller.VideoProxy)
	}

	videoV1Router := router.Group("/v1")
	videoV1Router.Use(middleware.RouteTag("relay"))
	videoV1Router.Use(middleware.TokenAuth(), middleware.Distribute())
	{
		videoV1Router.POST("/video/generations", controller.RelayTask)
		videoV1Router.GET("/video/generations/:task_id", controller.RelayTaskFetch)
		videoV1Router.POST("/videos/:video_id/remix", controller.RelayTask)
	}
	// openai compatible API video routes
	// docs: https://platform.openai.com/docs/api-reference/videos/create
	{
		videoV1Router.POST("/videos", controller.RelayTask)
		videoV1Router.GET("/videos/:task_id", controller.RelayTaskFetch)
	}

	klingV1Router := router.Group("/kling/v1")
	klingV1Router.Use(middleware.RouteTag("relay"))
	klingV1Router.Use(middleware.KlingRequestConvert(), middleware.TokenAuth(), middleware.Distribute())
	{
		klingV1Router.POST("/videos/text2video", controller.RelayTask)
		klingV1Router.POST("/videos/image2video", controller.RelayTask)
		klingV1Router.GET("/videos/text2video/:task_id", controller.RelayTaskFetch)
		klingV1Router.GET("/videos/image2video/:task_id", controller.RelayTaskFetch)
	}

	// Jimeng official API routes - direct mapping to official API format
	jimengOfficialGroup := router.Group("jimeng")
	jimengOfficialGroup.Use(middleware.RouteTag("relay"))
	jimengOfficialGroup.Use(middleware.JimengRequestConvert(), middleware.TokenAuth(), middleware.Distribute())
	{
		// Maps to: /?Action=CVSync2AsyncSubmitTask&Version=2022-08-31 and /?Action=CVSync2AsyncGetResult&Version=2022-08-31
		jimengOfficialGroup.POST("/", controller.RelayTask)
	}

	// 穗禾视频渠道：标准生成接口 + 画质增强 + 任务详情都进入计费 / 任务表，
	// 走 RelayTask 通路；其他辅助接口（余额 / 流水 / 模型 / 渠道 / 素材库 / 健康检查）
	// 由 SuiheProxy 直转上游，不计费、不写本地任务表。
	suiheGroup := router.Group("/suihe/v1")
	suiheGroup.Use(middleware.RouteTag("relay"))
	{
		// 视频生成（计费、入任务表）
		genGroup := suiheGroup.Group("")
		genGroup.Use(middleware.TokenAuth(), middleware.Distribute())
		genGroup.POST("/videos/generations", controller.RelayTask)

		// 画质增强：先用 SuiheEnhanceConvert 反查源任务并锁定渠道，再走 RelayTask
		enhanceGroup := suiheGroup.Group("")
		enhanceGroup.Use(middleware.TokenAuth(), middleware.SuiheEnhanceConvert(), middleware.Distribute())
		enhanceGroup.POST("/videos/enhance", controller.RelayTask)

		// 任务详情：从本地任务表查询并按穗禾结构返回（task_xxxx 公开 ID）
		fetchGroup := suiheGroup.Group("")
		fetchGroup.Use(middleware.TokenAuth())
		fetchGroup.GET("/tasks/:task_id", controller.SuiheTaskDetail)

		// 反向代理：余额 / 流水 / 模型 / 渠道
		proxyGroup := suiheGroup.Group("")
		proxyGroup.Use(middleware.TokenAuth())
		proxyGroup.POST("/tasks/list", controller.SuiheTasksList)
		proxyGroup.GET("/balance", controller.SuiheBalance)
		proxyGroup.GET("/transactions", controller.SuiheTransactions)
		proxyGroup.GET("/models", controller.SuiheModels)
		proxyGroup.GET("/channels", controller.SuiheChannels)

		// 素材库：用户自管上传 / 列表 / 删除（每个穗禾渠道对应独立 API Key，天然隔离）
		assetGroup := suiheGroup.Group("/asset-library")
		assetGroup.Use(middleware.TokenAuth())
		assetGroup.POST("/assets", controller.SuiheAssetUpload)
		assetGroup.POST("/assets/list", controller.SuiheAssetList)
		assetGroup.DELETE("/assets/:id", controller.SuiheAssetDelete)
	}

	// 穗禾健康检查：仅校验 token 即可（绑定 Key 时使用）
	suiheHealthGroup := router.Group("/suihe")
	suiheHealthGroup.Use(middleware.RouteTag("relay"))
	suiheHealthGroup.Use(middleware.TokenAuth())
	{
		suiheHealthGroup.GET("/health", controller.SuiheHealth)
	}
}
