package controllers

import (
	"net/http"
	"strings"

	"github.com/akhilrex/hammond/db"
	"github.com/akhilrex/hammond/service"
	"github.com/gin-gonic/gin"
)

// AccessMiddleware (UI API, after AuthMiddleware): routes with an ID only reach the caller's own data.
// Upstream hammond checked none of them: any logged-in user could read or change another user's vehicle,
// fill-ups, expenses, quick entries and attachments by ID. Unknown and foreign IDs both answer 404.
func AccessMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, p := c.MustGet("userId").(string), c.FullPath()
		ok := true
		switch {
		case strings.HasPrefix(p, "/api/vehicles/:id"):
			ok = service.UserCanAccessVehicle(uid, c.Param("id"))
			if ok && strings.HasPrefix(p, "/api/vehicles/:id/fillups/:subId") {
				ok = exists(&db.Fillup{}, "id = ? AND vehicle_id = ?", c.Param("subId"), c.Param("id"))
			}
			if ok && strings.HasPrefix(p, "/api/vehicles/:id/expenses/:subId") {
				ok = exists(&db.Expense{}, "id = ? AND vehicle_id = ?", c.Param("subId"), c.Param("id"))
			}
		case strings.HasPrefix(p, "/api/quickEntries/:id"):
			ok = exists(&db.QuickEntry{}, "id = ? AND user_id = ?", c.Param("id"), uid)
		case p == "/api/attachments/:id/file":
			// own upload, or attached to a vehicle shared with the caller
			ok = exists(&db.Attachment{}, "id = ? AND user_id = ?", c.Param("id"), uid) ||
				exists(&db.VehicleAttachment{}, "attachment_id = ? AND vehicle_id IN (?)", c.Param("id"),
					db.DB.Model(&db.UserVehicle{}).Select("vehicle_id").Where("user_id = ?", uid))
		}
		if !ok {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.Next()
	}
}

func exists(model any, where string, args ...any) bool {
	var n int64
	db.DB.Model(model).Where(where, args...).Count(&n)
	return n > 0
}
