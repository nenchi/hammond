package controllers

import (
	"net/http"
	"strings"

	"github.com/akhilrex/hammond/chat"
	"github.com/akhilrex/hammond/common"
	"github.com/akhilrex/hammond/db"
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
	router.GET("/vehicles/:id/gauge", func(c *gin.Context) {
		if !service.UserCanAccessVehicle(c.MustGet("userId").(string), c.Param("id")) {
			c.JSON(http.StatusNotFound, gin.H{})
			return
		}
		v, err := service.GetVehicleById(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{})
			return
		}
		c.JSON(http.StatusOK, service.Calibrate(v))
	})
	// litres per segment by hand ({"segmentLitres": [8, 10, 11, 14]}); null/[] = back to learned
	router.PUT("/vehicles/:id/gauge/manual", func(c *gin.Context) {
		if !service.UserCanAccessVehicle(c.MustGet("userId").(string), c.Param("id")) {
			c.JSON(http.StatusNotFound, gin.H{})
			return
		}
		var req struct {
			SegmentLitres []float64 `json:"segmentLitres"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusUnprocessableEntity, common.NewValidatorError(err))
			return
		}
		if len(req.SegmentLitres) == 0 {
			req.SegmentLitres = nil
		}
		if err := service.SetGaugeManual(c.Param("id"), req.SegmentLitres); err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
			return
		}
		v, _ := service.GetVehicleById(c.Param("id"))
		c.JSON(http.StatusOK, service.Calibrate(v))
	})
	router.POST("/vehicles/:id/gauge/reset", func(c *gin.Context) {
		if !service.UserCanAccessVehicle(c.MustGet("userId").(string), c.Param("id")) {
			c.JSON(http.StatusNotFound, gin.H{})
			return
		}
		if err := service.ResetGaugeCalibration(c.Param("id")); err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
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
		if id := chat.UserFromContext(c.Request.Context()); id != "" { // in-process call of the chat commands
			c.Set("userId", id)
			c.Next()
			return
		}
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
	// read-only, for the chat bot (homebot) and scripts
	router.GET("/me", func(c *gin.Context) {
		u, err := service.GetUserById(c.MustGet("userId").(string))
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"name": u.Name, "currency": u.Currency, "distanceUnit": u.DistanceUnit,
			"distanceUnitShort": db.DistanceUnitDetails[u.DistanceUnit].Short, "defaultVehicleId": u.DefaultVehicleID,
			"showTrips": u.ShowTrips})
	})
	router.GET("/vehicles/:id/stats", ownVehicle, getVehicleStats)
	router.GET("/vehicles/:id/fillups", ownVehicle, getFillupsByVehicleId)
	router.GET("/vehicles/:id/expenses", ownVehicle, getExpensesByVehicleId)
	router.GET("/vehicles/:id/mileage", ownVehicle, getMileageForVehicle)
	// a chat command as text (+ picture/file, base64): homebot on Matrix, scripts
	router.POST("/command", func(c *gin.Context) {
		var req struct {
			Text string `json:"text" binding:"required,max=500"`
		}
		if err := c.ShouldBindJSON(&req); !fail(c, err) {
			c.JSON(http.StatusOK, chat.Command(c.MustGet("userId").(string), req.Text))
		}
	})
	router.GET("/summary", func(c *gin.Context) { // dashboards: per vehicle and watched station (also sent over MQTT)
		s, err := service.GetSummary(c.MustGet("userId").(string))
		if !fail(c, err) {
			c.JSON(http.StatusOK, s)
		}
	})
	router.GET("/vehicles/:id/trips", ownVehicle, listTrips) // ?from=2026-11-01&to=2026-11-22
	router.GET("/vehicles/:id/trips.csv", ownVehicle, tripsCSV)
	router.GET("/me/priceWatches", listPriceWatches)
	router.GET("/me/priceWatches/:id/history", priceWatchHistory) // only the caller's own watches
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

// ownVehicle: 404 for a vehicle the API key's user can't access (the UI handlers it guards don't check)
func ownVehicle(c *gin.Context) {
	if !service.UserCanAccessVehicle(c.MustGet("userId").(string), c.Param("id")) {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "vehicle not found"})
	}
}
