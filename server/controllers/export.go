package controllers

import (
	"bytes"
	"fmt"
	"net/http"
	"time"

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
