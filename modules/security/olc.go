package security

import (
	"log"

	olc "github.com/google/open-location-code/go"
)

// InPlusCode returns true if the specified lat/lon falls within the bounding box of the given Plus Code.
func InPlusCode(code string, lat, lon float64) bool {
	if err := olc.CheckFull(code); err != nil {
		log.Printf("Invalid Plus Code provided: %s", code)
		return false
	}

	codeArea, err := olc.Decode(code)
	if err != nil {
		return false
	}

	// Check if lat/lon is inside the OLC bounding box
	if lat >= codeArea.LatLo && lat <= codeArea.LatHi && lon >= codeArea.LngLo && lon <= codeArea.LngHi {
		return true
	}

	return false
}
