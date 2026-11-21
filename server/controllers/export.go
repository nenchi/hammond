package controllers

import (
	"bytes"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/akhilrex/hammond/common"
	"github.com/akhilrex/hammond/db"
	"github.com/akhilrex/hammond/models"
	"github.com/akhilrex/hammond/service"
	"github.com/gin-gonic/gin"
)

// RegisterExportController: CSV export per vehicle (Fuelly format, importable again) and the
// database backup download for admins. Both also work with ?access_token= for plain download links.
func RegisterExportController(router *gin.RouterGroup) {
	router.GET("/vehicles/:id/export", func(c *gin.Context) {
		if !service.UserCanAccessVehicle(c.MustGet("userId").(string), c.Param("id")) {
			c.JSON(http.StatusNotFound, gin.H{})
			return
		}
		data, name, err := service.ExportVehicleCSV(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
			return
		}
		c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
		c.Data(http.StatusOK, "text/csv; charset=utf-8", data)
	})
	router.GET("/backup", ShouldBeAdmin(), func(c *gin.Context) {
		var buf bytes.Buffer
		if err := service.DatabaseBackupGzip(&buf); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		name := "hammond-backup-" + time.Now().Format("2006-01-02_1504") + ".db.gz"
		c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
		c.Data(http.StatusOK, "application/gzip", buf.Bytes())
	})
}

