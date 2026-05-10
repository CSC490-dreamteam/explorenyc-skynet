package routecreator

import (
	"context"
	"log"
	"strings"
	"time"

	_ "embed"

	"explorenyc-skynet/ai"
	"explorenyc-skynet/data"
	"explorenyc-skynet/db"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/genai"
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

const batchjobName = "take1-of-skynet"

func BatchSubmit(DBpool *pgxpool.Pool, smartModel *ai.GeminiClient) (string, error) {

	const batchRunsPerScenario = 3
	ctx := context.Background()

	placesList := data.FormatPlacesForPrompt()

	//build one inline request per scenario per run
	var inlined []*genai.InlinedRequest
	for _, scenario := range data.AllScenarios {
		for i := 0; i < batchRunsPerScenario; i++ {
			prompt := strings.ReplaceAll(creatorPrompt, "{{SCENARIO_INSTRUCTIONS}}", scenario.GeneratorPrompt)
			prompt = strings.ReplaceAll(prompt, "{{AVAILABLE_PLACES}}", placesList)

			inlined = append(inlined, &genai.InlinedRequest{
				Contents: []*genai.Content{genai.NewContentFromText(prompt, genai.RoleUser)},
				Config: &genai.GenerateContentConfig{
					ResponseMIMEType: "application/json",
				},
			})
		}
	}

	log.Printf("submitting batch with %d requests", len(inlined))

	job, err := smartModel.Client.Batches.Create(ctx, smartModel.Model, &genai.BatchJobSource{
		InlinedRequests: inlined,
	}, &genai.CreateBatchJobConfig{})
	if err != nil {
		log.Printf("batch create failed: %v", err)
		return "", err
	}

	log.Printf("batch submitted: displayName=%s name=%s state=%s", job.DisplayName, job.Name, job.State)
	return job.Name, nil

}

func BatchFetch(DBpool *pgxpool.Pool, smartModel *ai.GeminiClient, dumbModel *ai.GeminiClient, jobName string) {
	ctx := context.Background()

	job, err := smartModel.Client.Batches.Get(ctx, jobName, nil)
	if err != nil {
		log.Printf("batch get failed: %v", err)
		return
	}

	log.Printf("batch %s state: %s", job.Name, job.State)

	if job.State != genai.JobStateSucceeded {
		log.Printf("batch not done yet (or failed), exiting")
		return
	}

	if job.Dest == nil || job.Dest.InlinedResponses == nil {
		log.Printf("no inlined responses on completed job")
		return
	}

	placesList := data.FormatPlacesForPrompt()

	inserted, rejected, errored := 0, 0, 0

	for i, resp := range job.Dest.InlinedResponses {
		if resp.Error != nil {
			log.Printf("response %d errored: %v", i, resp.Error)
			errored++
			continue
		}
		if resp.Response == nil || len(resp.Response.Candidates) == 0 {
			log.Printf("response %d has no candidates", i)
			errored++
			continue
		}

		generatedJSON := resp.Response.Text()

		//pick a random scenario for validation + db insert
		scenario := data.RandomScenario()

		valPrompt := strings.ReplaceAll(validatorPrompt, "{{GENERATED_JSON}}", generatedJSON)

		verdict, err := dumbModel.Prompt(valPrompt)
		if err != nil {
			log.Printf("response %d validation call failed: %v", i, err)
			errored++
			continue
		}

		if !strings.HasPrefix(strings.TrimSpace(verdict), "VALID") {
			log.Printf("response %d rejected: %s", i, verdict)
			rejected++
			continue
		}

		id, err := db.InsertCreatedRoute(ctx, DBpool, []byte(generatedJSON), scenario.ID, smartModel.Model)
		if err != nil {
			log.Printf("response %d db insert failed: %v", i, err)
			errored++
			continue
		}

		log.Printf("inserted route_maker row: %s", id)
		inserted++
	}

	log.Printf("batch fetch complete: inserted=%d rejected=%d errored=%d", inserted, rejected, errored)
	_ = placesList //placeholder if you need it later
}
func BatchFull(DBpool *pgxpool.Pool, smartModel *ai.GeminiClient, dumbModel *ai.GeminiClient) {
	jobName, err := BatchSubmit(DBpool, smartModel)
	if err != nil {
		log.Fatalf("batch-submit failed: %v", err)
	}

	waits := []time.Duration{2 * time.Minute, 2 * time.Minute, 4 * time.Minute}
	ctx := context.Background()

	for i, wait := range waits {
		log.Printf("waiting %v before status check (attempt %d)...", wait, i+1)
		time.Sleep(wait)

		job, err := smartModel.Client.Batches.Get(ctx, jobName, nil)
		if err != nil {
			log.Printf("status check %d failed: %v", i+1, err)
			continue
		}

		log.Printf("batch job %s state: %s", job.Name, job.State)

		if job.State == genai.JobStateSucceeded {
			BatchFetch(DBpool, smartModel, dumbModel, jobName)
			return
		}
	}

	log.Fatalf("batch job did not succeed after all retries")
}
