package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/akhilrex/hammond/db"
)

func TestVehiclePhoto(t *testing.T) {
	setupDB(t)
	_, v := mkUserVehicle(t, "", 43)
	add := func(name string) *db.Attachment {
		p := filepath.Join(t.TempDir(), name)
		os.WriteFile(p, []byte("\xff\xd8\xff photo"), 0o644)
		a := db.Attachment{Path: p, OriginalName: name, ContentType: "image/jpeg"}
		db.DB.Create(&a)
		return &a
	}
	first := add("a.jpg")
	link, err := SetVehiclePhoto(v.ID, first, "https://fuel.example.com/")
	if err != nil || len(link) != len(db.PhotoPath)+32 {
		t.Fatalf("link %q %v", link, err)
	}
	got, _ := db.GetVehicleById(v.ID)
	if got.PhotoURL != link || PhotoLink(got, true) != "https://fuel.example.com"+link {
		t.Fatalf("photoUrl %q, HA link %q", got.PhotoURL, PhotoLink(got, true))
	}
	if p, kind, err := PhotoFile(link[len(db.PhotoPath):]); err != nil || p != first.Path || kind != "image/jpeg" {
		t.Fatalf("file %q %q %v", p, kind, err)
	}
	// a new photo: new link, the old one and its file are gone
	second := add("b.jpg")
	link2, _ := SetVehiclePhoto(v.ID, second, "javascript:alert(1)")
	if link2 == link {
		t.Fatal("a new photo needs a new link")
	}
	if _, _, err := PhotoFile(link[len(db.PhotoPath):]); err == nil {
		t.Fatal("the old link still works")
	}
	if _, err := os.Stat(first.Path); !os.IsNotExist(err) {
		t.Fatal("old photo file not deleted")
	}
	got, _ = db.GetVehicleById(v.ID)
	if PhotoLink(got, true) != "" { // not a plain http(s) origin: no absolute link for HA
		t.Fatalf("bad origin kept: %q", PhotoLink(got, true))
	}
	if err := RemoveVehiclePhoto(v.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = db.GetVehicleById(v.ID)
	if got.PhotoURL != "" {
		t.Fatalf("removed photo still linked: %q", got.PhotoURL)
	}
	if _, _, err := PhotoFile("short"); err == nil {
		t.Fatal("a short token must not match")
	}
	for _, o := range []string{"http://192.168.1.5:3000", "https://fuel.example.com"} {
		if cleanOrigin(o) != o {
			t.Errorf("origin %q rejected", o)
		}
	}
	for _, o := range []string{"https://x.com/path", "ftp://x.com", "https://u:p@x.com", ""} {
		if cleanOrigin(o) != "" {
			t.Errorf("origin %q accepted", o)
		}
	}
}