// RegisterFuelPriceController: stations nearby with current prices (for the fill-up form), and the
// admin settings for the price provider (the API key is write-only).
func RegisterFuelPriceController(router *gin.RouterGroup) {
	router.GET("/vehicles/:id/stations", func(c *gin.Context) {
		userID := c.MustGet("userId").(string)
		if !service.UserCanAccessVehicle(userID, c.Param("id")) {
			c.JSON(http.StatusNotFound, gin.H{})
			return
		}
		lat, err1 := strconv.ParseFloat(c.Query("lat"), 64)
		lng, err2 := strconv.ParseFloat(c.Query("lng"), 64)
		if err1 != nil || err2 != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "lat and lng are required"})
			return
		}
		rad, _ := strconv.ParseFloat(c.DefaultQuery("rad", "2"), 64)
		vehicle, err := service.GetVehicleById(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{})
			return
		}
		if c.Query("grades") == "all" { // Price watch: any grade, not only the ones this vehicle takes
			v := *vehicle
			v.FuelGrade = strings.Join(service.Grades, ",")
			vehicle = &v
		}
		res, err := service.NearbyStations(userID, vehicle, lat, lng, rad, c.Query("profile"))
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, res)
	})
	// test one profile the user may use (own or shared; admins also the private site ones)
	router.GET("/settings/fuelPrices/test", func(c *gin.Context) {
		var lat, lng *float64 // empty = a fixed point in the profile's country
		if c.Query("lat") != "" || c.Query("lng") != "" {
			la, err1 := strconv.ParseFloat(c.Query("lat"), 64)
			ln, err2 := strconv.ParseFloat(c.Query("lng"), 64)
			if err1 != nil || err2 != nil {
				c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "invalid position"})
				return
			}
			lat, lng = &la, &ln
		}
		res, err := service.TestFuelPriceProvider(c.MustGet("userId").(string), c.Query("profile"), lat, lng)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"ok": false, "error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true, "stations": len(res.Stations), "sample": res.Stations, "attribution": res.Attribution})
	})
	router.POST("/settings/fuelPrices", ShouldBeAdmin(), func(c *gin.Context) {
		var req models.UpdateFuelPriceSettingsRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusUnprocessableEntity, common.NewValidatorError(err))
			return
		}
		if err := service.UpdatePriceProfiles(req.Profiles); err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, service.GetSettings(true))
	})
	// the user's own profiles (asked before the shared ones)
	router.GET("/me/fuelPrices", func(c *gin.Context) {
		c.JSON(http.StatusOK, service.UserPriceProfiles(c.MustGet("userId").(string)))
	})
	router.POST("/me/fuelPrices", func(c *gin.Context) {
		var req models.UpdateFuelPriceSettingsRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusUnprocessableEntity, common.NewValidatorError(err))
			return
		}
		userID := c.MustGet("userId").(string)
		if err := service.UpdateUserPriceProfiles(userID, req.Profiles); err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, service.UserPriceProfiles(userID))
	})
	// place search for "Stations nearby" without the device's location
	router.GET("/places", func(c *gin.Context) {
		list, err := service.SearchPlaces(c.Query("q"))
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, list)
	})
	// price watch: favourite stations, alerts to Gotify, check interval per price source
	router.GET("/me/priceWatches", listPriceWatches)
	router.POST("/me/priceWatches", func(c *gin.Context) {
		var req db.PriceWatch
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusUnprocessableEntity, common.NewValidatorError(err))
			return
		}
		w, err := service.CreatePriceWatch(c.MustGet("userId").(string), req)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, w)
	})
	router.PUT("/me/priceWatches/:id", func(c *gin.Context) {
		var req struct {
			Grade string   `json:"grade"`
			Below *float64 `json:"below"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusUnprocessableEntity, common.NewValidatorError(err))
			return
		}
		w, err := service.UpdatePriceWatch(c.MustGet("userId").(string), c.Param("id"), req.Grade, req.Below)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, w)
	})
	router.DELETE("/me/priceWatches/:id", func(c *gin.Context) {
		if err := service.DeletePriceWatch(c.MustGet("userId").(string), c.Param("id")); err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{})
	})
	router.GET("/me/priceWatches/:id/history", priceWatchHistory)
	router.POST("/me/priceWatches/check", func(c *gin.Context) {
		list, err := service.CheckPriceWatchesNow(c.MustGet("userId").(string))
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, list)
	})
	router.GET("/me/priceWatchSettings", func(c *gin.Context) {
		s, err := service.GetPriceWatchSettings(c.MustGet("userId").(string))
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, s)
	})
	router.POST("/me/priceWatchSettings", func(c *gin.Context) {
		var req struct {
			GotifyURL   string         `json:"gotifyUrl"`
			GotifyToken string         `json:"gotifyToken"`
			Intervals   map[string]int `json:"intervals"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusUnprocessableEntity, common.NewValidatorError(err))
			return
		}
		userID := c.MustGet("userId").(string)
		if err := service.UpdatePriceWatchSettings(userID, req.GotifyURL, req.GotifyToken, req.Intervals); err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
			return
		}
		s, _ := service.GetPriceWatchSettings(userID)
		c.JSON(http.StatusOK, s)
	})
	router.POST("/me/priceWatchSettings/test", func(c *gin.Context) {
		if err := service.TestGotify(c.MustGet("userId").(string)); err != nil {
			c.JSON(http.StatusOK, gin.H{"ok": false, "error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	// what the fill-up form asks, in order: own, then shared
	router.GET("/fuelPrices/available", func(c *gin.Context) {
		c.JSON(http.StatusOK, service.PublicAvailableProfiles(c.MustGet("userId").(string)))
	})
}

// RegisterHomeAssistantController: the car's current odometer for the fill-up form, and the user's own
// connection (the token is write-only).
func RegisterHomeAssistantController(router *gin.RouterGroup) {
	router.GET("/vehicles/:id/odometer", func(c *gin.Context) {
		if !service.UserCanAccessVehicle(c.MustGet("userId").(string), c.Param("id")) {
			c.JSON(http.StatusNotFound, gin.H{})
			return
		}
		vehicle, err := service.GetVehicleById(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{})
			return
		}
		odo, err := service.CurrentOdometer(c.MustGet("userId").(string), vehicle)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, odo)
	})
	router.GET("/me/homeAssistant/entity", func(c *gin.Context) {
		o, err := service.CheckHAEntity(c.MustGet("userId").(string), c.Query("entity"))
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, o)
	})
	router.GET("/me/homeAssistant/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, service.TestHomeAssistant(c.MustGet("userId").(string)))
	})
	router.POST("/me/homeAssistant", func(c *gin.Context) {
		var req models.UpdateHomeAssistantRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusUnprocessableEntity, common.NewValidatorError(err))
			return
		}
		userID := c.MustGet("userId").(string)
		if err := service.UpdateHomeAssistantSettings(userID, req.URL, req.Token); err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
			return
		}
		user, _ := service.GetUserById(userID)
		c.JSON(http.StatusOK, user)
	})
}

// also on /api/v1 (API key): the chat bot reads watched stations and their prices
func listPriceWatches(c *gin.Context) {
	list, err := service.ListPriceWatches(c.MustGet("userId").(string))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, list)
}

func priceWatchHistory(c *gin.Context) {
	days, _ := strconv.Atoi(c.DefaultQuery("days", "30"))
	if days < 1 || days > 3660 {
		days = 30
	}
	list, err := service.PriceWatchHistory(c.MustGet("userId").(string), c.Param("id"), time.Now().AddDate(0, 0, -days))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, list)
}
