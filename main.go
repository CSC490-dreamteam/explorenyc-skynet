package main

import (
	"context"
	"log"
	"os"

	"github.com/joho/godotenv"

	"explorenyc-skynet/ai"
	"explorenyc-skynet/db"
	"explorenyc-skynet/routecreator"
	"explorenyc-skynet/routeprocessor"
)

func main() {
	_ = godotenv.Load()

	if len(os.Args) < 2 {
		log.Fatal("usage: app <generate>")
	}

	ctx := context.Background()

	//init DB pool
	pool, err := db.InitPool(ctx)
	if err != nil {
		log.Fatalf("db init failed: %v", err)
	}
	defer pool.Close()

	//init Gemini clients

	genModelName := os.Getenv("genModel")
	if genModelName == "" {
		log.Printf("genModel not set")
		return
	}
	validModelName := os.Getenv("validModel")
	if validModelName == "" {
		log.Printf("validModel not set")
		return
	}
	rateModelName := os.Getenv("rateModel")
	if rateModelName == "" {
		log.Printf("rateModel not set")
		return
	}

	genModel, err := ai.NewClient(ctx, genModelName)
	if err != nil {
		log.Fatalf("smart model init failed: %v", err)
	}

	validModel, err := ai.NewClient(ctx, validModelName)
	if err != nil {
		log.Fatalf("dumb model init failed: %v", err)
	}

	raterModel, err := ai.NewClient(ctx, rateModelName)
	if err != nil {
		log.Fatalf("smart model init failed: %v", err)
	}

	//CLI command
	switch os.Args[1] {
	case "generate":
		routecreator.Run(pool, genModel, validModel)
	case "process":
		routeprocessor.Run(pool, raterModel)
	default:
		log.Fatalf("unknown subcommand: %s", os.Args[1])
	}
}
