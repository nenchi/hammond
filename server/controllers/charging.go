package controllers

import (
	"net/http"
	"time"

	"github.com/akhilrex/hammond/db"
	"github.com/akhilrex/hammond/models"
	"github.com/akhilrex/hammond/service"
	"github.com/gin-gonic/gin"
)

// RegisterChargingController: charging places with their tariffs (own ones, and shared ones from
// admins), and the price of a charge at a place.
func RegisterChargingController(router *gin.RouterGroup) {
	router.GET("/chargingPlaces", func(c *gin.Context) {
		ps, err := service.ListChargingPlaces(c.MustGet("userId").(string))
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
		saved, err := service.SaveChargingPlace(c.MustGet("userId").(string), isAdmin(c), p)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, saved)
	}
	router.POST("/chargingPlaces", save)
	router.PUT("/chargingPlaces/:id", save)
	router.DELETE("/chargingPlaces/:id", func(c *gin.Context) {
		if err := service.DeleteChargingPlace(c.MustGet("userId").(string), isAdmin(c), c.Param("id")); err != nil {
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
		q, err := service.QuoteChargeAt(c.MustGet("userId").(string), c.Param("id"), req.Start, end, req.KWh, true)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, q)
	})
}

// RegisterEvccController: the user's own EVCC import and "Sync now"
func RegisterEvccController(router *gin.RouterGroup) {
	router.POST("/me/evcc", func(c *gin.Context) {
		var req models.UpdateEvccRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
			return
		}
		userID := c.MustGet("userId").(string)
		if err := service.UpdateEvccSettings(userID, req); err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
			return
		}
		user, _ := service.GetUserById(userID)
		c.JSON(http.StatusOK, user)
	})
	router.POST("/me/evcc/sync", func(c *gin.Context) {
		res, err := service.SyncEvcc(c.MustGet("userId").(string))
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, res)
	})
}
