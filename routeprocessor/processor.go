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

	"explorenyc-skynet/ai"
	"explorenyc-skynet/db"
)

//go:embed routerater_prompt.txt
var raterPrompt string

func Run(DBpool *pgxpool.Pool, smartModel *ai.GeminiClient, dumbModel *ai.GeminiClient) {
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

	if resp.StatusCode != http.StatusOK {
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

	ratingsJSON, err := smartModel.PromptJSON(ratePrompt)
	if err != nil {
		log.Printf("rating call failed: %v", err)
		return
	}

	//insert rating into DB
	if err := db.InsertRouteRating(ctx, DBpool, id, []byte(ratingsJSON), "gemini-1.5-pro"); err != nil {
		log.Printf("insert rating failed: %v", err)
		return
	}
	log.Printf("rated row: %s", id)

	_ = dumbModel // unused in processor for now
}
