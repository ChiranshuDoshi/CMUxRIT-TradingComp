package logic

import (
	"context"
	"log"
	"strconv"
	"strings"
	"volcase/api"
)

var openPositions []OptionPosition
var rtmPosition int

func VolTrader(ctx context.Context, baseURL, apiKey string, tick int) {
	// -------------------
	// 1. Fetch latest news & forecast
	// -------------------
	news, err := api.GetNews(ctx, baseURL, apiKey)
	if err != nil || len(news) == 0 {
		log.Println("news err:", err)
		return
	}
	latest := news[0]
	forecast := ParseVolForecast(latest.Body, tick)
	if forecast.Type == "" {
		log.Println("no forecast found in news:", latest.Body)
		return
	}

	// skip if forecast week is past
	currentWeek := (tick / 75) + 1
	if forecast.AppliesWeek != 0 && forecast.AppliesWeek < currentWeek {
		log.Printf("forecast applies to week %d but current week %d, skipping\n",
			forecast.AppliesWeek, currentWeek)
		return
	}

	// -------------------
	// 2. Get RTM underlying price
	// -------------------
	secs, err := api.GetSecurities(ctx, baseURL, apiKey)
	if err != nil {
		log.Println("GetSecurities err:", err)
		return
	}

	var S float64
	for _, s := range secs {
		if s.Ticker == "RTM" {
			S = (s.Bid + s.Ask) / 2
			break
		}
	}

	if S == 0 {
		log.Println("RTM price not found")
		return
	}

	// -------------------
	// 3. Select nearest strike
	// -------------------
	allowedStrikes := []int{48, 49, 50, 51, 52}
	strike := NearestStrike(S, allowedStrikes)
	callTicker := "RTM" + strconv.Itoa(strike) + "C"
	putTicker := "RTM" + strconv.Itoa(strike) + "P"

	// -------------------
	// 4. Get order books
	// -------------------
	obCall, errCall := api.GetOrderBook(ctx, baseURL, apiKey, callTicker)
	obPut, errPut := api.GetOrderBook(ctx, baseURL, apiKey, putTicker)
	if errCall != nil || errPut != nil || len(obCall.Asks) == 0 || len(obCall.Bids) == 0 ||
		len(obPut.Asks) == 0 || len(obPut.Bids) == 0 {
		log.Println("orderbook err:", errCall, errPut)
		return
	}

	midCall := (obCall.Asks[0].Price + obCall.Bids[0].Price) / 2
	midPut := (obPut.Asks[0].Price + obPut.Bids[0].Price) / 2

	// -------------------
	// 5. Calculate implied volatility
	// -------------------
	ivCall := CalcImpliedVol(midCall, S, float64(strike), true, tick)
	ivPut := CalcImpliedVol(midPut, S, float64(strike), false, tick)
	currentIV := (ivCall + ivPut) / 2
	log.Println("current IV:", currentIV)

	// -------------------
	// 6. Partial close existing positions
	// -------------------
	for i := range openPositions {
		closedQty := PartialClose(&openPositions[i], currentIV)
		if closedQty > 0 {
			log.Printf("Partially closed %d contracts for %s/%s at IV=%.2f",
				closedQty, openPositions[i].CallTicker, openPositions[i].PutTicker, currentIV)
		}
	}

	// -------------------
	// 7. Close positions if target reached
	// -------------------
	CheckAndClosePositions(ctx, baseURL, apiKey, currentIV)

	// -------------------
	// 8. Place new orders if forecast signals
	// -------------------
	if ShouldTrade(currentIV, forecast.Low, forecast.High, forecast.Type) {
		maxContracts := 20
		qty := CalcOrderQty(currentIV, forecast.High, maxContracts)
		targetIV := AdjustTargetIV(currentIV, forecast.High, forecast.Type)

		orders := []api.Order{
			{Ticker: callTicker, Quantity: qty, Action: "BUY", Type: "MARKET"},
			{Ticker: putTicker, Quantity: qty, Action: "BUY", Type: "MARKET"},
		}
		if err := SendOrders(ctx, baseURL, apiKey, orders); err != nil {
			log.Println("order err:", err)
		} else {
			AddPosition(callTicker, putTicker, qty, "BUY", currentIV, targetIV)
			log.Printf("Entered position: %s/%s, qty=%d, entryIV=%.2f, targetIV=%.2f",
				callTicker, putTicker, qty, currentIV, targetIV)
		}
	}

	// -------------------
	// 9. Hedge delta
	// -------------------
	optionDelta := 0.0
	for _, pos := range openPositions {
		callStrike, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(pos.CallTicker, "RTM"), "C"))
		callDelta := OptionDelta(S, float64(callStrike), timeToExpiry(tick), 0.0, currentIV/100.0, true)
		putDelta := OptionDelta(S, float64(callStrike), timeToExpiry(tick), 0.0, currentIV/100.0, false)
		qty := float64(pos.Quantity) * 100
		if pos.Action == "BUY" {
			optionDelta += qty*callDelta + qty*putDelta
		} else {
			optionDelta -= qty*callDelta + qty*putDelta
		}
	}

	totalDelta := float64(rtmPosition) + optionDelta
	if ShouldHedge(totalDelta, 6500) {
		ManageDelta(ctx, baseURL, apiKey, S, tick, currentIV)
	}
}
