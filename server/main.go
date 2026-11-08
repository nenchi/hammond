package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/akhilrex/hammond/controllers"
	"github.com/akhilrex/hammond/db"
	"github.com/akhilrex/hammond/service"
	"github.com/gin-contrib/location/v2"
	"github.com/gin-contrib/static"
	"github.com/gin-gonic/gin"
	_ "github.com/joho/godotenv/autoload"
)

func main() {
	var err error
	db.DB, err = db.Init()
	if err != nil {
		fmt.Println("status: ", err)
	} else {
		db.Migrate()
	}
	r := gin.Default()

	r.Use(setupSettings())
	r.Use(gin.Recovery())
	r.Use(location.Default())
	r.Use(static.Serve("/", static.LocalFile("./dist", true)))
	r.NoRoute(func(c *gin.Context) {
		//fmt.'Println(c.Request.URL.Path)
		c.File("dist/index.html")
	})
	// external API (API keys), registered before the UI API's JWT middleware
	controllers.RegisterApiV1(r.Group("/api/v1"))
	router := r.Group("/api")

	dataPath := os.Getenv("DATA")

	router.Static("/assets/", dataPath)

	controllers.RegisterAnonController(router)
	controllers.RegisterAnonMasterConroller(router)
	controllers.RegisterSetupController(router)

	router.Use(controllers.AuthMiddleware(true))
	controllers.RegisterUserController(router)
	controllers.RegisterMastersController(router)
	controllers.RegisterAuthController(router)
	controllers.RegisterVehicleController(router)
	controllers.RegisterFilesController(router)
	controllers.RegisteImportController(router)
	controllers.RegisterReportsController(router)
	controllers.RegisterDetectionController(router)

	go assetEnv()
	go intiCron()

	r.Run(":3000") // listen and serve on 0.0.0.0:8080 (for windows "localhost:8080")

}
func setupSettings() gin.HandlerFunc {
	return func(c *gin.Context) {

		setting := db.GetOrCreateSetting()
		c.Set("setting", setting)
		c.Writer.Header().Set("X-Clacks-Overhead", "GNU Terry Pratchett")

		c.Next()
	}
}

func intiCron() {

	// ponytail: stdlib ticker replaces the abandoned gocron; same schedule (every 2 days, first run 2 days after start)
	for range time.Tick(48 * time.Hour) {
		service.CreateBackup()
	}
}

func assetEnv() {
	log.Println("Config Dir: ", os.Getenv("CONFIG"))
	log.Println("Assets Dir: ", os.Getenv("DATA"))
}
