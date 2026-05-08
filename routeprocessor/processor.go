package routeprocessor

import (
	"bytes"
	"context"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	_ "embed"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/genai"

	"explorenyc-skynet/ai"
	"explorenyc-skynet/db"
)

//go:embed routerater_prompt.txt
var raterPrompt string

func Run(DBpool *pgxpool.Pool, smartModel *ai.GeminiClient) {
	ctx := context.Background()

	//get next unmaterialized row
	id, jsonInput, err := db.GetNextUnmaterialized(ctx, DBpool)
	if err != nil {
		log.Printf("no unmaterialized row available: %v", err)
		return
	}
	log.Printf("processing row: %s", id)

	//build HTTP request to routing engine
	engineURL := os.Getenv("ROUTING_ENGINE_URL")
	if engineURL == "" {
		log.Printf("ROUTING_ENGINE_URL not set")
		return
	}
	apiKey := os.Getenv("ROUTING_ENGINE_API_KEY")
	if apiKey == "" {
		log.Printf("ROUTING_ENGINE_API_KEY not set")
		return
	}

	req, err := http.NewRequestWithContext(ctx, "POST", engineURL, bytes.NewReader(jsonInput))
	if err != nil {
		log.Printf("building http request failed: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)

	//send input to routing engine
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("routing engine call failed: %v", err)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("reading response body failed: %v", err)
		return
	}

	engineStatus := "OK — engine returned a valid itinerary."
	if resp.StatusCode == 422 {
		log.Printf("routing engine returned 422 (impossible route) for %s, going to guess why", id)
		engineStatus = "FAILED — engine returned 422 (impossible to route). Score everything 0 and guess why."
	} else if resp.StatusCode != http.StatusOK {
		log.Printf("routing engine returned %d: %s", resp.StatusCode, string(body))
		return
	}

	//mark as materialized with the response body
	if err := db.MarkAsMaterialized(ctx, DBpool, id, body); err != nil {
		log.Printf("mark materialized failed: %v", err)
		return
	}
	log.Printf("materialized row: %s", id)

	//build the rater prompt and call smartModel for ratings
	ratePrompt := strings.ReplaceAll(raterPrompt, "{{TRIP_INPUT}}", string(jsonInput))
	ratePrompt = strings.ReplaceAll(ratePrompt, "{{TRIP_OUTPUT}}", string(body))
	ratePrompt = strings.ReplaceAll(ratePrompt, "{{ENGINE_STATUS}}", engineStatus)

	ratingsJSON, err := smartModel.PromptJSON(ratePrompt)
	if err != nil {
		log.Printf("rating call failed: %v", err)
		return
	}

	//insert rating into DB
	if err := db.InsertRouteRating(ctx, DBpool, id, []byte(ratingsJSON), smartModel.Model); err != nil {
		log.Printf("insert rating failed: %v", err)
		return
	}
	log.Printf("rated row: %s", id)

}

func BatchRateSubmit(DBpool *pgxpool.Pool, smartModel *ai.GeminiClient, batchname string) {
	const batchSize = 20
	ctx := context.Background()

	rows, err := db.GetNextNUnratedMaterialized(ctx, DBpool, batchSize)
	if err != nil {
		log.Printf("fetching unrated rows failed: %v", err)
		return
	}
	if len(rows) == 0 {
		log.Printf("no materialized-but-unrated rows available")
		return
	}

	const engineStatusOK = "OK — engine returned a valid itinerary."

	var inlined []*genai.InlinedRequest
	for _, r := range rows {
		prompt := strings.ReplaceAll(raterPrompt, "{{TRIP_INPUT}}", string(r.JSONInput))
		prompt = strings.ReplaceAll(prompt, "{{TRIP_OUTPUT}}", string(r.JSONOutput))
		prompt = strings.ReplaceAll(prompt, "{{ENGINE_STATUS}}", engineStatusOK)

		inlined = append(inlined, &genai.InlinedRequest{
			Contents: []*genai.Content{genai.NewContentFromText(prompt, genai.RoleUser)},
			Config: &genai.GenerateContentConfig{
				ResponseMIMEType: "application/json",
			},
		})
	}

	log.Printf("submitting rating batch with %d requests", len(inlined))

	job, err := smartModel.Client.Batches.Create(ctx, smartModel.Model, &genai.BatchJobSource{
		InlinedRequests: inlined,
	}, &genai.CreateBatchJobConfig{
		DisplayName: batchname,
	})
	if err != nil {
		log.Printf("rating batch create failed: %v", err)
		return
	}

	log.Printf("rating batch submitted: displayName=%s name=%s state=%s", job.DisplayName, job.Name, job.State)
}

func BatchRateFetch(DBpool *pgxpool.Pool, smartModel *ai.GeminiClient, jobName string) {
	const batchSize = 20
	ctx := context.Background()

	job, err := smartModel.Client.Batches.Get(ctx, jobName, nil)
	if err != nil {
		log.Printf("rating batch get failed: %v", err)
		return
	}

	log.Printf("rating batch %s state: %s", job.Name, job.State)

	if job.State != genai.JobStateSucceeded {
		log.Printf("rating batch not done yet (or failed), exiting")
		return
	}

	if job.Dest == nil || job.Dest.InlinedResponses == nil {
		log.Printf("no inlined responses on completed rating job")
		return
	}

	rows, err := db.GetNextNUnratedMaterialized(ctx, DBpool, batchSize)
	if err != nil {
		log.Printf("re-fetching unrated rows failed: %v", err)
		return
	}

	if len(rows) != len(job.Dest.InlinedResponses) {
		log.Printf("WARNING: row count (%d) != response count (%d), aborting to avoid mismatched ratings",
			len(rows), len(job.Dest.InlinedResponses))
		return
	}

	inserted, errored := 0, 0

	for i, resp := range job.Dest.InlinedResponses {
		row := rows[i]

		if resp.Error != nil {
			log.Printf("response %d (row %s) errored: %v", i, row.ID, resp.Error)
			errored++
			continue
		}
		if resp.Response == nil || len(resp.Response.Candidates) == 0 {
			log.Printf("response %d (row %s) has no candidates", i, row.ID)
			errored++
			continue
		}

		ratingsJSON := resp.Response.Text()

		if err := db.InsertRouteRating(ctx, DBpool, row.ID, []byte(ratingsJSON), smartModel.Model); err != nil {
			log.Printf("response %d (row %s) insert rating failed: %v", i, row.ID, err)
			errored++
			continue
		}

		log.Printf("rated row: %s", row.ID)
		inserted++
	}

	log.Printf("rating batch fetch complete: inserted=%d errored=%d", inserted, errored)
}
