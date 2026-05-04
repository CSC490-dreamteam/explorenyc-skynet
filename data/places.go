package data

import (
	_ "embed"
	"encoding/csv"
	"strings"
)

//go:embed places.csv
var placesCSV string

type Place struct {
	Name         string
	Category     string
	Neighborhood string
}

var AllPlaces []Place

func init() {
	r := csv.NewReader(strings.NewReader(placesCSV))
	records, _ := r.ReadAll()
	for _, rec := range records[1:] {
		AllPlaces = append(AllPlaces, Place{rec[0], rec[1], rec[2]})
	}
}

func FormatPlacesForPrompt() string {
	var sb strings.Builder
	for _, p := range AllPlaces {
		sb.WriteString(p.Name + ", " + p.Category + ", " + p.Neighborhood + "\n")
	}
	return sb.String()
}
