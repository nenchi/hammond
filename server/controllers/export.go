package controllers

import (
	"bytes"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/akhilrex/hammond/common"
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
		res, err := service.NearbyStations(vehicle, lat, lng, rad)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, res)
	})
	router.POST("/settings/fuelPrices", ShouldBeAdmin(), func(c *gin.Context) {
		var req models.UpdateFuelPriceSettingsRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusUnprocessableEntity, common.NewValidatorError(err))
			return
		}
		if err := service.UpdateFuelPriceSettings(req.Provider, req.URL, req.APIKey); err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, service.GetSettings())
	})
}
