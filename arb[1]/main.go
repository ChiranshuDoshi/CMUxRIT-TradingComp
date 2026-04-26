package main

import (
	"arbcase/api"
	"arbcase/logic"
	"context"
	"log"
	"time"
)

func main() {
	baseURL := ("http://localhost:9999/v1")
	apiKey := "18WWG30P"
	if baseURL == "" {
		log.Fatal("set RIT_BASE_URL")
	}
	ctx := context.Background()

	for {
		// secs, err := api.GetSecurities(ctx, baseURL, apiKey)
		// if err != nil {
		// 	log.Println("securities err:", err)
		// 	time.Sleep(time.Second)
		// 	continue
		// }
		// log.Println(secs)
		// logic.CheckArbitrageAndTrade(ctx, baseURL, apiKey)

		caseDetails, err := api.GetCase(ctx, baseURL, apiKey)
		if err != nil {
			continue
		}
		log.Println(caseDetails.Tick)
		logic.Arb(ctx, baseURL, apiKey, caseDetails.Tick)

		logic.TenderArb(ctx, baseURL, apiKey)
		// api.PlaceOrder(context.Background(), baseURL, apiKey, api.Order{Ticker: "RITC", Type: "MARKET", Quantity: 100, Action: "BUY"})

		time.Sleep(200 * time.Millisecond)
	}
}
