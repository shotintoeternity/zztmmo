package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strings"

	"github.com/benhoyt/zztgo"
)

func loadEnv() {
	content, err := ioutil.ReadFile("../../../.env")
	if err != nil {
		content, err = ioutil.ReadFile("../../.env")
	}
	if err != nil {
		content, err = ioutil.ReadFile("../.env")
	}
	if err != nil {
		content, err = ioutil.ReadFile(".env")
	}
	if err != nil {
		log.Printf("Warning: .env not found, using existing environment variables: %v", err)
		return
	}
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			os.Setenv(key, val)
		}
	}
}

func main() {
	loadEnv()

	prompt := "a tiny social plaza with a locked bakery gate and a bakery key in a nearby fountain"
	if len(os.Args) > 1 {
		prompt = strings.Join(os.Args[1:], " ")
	}

	fmt.Printf("Starting live generation for prompt: %q\n", prompt)

	// Ensure batch size is set. Let's try BatchSize = 2 to demo the new batch painting!
	os.Setenv("ZZT_GENERATION_BATCH_SIZE", "2")

	service, err := zztgo.GenerationServiceFromEnv()
	if err != nil {
		log.Fatalf("Failed to initialize generation service: %v", err)
	}

	service.SetProgressReporter(func(p zztgo.GenerationProgress) {
		if p.Board != "" {
			fmt.Printf("[%s] (Board: %s, Attempt %d/%d): %s\n", p.Stage, p.Board, p.Attempt, 3, p.Detail)
		} else {
			fmt.Printf("[%s]: %s\n", p.Stage, p.Detail)
		}
	})

	result, err := service.Generate(context.Background(), "script", prompt, "BAKERY", nil)
	if err != nil {
		log.Fatalf("Generation failed: %v", err)
	}

	fmt.Println("\n==================================================")
	fmt.Printf("Success! Generated world hosted as: %s\n", result.Name)
	fmt.Printf("Plan Text:\n%s\n", result.Plan)
	fmt.Println("==================================================")
}
