package controllers

import (
	"net/http"
	"time"

	"github.com/akhilrex/hammond/db"
	"github.com/akhilrex/hammond/models"
	"github.com/akhilrex/hammond/service"
	"github.com/gin-gonic/gin"
)

// RegisterChargingController: charging places with their tariffs (admins edit, everyone reads), and
// the price of a charge at a place.
func RegisterChargingController(router *gin.RouterGroup) {
	router.GET("/chargingPlaces", func(c *gin.Context) {
		ps, err := service.ListChargingPlaces()
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, ps)
	})
	save := func(c *gin.Context) {
		var p db.ChargingPlace
		if err := c.ShouldBindJSON(&p); err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
			return
		}
		p.ID = c.Param("id") // "" on create
		saved, err := service.SaveChargingPlace(p)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, saved)
	}
	router.POST("/chargingPlaces", ShouldBeAdmin(), save)
	router.PUT("/chargingPlaces/:id", ShouldBeAdmin(), save)
	router.DELETE("/chargingPlaces/:id", ShouldBeAdmin(), func(c *gin.Context) {
		if err := service.DeleteChargingPlace(c.Param("id")); err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{})
	})
	// price of a charge: start, end (optional) and kWh as the car got them (the place's loss is added)
	router.POST("/chargingPlaces/:id/quote", func(c *gin.Context) {
		var req struct {
			Start time.Time  `json:"start" binding:"required"`
			End   *time.Time `json:"end"`
			KWh   float64    `json:"kWh" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
			return
		}
		end := req.Start
		if req.End != nil {
			end = *req.End
		}
		q, err := service.QuoteChargeAt(c.Param("id"), req.Start, end, req.KWh, true)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, q)
	})
}

// RegisterEvccController: admin settings for the EVCC import and "Sync now"
func RegisterEvccController(router *gin.RouterGroup) {
	router.POST("/settings/evcc", ShouldBeAdmin(), func(c *gin.Context) {
		var req models.UpdateEvccRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
			return
		}
		if err := service.UpdateEvccSettings(req); err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, service.GetSettings())
	})
	router.POST("/settings/evcc/sync", ShouldBeAdmin(), func(c *gin.Context) {
		res, err := service.SyncEvcc()
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, res)
	})
}
