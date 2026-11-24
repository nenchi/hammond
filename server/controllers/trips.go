package controllers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/akhilrex/hammond/db"
	"github.com/akhilrex/hammond/service"
	"github.com/gin-gonic/gin"
)

// tripRange: ?from=2026-11-01&to=2026-11-22 (days, local time; to inclusive), default the last 30 days
func tripRange(c *gin.Context) (time.Time, time.Time) {
	now := time.Now()
	day := func(s string) (time.Time, bool) {
		t, err := time.ParseInLocation("2006-01-02", s, time.Local)
		return t, err == nil
	}
	to := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.Local)
	if t, ok := day(c.Query("to")); ok {
		to = t.AddDate(0, 0, 1)
	}
	from := to.AddDate(0, 0, -30)
	if t, ok := day(c.Query("from")); ok {
		from = t
	}
	return from, to
}

func listTrips(c *gin.Context) {
	from, to := tripRange(c)
	trips, err := service.ListTrips(c.MustGet("userId").(string), c.Param("id"), from, to)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, trips)
}

func tripsCSV(c *gin.Context) {
	from, to := tripRange(c)
	trips, err := service.ListTrips(c.MustGet("userId").(string), c.Param("id"), from, to)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}
	cur := ""
	if u, err := service.GetUserById(c.MustGet("userId").(string)); err == nil {
		cur = u.Currency
	}
	c.Header("Content-Disposition", `attachment; filename="trips-`+from.Format("2006-01-02")+`-`+to.AddDate(0, 0, -1).Format("2006-01-02")+`.csv"`)
	c.Header("Content-Type", "text/csv; charset=utf-8")
	service.TripsCSV(c.Writer, trips, cur)
}

// ownTrip / ownLeg: 404 unless the trip's (leg's) vehicle is the user's
func ownTrip(c *gin.Context) {
	if v := service.TripVehicle(c.Param("tripId")); v == "" || !service.UserCanAccessVehicle(c.MustGet("userId").(string), v) {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "trip not found"})
	}
}

func ownLeg(c *gin.Context) {
	l, ok := service.LegVehicle(c.Param("legId"))
	if !ok || !service.UserCanAccessVehicle(c.MustGet("userId").(string), l.VehicleID) {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "drive not found"})
		return
	}
	c.Set("leg", l)
}

// kmAllowed: times and kilometres are locked for non-admins when the admin turned it on
func kmAllowed(c *gin.Context) {
	if service.TripsLocked() && !isAdmin(c) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "trip times and kilometres are locked by the admin"})
	}
}

func fail(c *gin.Context, err error) bool {
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
	}
	return err != nil
}

