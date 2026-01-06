// Package googlephotos provides Google Takeout metadata parsing with URL extraction.
// This is a modified version of github.com/simulot/immich-go/adapters/googlePhotos/json.go
// that captures the actual Google Photos URL string instead of just checking for presence.
package googlephotos

import (
	"encoding/json"
	"strconv"
	"time"
)

// GoogleMetaData represents the JSON sidecar metadata from Google Takeout.
// Modified from immich-go to extract the actual URL string.
type GoogleMetaData struct {
	Title          string          `json:"title"`
	Description    string          `json:"description"`
	Category       string          `json:"category"`
	Date           *GoogTimeObject `json:"date,omitempty"`
	PhotoTakenTime *GoogTimeObject `json:"photoTakenTime"`
	GeoDataExif    *GoogGeoData    `json:"geoDataExif"`
	GeoData        *GoogGeoData    `json:"geoData"`
	Trashed        bool            `json:"trashed,omitempty"`
	Archived       bool            `json:"archived,omitempty"`
	URL            string          `json:"url,omitempty"` // The Google Photos URL - changed from googIsPresent to string
	Favorited      bool            `json:"favorited,omitempty"`
	People         []Person        `json:"people,omitempty"`
	GooglePhotosOrigin struct {
		FromPartnerSharing bool `json:"fromPartnerSharing,omitempty"`
	} `json:"googlePhotosOrigin"`
}

// Person represents a tagged person in the photo.
type Person struct {
	Name string `json:"name"`
}

// UnmarshalJSON handles both album metadata and asset metadata formats.
func (gmd *GoogleMetaData) UnmarshalJSON(data []byte) error {
	// Test the presence of the key albumData (album metadata format)
	type md GoogleMetaData
	type album struct {
		AlbumData *md `json:"albumData"`
	}

	var t album
	err := json.Unmarshal(data, &t)
	if err == nil && t.AlbumData != nil {
		*gmd = GoogleMetaData(*(t.AlbumData))
		return nil
	}

	var gg md
	err = json.Unmarshal(data, &gg)
	if err != nil {
		return err
	}

	*gmd = GoogleMetaData(gg)
	return nil
}

// IsAsset returns true if this metadata represents an asset (photo/video).
func (gmd *GoogleMetaData) IsAsset() bool {
	if gmd == nil || gmd.PhotoTakenTime == nil {
		return false
	}
	return gmd.PhotoTakenTime.Timestamp != ""
}

// IsAlbum returns true if this metadata represents an album.
func (gmd *GoogleMetaData) IsAlbum() bool {
	if gmd == nil || gmd.IsAsset() {
		return false
	}
	return gmd.Title != ""
}

// HasURL returns true if this metadata contains a Google Photos URL.
func (gmd *GoogleMetaData) HasURL() bool {
	return gmd != nil && gmd.URL != ""
}

// GoogGeoData contains GPS coordinates.
type GoogGeoData struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Altitude  float64 `json:"altitude"`
}

// GoogTimeObject handles the epoch timestamp format used by Google Takeout.
type GoogTimeObject struct {
	Timestamp string `json:"timestamp"`
}

// Time returns the time.Time representation of the epoch timestamp.
func (gt *GoogTimeObject) Time() time.Time {
	if gt == nil {
		return time.Time{}
	}
	ts, _ := strconv.ParseInt(gt.Timestamp, 10, 64)
	if ts == 0 {
		return time.Time{}
	}
	return time.Unix(ts, 0).In(time.Local)
}

// ParseMetadata parses JSON data into GoogleMetaData.
func ParseMetadata(data []byte) (*GoogleMetaData, error) {
	var md GoogleMetaData
	err := json.Unmarshal(data, &md)
	if err != nil {
		return nil, err
	}
	return &md, nil
}
