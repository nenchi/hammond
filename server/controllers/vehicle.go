package controllers

import (
	"errors"
	"net/http"

	"github.com/akhilrex/hammond/common"
	"github.com/akhilrex/hammond/db"
	"github.com/akhilrex/hammond/models"
	"github.com/akhilrex/hammond/service"
	"github.com/gin-gonic/gin"
)

func RegisterVehicleController(router *gin.RouterGroup) {
	router.POST("/vehicles", createVehicle)
	router.GET("/vehicles", getAllVehicles)
	router.GET("/vehicles/:id", getVehicleById)
	router.PUT("/vehicles/:id", updateVehicle)
	router.DELETE("/vehicles/:id", deleteVehicle)
	router.PUT("/vehicles/:id/photo", putVehiclePhoto)
	router.PUT("/vehicles/:id/photoHidden", func(c *gin.Context) {
		var req struct {
			Hidden bool `json:"hidden"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusUnprocessableEntity, common.NewValidatorError(err))
			return
		}
		if err := db.DB.Model(&db.Vehicle{}).Where("id = ?", c.Param("id")).Update("photo_hidden", req.Hidden).Error; err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"hidden": req.Hidden})
	})
	router.DELETE("/vehicles/:id/photo", func(c *gin.Context) {
		if err := service.RemoveVehiclePhoto(c.Param("id")); err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{})
	})
	router.GET("/vehicles/:id/stats", getVehicleStats)
	router.GET("/vehicles/:id/users", getVehicleUsers)
	router.POST("/vehicles/:id/users/:subId", shareVehicle)
	router.DELETE("/vehicles/:id/users/:subId", unshareVehicle)
	router.POST("/vehicles/:id/users/:subId/transfer", transferVehicle)

	router.GET("/me/vehicles", getMyVehicles)
	router.GET("/me/stats", getMystats)

	router.GET("/vehicles/:id/fillups", getFillupsByVehicleId)
	router.GET("/vehicles/:id/fuelSubTypes", getFuelSubTypesByVehicleId)
	router.POST("/vehicles/:id/fillups", createFillup)
	router.GET("/vehicles/:id/fillups/:subId", getFillupById)
	router.PUT("/vehicles/:id/fillups/:subId", updateFillup)
	router.DELETE("/vehicles/:id/fillups/:subId", deleteFillup)

	router.GET("/vehicles/:id/expenses", getExpensesByVehicleId)
	router.POST("/vehicles/:id/expenses", createExpense)
	router.GET("/vehicles/:id/expenses/:subId", getExpenseById)
	router.PUT("/vehicles/:id/expenses/:subId", updateExpense)
	router.DELETE("/vehicles/:id/expenses/:subId", deleteExpense)

	router.POST("/vehicles/:id/attachments", createVehicleAttachment)
	router.GET("/vehicles/:id/attachments", getVehicleAttachments)
}

func createVehicle(c *gin.Context) {
	var request models.CreateVehicleRequest
	if err := c.ShouldBind(&request); err != nil {
		c.JSON(http.StatusUnprocessableEntity, common.NewValidatorError(err))
		return
	}
	vehicle, err := service.CreateVehicle(request, c.MustGet("userId").(string))
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, common.NewError("createVehicle", err))
		return
	}
	c.JSON(http.StatusCreated, vehicle)
}
func getVehicleById(c *gin.Context) {
	var searchByIdQuery models.SearchByIdQuery

	if c.ShouldBindUri(&searchByIdQuery) == nil {
		vehicle, err := service.GetVehicleById(searchByIdQuery.Id)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, common.NewError("getVehicleById", err))
			return
		}
		c.JSON(http.StatusOK, vehicle)
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
	}
}
func updateVehicle(c *gin.Context) {
	var searchByIdQuery models.SearchByIdQuery
	var updateVehicleModel models.UpdateVehicleRequest
	if err := c.ShouldBindUri(&searchByIdQuery); err == nil {
		if err := c.ShouldBind(&updateVehicleModel); err == nil {
			err := service.UpdateVehicle(searchByIdQuery.Id, updateVehicleModel)
			if err != nil {
				c.JSON(http.StatusUnprocessableEntity, common.NewError("getVehicleById", err))
				return
			}
			c.JSON(http.StatusOK, gin.H{})
		} else {
			c.JSON(http.StatusUnprocessableEntity, common.NewValidatorError(err))
		}
	} else {
		c.JSON(http.StatusUnprocessableEntity, common.NewValidatorError(err))
	}
}
func getAllVehicles(c *gin.Context) {
	vehicles, err := service.GetAllVehicles()
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, common.NewError("getVehicleById", err))
		return
	}
	c.JSON(200, vehicles)

}
func getMyVehicles(c *gin.Context) {
	vehicles, err := service.GetUserVehicles(c.MustGet("userId").(string))
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, common.NewError("getMyVehicles", err))
		return
	}
	c.JSON(200, vehicles)

}

func getFillupsByVehicleId(c *gin.Context) {

	var searchByIdQuery models.SearchByIdQuery

	if err := c.ShouldBindUri(&searchByIdQuery); err == nil {

		fillups, err := service.GetFillupsByVehicleId(searchByIdQuery.Id)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, common.NewError("getFillupsByVehicleId", err))
			return
		}
		c.JSON(http.StatusOK, fillups)
	} else {
		c.JSON(http.StatusUnprocessableEntity, common.NewValidatorError(err))
	}
}
func getFuelSubTypesByVehicleId(c *gin.Context) {

	var searchByIdQuery models.SearchByIdQuery

	if err := c.ShouldBindUri(&searchByIdQuery); err == nil {

		fuelSubtypes, err := service.GetDistinctFuelSubtypesForVehicle(searchByIdQuery.Id)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, common.NewError("getFuelSubTypesByVehicleId", err))
			return
		}
		c.JSON(http.StatusOK, fuelSubtypes)
	} else {
		c.JSON(http.StatusUnprocessableEntity, common.NewValidatorError(err))
	}
}

func getExpensesByVehicleId(c *gin.Context) {

	var searchByIdQuery models.SearchByIdQuery

	if err := c.ShouldBindUri(&searchByIdQuery); err == nil {

		data, err := service.GetExpensesByVehicleId(searchByIdQuery.Id)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, common.NewError("getExpensesByVehicleId", err))
			return
		}
		c.JSON(http.StatusOK, data)
	} else {
		c.JSON(http.StatusUnprocessableEntity, common.NewValidatorError(err))
	}
}

func createFillup(c *gin.Context) {
	var request models.CreateFillupRequest
	var searchByIdQuery models.SearchByIdQuery

	if err := c.ShouldBindUri(&searchByIdQuery); err == nil {
		if err := c.ShouldBind(&request); err != nil {
			c.JSON(http.StatusUnprocessableEntity, common.NewValidatorError(err))
			return
		}
		request.VehicleID = searchByIdQuery.Id // the vehicle of the URL (checked by AccessMiddleware), not the body's
		fillup, err := service.CreateFillup(request)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, common.NewError("createFillup", err))
			return
		}
		c.JSON(http.StatusCreated, fillup)
	} else {
		c.JSON(http.StatusUnprocessableEntity, common.NewValidatorError(err))
	}
}

func createExpense(c *gin.Context) {
	var request models.CreateExpenseRequest
	var searchByIdQuery models.SearchByIdQuery

	if err := c.ShouldBindUri(&searchByIdQuery); err == nil {
		if err := c.ShouldBind(&request); err != nil {
			c.JSON(http.StatusUnprocessableEntity, common.NewValidatorError(err))
			return
		}
		request.VehicleID = searchByIdQuery.Id
		expense, err := service.CreateExpense(request)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, common.NewError("createExpense", err))
			return
		}
		c.JSON(http.StatusCreated, expense)
	} else {
		c.JSON(http.StatusUnprocessableEntity, common.NewValidatorError(err))
	}
}

func updateExpense(c *gin.Context) {
	var searchByIdQuery models.SubItemQuery
	var updateExpenseModel models.UpdateExpenseRequest
	if err := c.ShouldBindUri(&searchByIdQuery); err == nil {
		if err := c.ShouldBind(&updateExpenseModel); err == nil {
			updateExpenseModel.VehicleID = searchByIdQuery.Id // can't move it to someone else's vehicle
			err := service.UpdateExpense(searchByIdQuery.SubId, updateExpenseModel)
			if err != nil {
				c.JSON(http.StatusUnprocessableEntity, common.NewError("getExpenseById", err))
				return
			}
			c.JSON(http.StatusOK, gin.H{})
		} else {
			c.JSON(http.StatusUnprocessableEntity, common.NewValidatorError(err))
		}
	} else {
		c.JSON(http.StatusUnprocessableEntity, common.NewValidatorError(err))
	}
}
func updateFillup(c *gin.Context) {
	var searchByIdQuery models.SubItemQuery
	var updateFillupModel models.UpdateFillupRequest
	if err := c.ShouldBindUri(&searchByIdQuery); err == nil {
		if err := c.ShouldBind(&updateFillupModel); err == nil {
			updateFillupModel.VehicleID = searchByIdQuery.Id
			err := service.UpdateFillup(searchByIdQuery.SubId, updateFillupModel)
			if err != nil {
				c.JSON(http.StatusUnprocessableEntity, common.NewError("getFillupById", err))
				return
			}
			c.JSON(http.StatusOK, gin.H{})
		} else {
			c.JSON(http.StatusUnprocessableEntity, common.NewValidatorError(err))
		}
	} else {
		c.JSON(http.StatusUnprocessableEntity, common.NewValidatorError(err))
	}
}

func deleteExpense(c *gin.Context) {
	var searchByIdQuery models.SubItemQuery

	if err := c.ShouldBindUri(&searchByIdQuery); err == nil {

		err := service.DeleteExpenseById(searchByIdQuery.SubId)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, common.NewError("getExpenseById", err))
			return
		}
		c.JSON(http.StatusOK, gin.H{})

	} else {
		c.JSON(http.StatusUnprocessableEntity, common.NewValidatorError(err))
	}
}
func deleteFillup(c *gin.Context) {
	var searchByIdQuery models.SubItemQuery

	if err := c.ShouldBindUri(&searchByIdQuery); err == nil {

		err := service.DeleteFillupById(searchByIdQuery.SubId)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, common.NewError("getFillupById", err))
			return
		}
		c.JSON(http.StatusOK, gin.H{})

	} else {
		c.JSON(http.StatusUnprocessableEntity, common.NewValidatorError(err))
	}
}

func getExpenseById(c *gin.Context) {
	var searchByIdQuery models.SubItemQuery

	if err := c.ShouldBindUri(&searchByIdQuery); err == nil {

		obj, err := service.GetExpenseById(searchByIdQuery.SubId)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, common.NewError("getExpenseById", err))
			return
		}
		c.JSON(http.StatusOK, obj)

	} else {
		c.JSON(http.StatusUnprocessableEntity, common.NewValidatorError(err))
	}
}
func getFillupById(c *gin.Context) {
	var searchByIdQuery models.SubItemQuery

	if err := c.ShouldBindUri(&searchByIdQuery); err == nil {

		obj, err := service.GetFillupById(searchByIdQuery.SubId)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, common.NewError("getFillupById", err))
			return
		}
		c.JSON(http.StatusOK, obj)

	} else {
		c.JSON(http.StatusUnprocessableEntity, common.NewValidatorError(err))
	}
}

func createVehicleAttachment(c *gin.Context) {
	var searchByIdQuery models.SearchByIdQuery
	var dataModel models.CreateVehicleAttachmentModel
	if err := c.ShouldBindUri(&searchByIdQuery); err == nil {
		if err := c.ShouldBind(&dataModel); err == nil {
			vehicle, err := service.GetVehicleById(searchByIdQuery.Id)
			if err != nil {
				c.JSON(http.StatusUnprocessableEntity, common.NewError("createVehicleAttachment", err))
				return
			}
			attachment, err := saveUploadedFile(c, "file")
			if err != nil {
				c.JSON(http.StatusUnprocessableEntity, common.NewError("createVehicleAttachment", err))
				return
			}
			err = service.CreateVehicleAttachment(vehicle.ID, attachment.ID, dataModel.Title)
			if err != nil {
				c.JSON(http.StatusUnprocessableEntity, common.NewError("createVehicleAttachment", err))
				return
			}
			c.JSON(http.StatusOK, gin.H{})
		} else {
			c.JSON(http.StatusUnprocessableEntity, common.NewValidatorError(err))
		}
	} else {
		c.JSON(http.StatusUnprocessableEntity, common.NewValidatorError(err))
	}
}

func getVehicleAttachments(c *gin.Context) {
	var searchByIdQuery models.SearchByIdQuery

	if err := c.ShouldBindUri(&searchByIdQuery); err == nil {

		vehicle, err := service.GetVehicleById(searchByIdQuery.Id)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, common.NewError("createVehicleAttachment", err))
			return
		}

		attachments, err := service.GetVehicleAttachments(vehicle.ID)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, common.NewError("createVehicleAttachment", err))
			return
		}
		c.JSON(http.StatusOK, attachments)

	} else {
		c.JSON(http.StatusUnprocessableEntity, common.NewValidatorError(err))
	}
}

func getVehicleUsers(c *gin.Context) {
	var searchByIdQuery models.SearchByIdQuery

	if err := c.ShouldBindUri(&searchByIdQuery); err == nil {

		vehicle, err := service.GetVehicleById(searchByIdQuery.Id)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, common.NewError("getVehicleUsers", err))
			return
		}
		data, err := service.GetVehicleUsers(vehicle.ID)

		var model []models.UserVehicleSimpleModel

		for _, item := range *data {
			model = append(model, models.UserVehicleSimpleModel{
				ID:        item.ID,
				UserID:    item.UserID,
				VehicleID: item.VehicleID,
				IsOwner:   item.IsOwner,
				Name:      item.User.Name,
			})
		}

		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, common.NewError("getVehicleUsers", err))
			return
		}

		c.JSON(http.StatusOK, model)

	} else {
		c.JSON(http.StatusUnprocessableEntity, common.NewValidatorError(err))
	}
}
func deleteVehicle(c *gin.Context) {
	var searchByIdQuery models.SearchByIdQuery

	if err := c.ShouldBindUri(&searchByIdQuery); err == nil {

		canDelete, err := service.CanDeleteVehicle(searchByIdQuery.Id, c.MustGet("userId").(string))
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, common.NewError("shareVehicle", err))
			return
		}
		if !canDelete {
			c.JSON(http.StatusUnprocessableEntity, common.NewError("shareVehicle", errors.New("you are not allowed to delete this vehicle")))
			return
		}
		err = service.DeleteVehicle(searchByIdQuery.Id)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, common.NewError("shareVehicle", err))
			return
		}
		c.JSON(http.StatusOK, gin.H{})

	} else {
		c.JSON(http.StatusUnprocessableEntity, common.NewValidatorError(err))
	}
}
func shareVehicle(c *gin.Context) {
	var searchByIdQuery models.SubItemQuery

	if err := c.ShouldBindUri(&searchByIdQuery); err == nil {

		err := service.ShareVehicle(searchByIdQuery.Id, searchByIdQuery.SubId)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, common.NewError("shareVehicle", err))
			return
		}
		c.JSON(http.StatusOK, gin.H{})

	} else {
		c.JSON(http.StatusUnprocessableEntity, common.NewValidatorError(err))
	}
}
func transferVehicle(c *gin.Context) {
	var searchByIdQuery models.SubItemQuery

	if err := c.ShouldBindUri(&searchByIdQuery); err == nil {

		err := service.TransferVehicle(searchByIdQuery.Id, c.MustGet("userId").(string), searchByIdQuery.SubId)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, common.NewError("shareVehicle", err))
			return
		}
		c.JSON(http.StatusOK, gin.H{})

	} else {
		c.JSON(http.StatusUnprocessableEntity, common.NewValidatorError(err))
	}
}
func unshareVehicle(c *gin.Context) {
	var searchByIdQuery models.SubItemQuery

	if err := c.ShouldBindUri(&searchByIdQuery); err == nil {

		err := service.UnshareVehicle(searchByIdQuery.Id, searchByIdQuery.SubId)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, common.NewError("shareVehicle", err))
			return
		}
		c.JSON(http.StatusOK, gin.H{})

	} else {
		c.JSON(http.StatusUnprocessableEntity, common.NewValidatorError(err))
	}
}

func getVehicleStats(c *gin.Context) {
	var searchByIdQuery models.SearchByIdQuery

	if err := c.ShouldBindUri(&searchByIdQuery); err == nil {

		vehicle, err := service.GetVehicleById(searchByIdQuery.Id)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, common.NewError("getVehicleState", err))
			return
		}

		model := models.VehicleStatsModel{}

		c.JSON(http.StatusOK, model.SetStats(&vehicle.Fillups, &vehicle.Expenses))

	} else {
		c.JSON(http.StatusUnprocessableEntity, common.NewValidatorError(err))
	}
}

func getMystats(c *gin.Context) {
	var model models.UserStatsQueryModel
	if err := c.ShouldBind(&model); err == nil {

		stats, err := service.GetUserStats(c.MustGet("userId").(string), model)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, common.NewError("getMyVehicles", err))
			return
		}
		c.JSON(200, stats)
	} else {
		c.JSON(http.StatusUnprocessableEntity, common.NewValidatorError(err))
	}

}

// a car photo from the phone camera or a file: an image of at most 15 MB; "origin" = the address the
// browser uses, for Home Assistant's link
func putVehiclePhoto(c *gin.Context) {
	f, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "choose a photo"})
		return
	}
	if f.Size > 15<<20 {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "the photo is larger than 15 MB"})
		return
	}
	r, err := f.Open()
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}
	head := make([]byte, 512)
	n, _ := r.Read(head)
	r.Close()
	kind := http.DetectContentType(head[:n]) // the bytes, not the browser's claim: only real images
	if kind != "image/jpeg" && kind != "image/png" && kind != "image/webp" && kind != "image/gif" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "not a JPEG, PNG, WebP or GIF image"})
		return
	}
	a, err := saveUploadedFile(c, "file")
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}
	db.DB.Model(a).Update("content_type", kind)
	link, err := service.SetVehiclePhoto(c.Param("id"), a, c.PostForm("origin"))
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"photoUrl": link})
}
