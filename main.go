package main

import (
	"context"
	"explorenyc-skynet/ai"
	"fmt"
	"log"

	"github.com/joho/godotenv"
)

func main() {
	fmt.Println("Hello, World!")

	err := godotenv.Load()
	if err != nil {
		log.Println("No .env file found, using system environment variables")
	}

	flash, err := ai.NewClient(context.Background(), "gemini-2.5-flash")
	if err != nil {
		fmt.Printf("Error creating Gemini client: %v\n", err)
		return
	}

	fmt.Println(flash.Prompt("whats 2+2?"))
}