// RegisterTripController: the trip log (UI, logged-in user)
func RegisterTripController(router *gin.RouterGroup) {
	router.GET("/vehicles/:id/trips", ownVehicle, listTrips)
	router.GET("/vehicles/:id/trips.csv", ownVehicle, tripsCSV)
	router.POST("/vehicles/:id/trips/sync", ownVehicle, func(c *gin.Context) {
		n, err := service.SyncTrips(c.Param("id"))
		if !fail(c, err) {
			c.JSON(http.StatusOK, gin.H{"added": n})
		}
	})
	router.PUT("/vehicles/:id/tripEntities", ownVehicle, func(c *gin.Context) {
		var e db.TripEntities
		if err := c.ShouldBindJSON(&e); !fail(c, err) && !fail(c, service.SetTripEntities(c.Param("id"), e)) {
			c.JSON(http.StatusOK, e)
		}
	})
	// automatic recording on/off; with trips locked only admins (a pause would leave gaps in the log)
	router.PUT("/vehicles/:id/tripsPaused", ownVehicle, kmAllowed, func(c *gin.Context) {
		var req struct {
			Paused bool `json:"paused"`
		}
		if err := c.ShouldBindJSON(&req); !fail(c, err) && !fail(c, service.SetTripsPaused(c.Param("id"), req.Paused)) {
			c.JSON(http.StatusOK, gin.H{"paused": req.Paused})
		}
	})
	// the weekdays to record on ("1111100" = working days); with trips locked only admins
	router.PUT("/vehicles/:id/tripDays", ownVehicle, kmAllowed, func(c *gin.Context) {
		var req struct {
			Days string `json:"days"`
		}
		if err := c.ShouldBindJSON(&req); !fail(c, err) && !fail(c, service.SetTripDays(c.Param("id"), req.Days)) {
			c.JSON(http.StatusOK, gin.H{"days": req.Days})
		}
	})
	// a trip by hand: road distance, driving time and route between two points (from=lat,lng&to=lat,lng)
	router.GET("/route", func(c *gin.Context) {
		var p [4]float64
		_, e1 := fmt.Sscanf(c.Query("from"), "%f,%f", &p[0], &p[1])
		_, e2 := fmt.Sscanf(c.Query("to"), "%f,%f", &p[2], &p[3])
		if e1 != nil || e2 != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "from and to: lat,lng"})
			return
		}
		r, err := service.RouteBetween(p[0], p[1], p[2], p[3])
		if !fail(c, err) {
			c.JSON(http.StatusOK, r)
		}
	})
	router.POST("/vehicles/:id/trips", ownVehicle, kmAllowed, func(c *gin.Context) {
		var req struct {
			service.LegInput
			Business bool   `json:"business"`
			Purpose  string `json:"purpose" binding:"max=200"`
		}
		if err := c.ShouldBindJSON(&req); fail(c, err) {
			return
		}
		id, err := service.AddTrip(c.MustGet("userId").(string), c.Param("id"), req.LegInput, req.Business, req.Purpose)
		if !fail(c, err) {
			c.JSON(http.StatusCreated, gin.H{"id": id})
		}
	})
	router.PUT("/trips/:tripId", ownTrip, func(c *gin.Context) {
		var req struct {
			Business bool   `json:"business"`
			Purpose  string `json:"purpose" binding:"max=200"`
			Notes    string `json:"notes" binding:"max=2000"`
		}
		if err := c.ShouldBindJSON(&req); !fail(c, err) && !fail(c, service.UpdateTrip(c.Param("tripId"), req.Business, req.Purpose, req.Notes)) {
			c.JSON(http.StatusOK, gin.H{})
		}
	})
	router.POST("/trips/:tripId/join", ownTrip, func(c *gin.Context) {
		if !fail(c, service.JoinTrip(c.Param("tripId"))) {
			c.JSON(http.StatusOK, gin.H{})
		}
	})
	router.DELETE("/trips/:tripId", ownTrip, kmAllowed, func(c *gin.Context) {
		if !fail(c, service.DeleteTrip(c.Param("tripId"))) {
			c.JSON(http.StatusOK, gin.H{})
		}
	})
	router.POST("/trips/:tripId/costs", ownTrip, func(c *gin.Context) {
		var req struct {
			Amount  float64 `json:"amount" binding:"gt=0"`
			Type    string  `json:"type" binding:"required,max=50"`
			Comment string  `json:"comment" binding:"max=500"`
		}
		if err := c.ShouldBindJSON(&req); fail(c, err) {
			return
		}
		id, err := service.AddTripCost(c.MustGet("userId").(string), c.Param("tripId"), req.Amount, req.Type, req.Comment)
		if !fail(c, err) {
			c.JSON(http.StatusCreated, gin.H{"id": id})
		}
	})
	router.POST("/legs/:legId/split", ownLeg, func(c *gin.Context) {
		id, err := service.SplitTrip(c.MustGet("leg").(db.TripLeg))
		if !fail(c, err) {
			c.JSON(http.StatusOK, gin.H{"id": id})
		}
	})
	router.PUT("/legs/:legId", ownLeg, kmAllowed, func(c *gin.Context) {
		var in service.LegInput
		if err := c.ShouldBindJSON(&in); !fail(c, err) && !fail(c, service.UpdateLeg(c.Param("legId"), in)) {
			c.JSON(http.StatusOK, gin.H{})
		}
	})
	// the car the chat commands use by default ("" = ask when there are several)
	router.POST("/me/defaultVehicle", func(c *gin.Context) {
		var req struct {
			VehicleID string `json:"vehicleId"`
		}
		userID := c.MustGet("userId").(string)
		if err := c.ShouldBindJSON(&req); fail(c, err) {
			return
		}
		if req.VehicleID != "" && !service.UserCanAccessVehicle(userID, req.VehicleID) {
			c.JSON(http.StatusNotFound, gin.H{"error": "vehicle not found"})
			return
		}
		if !fail(c, db.DB.Model(&db.User{}).Where("id = ?", userID).Update("default_vehicle_id", req.VehicleID).Error) {
			c.JSON(http.StatusOK, gin.H{"defaultVehicleId": req.VehicleID})
		}
	})
	router.POST("/me/tripSettings", func(c *gin.Context) {
		var req struct {
			MergeMinutes int `json:"mergeMinutes" binding:"min=0,max=1440"`
		}
		if err := c.ShouldBindJSON(&req); !fail(c, err) && !fail(c, service.SetTripMergeMinutes(c.MustGet("userId").(string), req.MergeMinutes)) {
			c.JSON(http.StatusOK, gin.H{})
		}
	})
	// MQTT → Home Assistant (Settings → Connections)
	router.POST("/me/mqtt", func(c *gin.Context) {
		var req struct {
			URL      string `json:"url"`
			User     string `json:"user"`
			Password string `json:"password"`
			Prefix   string `json:"prefix"`
		}
		userID := c.MustGet("userId").(string)
		if err := c.ShouldBindJSON(&req); fail(c, err) || fail(c, service.UpdateMqttSettings(userID, req.URL, req.User, req.Password, req.Prefix)) {
			return
		}
		u, err := service.GetUserById(userID)
		if !fail(c, err) {
			c.JSON(http.StatusOK, u)
		}
	})
	router.POST("/me/mqtt/publish", func(c *gin.Context) {
		r, err := service.PublishMqtt(c.MustGet("userId").(string))
		if !fail(c, err) {
			c.JSON(http.StatusOK, r)
		}
	})
	// notification channels (Settings → Notifications): Gotify, ntfy, Matrix, Telegram
	router.GET("/me/notifications", func(c *gin.Context) {
		list, err := service.ListChannels(c.MustGet("userId").(string))
		if !fail(c, err) {
			c.JSON(http.StatusOK, list)
		}
	})
	router.POST("/me/notifications", func(c *gin.Context) {
		var in service.ChannelInput
		if err := c.ShouldBindJSON(&in); fail(c, err) {
			return
		}
		ch, err := service.CreateChannel(c.MustGet("userId").(string), in)
		if !fail(c, err) {
			c.JSON(http.StatusCreated, ch)
		}
	})
	router.PUT("/me/notifications/:cid", func(c *gin.Context) {
		var in service.ChannelInput
		if err := c.ShouldBindJSON(&in); fail(c, err) {
			return
		}
		ch, err := service.UpdateChannel(c.MustGet("userId").(string), c.Param("cid"), in)
		if !fail(c, err) {
			c.JSON(http.StatusOK, ch)
		}
	})
	router.DELETE("/me/notifications/:cid", func(c *gin.Context) {
		if err := service.DeleteChannel(c.MustGet("userId").(string), c.Param("cid")); err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{})
	})
	router.POST("/me/notifications/:cid/test", func(c *gin.Context) {
		if err := service.TestChannel(c.MustGet("userId").(string), c.Param("cid")); err != nil {
			c.JSON(http.StatusOK, gin.H{"ok": false, "error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	// the token of a Matrix bot account from username + password; nothing is stored here
	router.POST("/me/notifications/matrixLogin", func(c *gin.Context) {
		var req struct {
			Homeserver string `json:"homeserver"`
			User       string `json:"user"`
			Password   string `json:"password"`
		}
		if err := c.ShouldBindJSON(&req); fail(c, err) {
			return
		}
		token, user, err := service.MatrixLogin(req.Homeserver, req.User, req.Password)
		if !fail(c, err) {
			c.JSON(http.StatusOK, gin.H{"token": token, "userId": user})
		}
	})
	router.POST("/me/notifications/:cid/unlink", func(c *gin.Context) {
		if !fail(c, service.UnlinkChannel(c.MustGet("userId").(string), c.Param("cid"))) {
			c.JSON(http.StatusOK, gin.H{})
		}
	})
	router.POST("/settings/trips", ShouldBeAdmin(), func(c *gin.Context) {
		var req struct {
			Locked bool `json:"locked"`
		}
		if err := c.ShouldBindJSON(&req); !fail(c, err) && !fail(c, service.SetTripsLocked(req.Locked)) {
			c.JSON(http.StatusOK, gin.H{})
		}
	})
}
