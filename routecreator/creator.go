package routecreator

import (
	"context"
	"log"
	"strings"

	_ "embed"

	"explorenyc-skynet/ai"
	"explorenyc-skynet/data"
	"explorenyc-skynet/db"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed routecreator_prompt.txt
var creatorPrompt string

//go:embed routecreator_validate.txt
var validatorPrompt string

func Run(DBpool *pgxpool.Pool, smartModel *ai.GeminiClient, dumbModel *ai.GeminiClient) {

	//get a random scenario
	scenario := data.RandomScenario()
	log.Printf("selected scenario: %s (%s)", scenario.ID, scenario.Name)

	//build the generator prompt
	placesList := data.FormatPlacesForPrompt()
	genPrompt := strings.ReplaceAll(creatorPrompt, "{{SCENARIO_INSTRUCTIONS}}", scenario.GeneratorPrompt)
	genPrompt = strings.ReplaceAll(genPrompt, "{{AVAILABLE_PLACES}}", placesList)

	//generate the synthetic request JSON
	generatedJSON, err := smartModel.PromptJSON(genPrompt)
	if err != nil {
		log.Printf("generation failed: %v", err)
		return
	}

	//build the validator prompt
	valPrompt := strings.ReplaceAll(validatorPrompt, "{{SCENARIO_INSTRUCTIONS}}", scenario.GeneratorPrompt)
	valPrompt = strings.ReplaceAll(valPrompt, "{{GENERATED_JSON}}", generatedJSON)

	//call Flash to validate
	verdict, err := dumbModel.Prompt(valPrompt)
	if err != nil {
		log.Printf("validation call failed: %v", err)
		return
	}

	if !strings.HasPrefix(strings.TrimSpace(verdict), "VALID") {
		log.Printf("validation rejected: %s", verdict)
		return
	}

	//insert routeInput into DB
	id, err := db.InsertCreatedRoute(context.Background(), DBpool, []byte(generatedJSON), scenario.ID, smartModel.Model)
	if err != nil {
		log.Printf("db insert failed: %v", err)
		return
	}

	log.Printf("inserted route_maker row: %s", id)

}
