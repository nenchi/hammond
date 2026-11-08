package controllers

import (
	"net/http"
	"strings"

	"github.com/akhilrex/hammond/common"
	"github.com/akhilrex/hammond/models"
	"github.com/akhilrex/hammond/service"
	"github.com/gin-gonic/gin"
)

// RegisterDetectionController: UI endpoints (logged-in user) for API keys and pending detections.
func RegisterDetectionController(router *gin.RouterGroup) {
	router.GET("/me/apikeys", func(c *gin.Context) {
		keys, err := service.ListApiKeys(c.MustGet("userId").(string))
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, common.NewError("apikeys", err))
			return
		}
		c.JSON(http.StatusOK, keys)
	})
	router.POST("/me/apikeys", func(c *gin.Context) {
		var req models.CreateApiKeyRequest
		if err := c.ShouldBind(&req); err != nil {
			c.JSON(http.StatusUnprocessableEntity, common.NewValidatorError(err))
			return
		}
		key, k, err := service.CreateApiKey(c.MustGet("userId").(string), req.Name)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, common.NewError("apikeys", err))
			return
		}
		c.JSON(http.StatusCreated, gin.H{"key": key, "apiKey": k}) // the only time the key is returned
	})
	router.DELETE("/me/apikeys/:id", func(c *gin.Context) {
		if err := service.DeleteApiKey(c.MustGet("userId").(string), c.Param("id")); err != nil {
			c.JSON(http.StatusNotFound, common.NewError("apikeys", err))
			return
		}
		c.JSON(http.StatusNoContent, gin.H{})
	})

	router.GET("/detections", func(c *gin.Context) {
		ds, err := service.GetPendingDetections(c.MustGet("userId").(string))
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, common.NewError("detections", err))
			return
		}
		c.JSON(http.StatusOK, ds)
	})
	router.POST("/detections/:id/discard", func(c *gin.Context) {
		if err := service.DiscardDetection(c.MustGet("userId").(string), c.Param("id")); err != nil {
			c.JSON(http.StatusNotFound, common.NewError("detections", err))
			return
		}
		c.JSON(http.StatusOK, gin.H{})
	})
	router.POST("/detections/:id/apply", func(c *gin.Context) {
		if err := service.ApplyDetection(c.MustGet("userId").(string), c.Param("id")); err != nil {
			c.JSON(http.StatusUnprocessableEntity, common.NewError("detections", err))
			return
		}
		c.JSON(http.StatusOK, gin.H{})
	})
	router.GET("/vehicles/:id/tankSuggestion", func(c *gin.Context) {
		if !service.UserCanAccessVehicle(c.MustGet("userId").(string), c.Param("id")) {
			c.JSON(http.StatusNotFound, gin.H{})
			return
		}
		size, samples := service.SuggestTankSize(c.Param("id"))
		c.JSON(http.StatusOK, gin.H{"tankCapacity": size, "samples": samples})
	})
}

// ApiKeyMiddleware authenticates /api/v1 with "Authorization: Bearer hmd_..." or "X-Api-Key: hmd_...".
// API keys work only here, never on the UI API.
func ApiKeyMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.GetHeader("X-Api-Key")
		if key == "" {
			key, _ = stripBearerPrefixFromTokenString(strings.TrimSpace(c.GetHeader("Authorization")))
		}
		userID := service.UserIDForApiKey(key)
		if userID == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing or invalid API key"})
			return
		}
		c.Set("userId", userID)
		c.Next()
	}
}

// RegisterApiV1 is the external API (Home Assistant, Node-RED, scripts), API keys only.
func RegisterApiV1(router *gin.RouterGroup) {
	router.Use(ApiKeyMiddleware())
	router.GET("/vehicles", func(c *gin.Context) {
		vs, err := service.GetUserVehicles(c.MustGet("userId").(string))
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, common.NewError("vehicles", err))
			return
		}
		out := []gin.H{}
		for _, v := range *vs {
			out = append(out, gin.H{"id": v.ID, "nickname": v.Nickname, "registration": v.Registration,
				"tankCapacity": v.TankCapacity, "fuelUnit": v.FuelUnitDetail().Short})
		}
		c.JSON(http.StatusOK, out)
	})
	router.POST("/vehicles/:id/detections", func(c *gin.Context) {
		userID, vehicleID := c.MustGet("userId").(string), c.Param("id")
		if !service.UserCanAccessVehicle(userID, vehicleID) {
			c.JSON(http.StatusNotFound, gin.H{"error": "vehicle not found"})
			return
		}
		var req models.CreateDetectionRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusUnprocessableEntity, common.NewValidatorError(err))
			return
		}
		res, err := service.CreateDetection(userID, vehicleID, req)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusCreated, gin.H{
			"result": res.Result, "message": res.Message, "detection": res.Detection,
			// open this path in hammond to complete it (for a notification's tap action)
			"path": "/detections",
		})
	})
}
