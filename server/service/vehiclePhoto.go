package service

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"os"

	"github.com/akhilrex/hammond/db"
)

// PhotoLink: the vehicle's photo link, relative ("" = none); absolute = with the upload's origin (for HA)
func PhotoLink(v *db.Vehicle, absolute bool) string {
	if v.PhotoToken == "" {
		return ""
	}
	if absolute {
		if v.PhotoOrigin == "" {
			return ""
		}
		return v.PhotoOrigin + db.PhotoPath + v.PhotoToken
	}
	return db.PhotoPath + v.PhotoToken
}

// cleanOrigin: "https://fuel.example.com" from what the browser sent; "" when it isn't a plain origin
func cleanOrigin(o string) string {
	u, err := url.Parse(o)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// SetVehiclePhoto: the uploaded file (already saved as an attachment) becomes the vehicle's photo, with a new
// random link; the old photo is deleted
func SetVehiclePhoto(vehicleID string, a *db.Attachment, origin string) (string, error) {
	v, err := db.GetVehicleById(vehicleID)
	if err != nil {
		return "", err
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	old := v.PhotoID
	tok := hex.EncodeToString(b)
	if err := db.DB.Model(&db.Vehicle{}).Where("id = ?", vehicleID).
		Updates(map[string]any{"photo_id": a.ID, "photo_token": tok, "photo_origin": cleanOrigin(origin)}).Error; err != nil {
		return "", err
	}
	deleteAttachment(old)
	return db.PhotoPath + tok, nil
}

func RemoveVehiclePhoto(vehicleID string) error {
	v, err := db.GetVehicleById(vehicleID)
	if err != nil {
		return err
	}
	if err := db.DB.Model(&db.Vehicle{}).Where("id = ?", vehicleID).
		Updates(map[string]any{"photo_id": "", "photo_token": "", "photo_origin": ""}).Error; err != nil {
		return err
	}
	deleteAttachment(v.PhotoID)
	return nil
}

// PhotoFile: the file behind a photo link (path, content type)
func PhotoFile(token string) (string, string, error) {
	if len(token) != 32 {
		return "", "", errors.New("not found")
	}
	var v db.Vehicle
	if db.DB.Where("photo_token = ?", token).Limit(1).Find(&v); v.ID == "" || v.PhotoID == "" {
		return "", "", errors.New("not found")
	}
	a, err := db.GetAttachmentById(v.PhotoID)
	if err != nil {
		return "", "", err
	}
	return a.Path, a.ContentType, nil
}

func deleteAttachment(id string) {
	if id == "" {
		return
	}
	if a, err := db.GetAttachmentById(id); err == nil {
		os.Remove(a.Path)
		db.DB.Delete(&db.Attachment{}, "id = ?", id)
	}
}
