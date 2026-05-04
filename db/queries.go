package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// store created route input in the database for later retrieval and processing
func InsertCreatedRoute(ctx context.Context, pool *pgxpool.Pool, jsonInput []byte, scenarioID string, llmUsed string) (string, error) {
	var id string
	query := `
		INSERT INTO route_maker (json_input, scenario_id, llm_used, is_rated, is_materialized)
		VALUES ($1, $2, $3, FALSE, FALSE)
		RETURNING id`
	err := pool.QueryRow(ctx, query, jsonInput, scenarioID, llmUsed).Scan(&id)
	return id, err
}

// finds a route to materialize
func GetNextUnmaterialized(ctx context.Context, pool *pgxpool.Pool) (string, []byte, error) {
	var id string
	var jsonInput []byte
	query := `
		SELECT id, json_input FROM route_maker 
		WHERE is_materialized = FALSE 
		ORDER BY id ASC LIMIT 1`
	err := pool.QueryRow(ctx, query).Scan(&id, &jsonInput)
	return id, jsonInput, err
}

// insert rating of a route
func InsertRouteRating(ctx context.Context, pool *pgxpool.Pool, materializerID string, jsonRatings []byte, llmUsed string) error {
	insertQuery := `INSERT INTO route_rater (materializer_id, json_ratings, llm_used) VALUES ($1, $2, $3)`
	if _, err := pool.Exec(ctx, insertQuery, materializerID, jsonRatings, llmUsed); err != nil {
		return err
	}
	updateQuery := `UPDATE route_maker SET is_rated = TRUE WHERE id = $1`
	_, err := pool.Exec(ctx, updateQuery, materializerID)
	return err
}
