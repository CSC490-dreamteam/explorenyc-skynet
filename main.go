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
	smartModel, err := ai.NewClient(ctx, "gemini-3-flash-preview")
	if err != nil {
		log.Fatalf("smart model init failed: %v", err)
	}

	dumbModel, err := ai.NewClient(ctx, "gemini-2.5-flash")
	if err != nil {
		log.Fatalf("dumb model init failed: %v", err)
	}

	//CLI command
	switch os.Args[1] {
	case "generate":
		routecreator.Run(pool, smartModel, dumbModel)
	case "process":
		routeprocessor.Run(pool, smartModel, dumbModel)
	default:
		log.Fatalf("unknown subcommand: %s", os.Args[1])
	}
}
