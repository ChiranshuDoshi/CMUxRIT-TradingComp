package main

import (
	"context"
	"log"
	"time"
	"volcase/api"
	"volcase/logic"
)

func main() {
	baseURL := ("http://localhost:9999/v1")
	apiKey := "KOH9N670"
	if baseURL == "" {
		log.Fatal("set RIT_BASE_URL")
	}
	ctx := context.Background()

	for {
		caseDetails, err := api.GetCase(ctx, baseURL, apiKey)
		if err != nil {
			continue
		}
		// log.Println(caseDetails.Tick)
		logic.VolTrader(ctx, baseURL, apiKey, caseDetails.Tick)
		time.Sleep(1000 * time.Millisecond)
	}

}
