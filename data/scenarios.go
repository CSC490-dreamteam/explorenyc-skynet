package data

import (
	_ "embed"
	"encoding/csv"
	"math/rand"
	"strings"
)

//go:embed scenarios.csv
var scenariosCSV string

type Scenario struct {
	Name            string
	ScenarioID      string
	Summary         string
	CoreTest        string
	GeneratorPrompt string
}

var AllScenarios []Scenario

func init() {
	r := csv.NewReader(strings.NewReader(scenariosCSV))
	records, _ := r.ReadAll()
	for _, rec := range records[1:] {
		AllScenarios = append(AllScenarios, Scenario{
			Name:            rec[0],
			ScenarioID:      rec[1],
			Summary:         rec[2],
			CoreTest:        rec[3],
			GeneratorPrompt: rec[4],
		})
	}
}

func RandomScenario() Scenario {
	if len(AllScenarios) == 0 {
		return Scenario{}
	}
	return AllScenarios[rand.Intn(len(AllScenarios))]
}
