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

const batchSize = 20

func BatchRateSubmit(DBpool *pgxpool.Pool, smartModel *ai.GeminiClient) (string, error) {

	ctx := context.Background()

	engineURL := os.Getenv("ROUTING_ENGINE_URL")
	if engineURL == "" {
		log.Printf("ROUTING_ENGINE_URL not set")
		return "", nil
	}
	apiKey := os.Getenv("ROUTING_ENGINE_API_KEY")
	if apiKey == "" {
		log.Printf("ROUTING_ENGINE_API_KEY not set")
		return "", nil
	}

	httpClient := &http.Client{Timeout: 10 * time.Second}

	var inlined []*genai.InlinedRequest
	materialized := 0

	for materialized < batchSize {
		id, jsonInput, err := db.GetNextUnmaterialized(ctx, DBpool)
		if err != nil {
			log.Printf("no more unmaterialized rows (got %d): %v", materialized, err)
			break
		}

		req, err := http.NewRequestWithContext(ctx, "POST", engineURL, bytes.NewReader(jsonInput))
		if err != nil {
			log.Printf("row %s: building http request failed: %v", id, err)
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", apiKey)

		resp, err := httpClient.Do(req)
		if err != nil {
			log.Printf("row %s: routing engine call failed: %v", id, err)
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			log.Printf("row %s: reading response body failed: %v", id, err)
			continue
		}

		engineStatus := "OK — engine returned a valid itinerary."
		if resp.StatusCode == 422 {
			log.Printf("row %s: routing engine returned 422", id)
			engineStatus = "FAILED — engine returned 422 (impossible to route). Score everything 0 and guess why."
		} else if resp.StatusCode != http.StatusOK {
			log.Printf("row %s: routing engine returned %d, skipping", id, resp.StatusCode)
			continue
		}

		if err := db.MarkAsMaterialized(ctx, DBpool, id, body); err != nil {
			log.Printf("row %s: mark materialized failed: %v", id, err)
			continue
		}
		log.Printf("materialized row: %s", id)

		prompt := strings.ReplaceAll(raterPrompt, "{{TRIP_INPUT}}", string(jsonInput))
		prompt = strings.ReplaceAll(prompt, "{{TRIP_OUTPUT}}", string(body))
		prompt = strings.ReplaceAll(prompt, "{{ENGINE_STATUS}}", engineStatus)

		inlined = append(inlined, &genai.InlinedRequest{
			Contents: []*genai.Content{genai.NewContentFromText(prompt, genai.RoleUser)},
			Config: &genai.GenerateContentConfig{
				ResponseMIMEType: "application/json",
			},
		})
		materialized++
	}

	if len(inlined) == 0 {
		log.Printf("no rows materialized, nothing to submit")
		return "", nil
	}

	log.Printf("submitting rating batch with %d requests", len(inlined))

	job, err := smartModel.Client.Batches.Create(ctx, smartModel.Model, &genai.BatchJobSource{
		InlinedRequests: inlined,
	}, &genai.CreateBatchJobConfig{})
	if err != nil {
		log.Printf("rating batch create failed: %v", err)
		return "", err
	}

	log.Printf("rating batch submitted: displayName=%s name=%s state=%s", job.DisplayName, job.Name, job.State)
	return job.Name, nil
}

func BatchRateFetch(DBpool *pgxpool.Pool, smartModel *ai.GeminiClient, jobName string) {
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

func BatchRateFull(DBpool *pgxpool.Pool, smartModel *ai.GeminiClient) {
	jobName, err := BatchRateSubmit(DBpool, smartModel)
	if err != nil {
		log.Fatalf("batch-rate-submit failed: %v", err)
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
			BatchRateFetch(DBpool, smartModel, jobName)
			return
		}
	}

	log.Fatalf("batch job did not succeed after all retries")
}

func BatchRateMat(DBpool *pgxpool.Pool, smartModel *ai.GeminiClient) {
	ctx := context.Background()

	rows, err := db.GetNextNUnratedMaterialized(ctx, DBpool, batchSize)
	if err != nil {
		log.Fatalf("fetching unrated materialized rows failed: %v", err)
	}
	if len(rows) == 0 {
		log.Printf("no unrated materialized rows found")
		return
	}

	var inlined []*genai.InlinedRequest
	for _, row := range rows {
		engineStatus := "OK — engine returned a valid itinerary."

		prompt := strings.ReplaceAll(raterPrompt, "{{TRIP_INPUT}}", string(row.JSONInput))
		prompt = strings.ReplaceAll(prompt, "{{TRIP_OUTPUT}}", string(row.JSONOutput))
		prompt = strings.ReplaceAll(prompt, "{{ENGINE_STATUS}}", engineStatus)

		inlined = append(inlined, &genai.InlinedRequest{
			Contents: []*genai.Content{genai.NewContentFromText(prompt, genai.RoleUser)},
			Config: &genai.GenerateContentConfig{
				ResponseMIMEType: "application/json",
			},
		})
	}

	log.Printf("submitting materialized-rating batch with %d requests", len(inlined))

	job, err := smartModel.Client.Batches.Create(ctx, smartModel.Model, &genai.BatchJobSource{
		InlinedRequests: inlined,
	}, &genai.CreateBatchJobConfig{})
	if err != nil {
		log.Fatalf("rating batch create failed: %v", err)
	}

	jobName := job.Name
	log.Printf("rating batch submitted: displayName=%s name=%s state=%s", job.DisplayName, job.Name, job.State)

	waits := []time.Duration{180 * time.Second, 2 * time.Minute, 4 * time.Minute}

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
			if job.Dest == nil || job.Dest.InlinedResponses == nil {
				log.Fatalf("no inlined responses on completed rating job")
			}

			if len(rows) != len(job.Dest.InlinedResponses) {
				log.Fatalf("row count (%d) != response count (%d), aborting to avoid mismatched ratings",
					len(rows), len(job.Dest.InlinedResponses))
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
			return
		}
	}

	log.Fatalf("batch job did not succeed after all retries")
}
