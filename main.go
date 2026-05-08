package main

import (
	"context"
	"flag"
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
		log.Fatal("usage: app <generate|process|batch-submit|batch-fetch|batch-list|batch-rate-submit|batch-rate-fetch>")

	}
	subcommand := os.Args[1]

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
	switch subcommand {
	case "generate":
		routecreator.Run(pool, genModel, validModel)
	case "process":
		routeprocessor.Run(pool, raterModel)
	case "batch-submit":
		fs := flag.NewFlagSet("batch-submit", flag.ExitOnError)
		name := fs.String("n", "", "display name for the batch job")
		fs.Parse(os.Args[2:])
		if *name == "" {
			log.Fatal("batch-submit requires -n <name>")
		}
		routecreator.BatchSubmit(pool, genModel, *name)
	case "batch-fetch":
		fs := flag.NewFlagSet("batch-fetch", flag.ExitOnError)
		name := fs.String("n", "", "batch job name (e.g. batches/abc123)")
		fs.Parse(os.Args[2:])
		if *name == "" {
			log.Fatal("batch-fetch requires -n <job-name>")
		}
		routecreator.BatchFetch(pool, genModel, validModel, *name)
	case "batch-rate-submit":
		fs := flag.NewFlagSet("batch-rate-submit", flag.ExitOnError)
		name := fs.String("n", "", "display name for the rating batch job")
		fs.Parse(os.Args[2:])
		if *name == "" {
			log.Fatal("batch-rate-submit requires -n <name>")
		}
		routeprocessor.BatchRateSubmit(pool, raterModel, *name)
	case "batch-rate-fetch":
		fs := flag.NewFlagSet("batch-rate-fetch", flag.ExitOnError)
		name := fs.String("n", "", "rating batch job name (e.g. batches/abc123)")
		fs.Parse(os.Args[2:])
		if *name == "" {
			log.Fatal("batch-rate-fetch requires -n <job-name>")
		}
		routeprocessor.BatchRateFetch(pool, raterModel, *name)

	default:
		log.Fatalf("unknown subcommand: %s", subcommand)
	}

}
