package logic

import (
	"context"
	"fmt"
	"log"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"volcase/api"
)

type VolForecast struct {
	Type        string  // "increase","decrease","range","fixed"
	Low, High   float64 // percent values (e.g. 33.0, 38.0)
	AppliesWeek int     // which week the forecast applies to
}

func ParseVolForecast(body string, tick int) VolForecast {
	lower := strings.ToLower(body)
	vf := VolForecast{}

	// current week (1..4)
	currentWeek := (tick / 75) + 1

	if strings.Contains(lower, "this week") || strings.Contains(lower, "current") {
		vf.AppliesWeek = currentWeek
	} else if strings.Contains(lower, "next week") {
		vf.AppliesWeek = currentWeek + 1
	}

	// detect numbers (between X% and Y%)
	re := regexp.MustCompile(`between\s+(\d+)%\s+and\s+(\d+)%`)
	if m := re.FindStringSubmatch(lower); len(m) == 3 {
		low, _ := strconv.ParseFloat(m[1], 64)
		high, _ := strconv.ParseFloat(m[2], 64)
		vf.Type = "range"
		vf.Low = low
		vf.High = high
	} else if strings.Contains(lower, "increase") {
		vf.Type = "increase"
	} else if strings.Contains(lower, "decrease") {
		vf.Type = "decrease"
	} else if strings.Contains(lower, "will be") {
		// single number forecast
		re2 := regexp.MustCompile(`will be\s+(\d+)%`)
		if m2 := re2.FindStringSubmatch(lower); len(m2) == 2 {
			val, _ := strconv.ParseFloat(m2[1], 64)
			vf.Type = "fixed"
			vf.Low = val
			vf.High = val
		}
	} else if strings.Contains(lower, "current annualized realized volatility") {
		re := regexp.MustCompile(`volatility\s+is\s+(\d+)%`)
		if m := re.FindStringSubmatch(lower); len(m) == 2 {
			val, _ := strconv.ParseFloat(m[1], 64)
			vf.Type = "fixed"
			vf.Low = val
			vf.High = val
		}
	}
	return vf
}

func BlackScholesPrice(S, K, T, r, sigma float64, isCall bool) float64 {
	d1 := (math.Log(S/K) + (r+0.5*sigma*sigma)*T) / (sigma * math.Sqrt(T))
	d2 := d1 - sigma*math.Sqrt(T)

	if isCall {
		return S*normCDF(d1) - K*math.Exp(-r*T)*normCDF(d2)
	} else {
		return K*math.Exp(-r*T)*normCDF(-d2) - S*normCDF(-d1)
	}
}

func normCDF(x float64) float64 {
	return 0.5 * (1.0 + math.Erf(x/math.Sqrt2))
}

// Invert price → implied vol
func ImpliedVol(price, S, K, T, r float64, isCall bool) float64 {
	// simple bisection between 0.01 and 3.0
	low, high := 0.0001, 3.0
	for i := 0; i < 50; i++ {
		mid := (low + high) / 2
		p := BlackScholesPrice(S, K, T, r, mid, isCall)
		if p > price {
			high = mid
		} else {
			low = mid
		}
	}
	return (low + high) / 2
}

func AddPosition(callTicker, putTicker string, qty int, action string, entryIV, targetIV float64) {
	pos := OptionPosition{
		CallTicker: callTicker,
		PutTicker:  putTicker,
		Quantity:   qty,
		Action:     strings.ToUpper(action),
		EntryIV:    entryIV,
		TargetIV:   targetIV,
	}
	openPositions = append(openPositions, pos)
}

func OppositeAction(action string) string {
	if strings.ToUpper(action) == "BUY" {
		return "SELL"
	}
	return "BUY"
}

func SendOrders(ctx context.Context, baseURL, apiKey string, orders []api.Order) error {
	var wg sync.WaitGroup
	errs := make(chan error, len(orders))
	wg.Add(len(orders))
	for _, o := range orders {
		go func(ord api.Order) {
			defer wg.Done()
			err := api.PlaceOrder(ctx, baseURL, apiKey, ord)
			errs <- err
		}(o)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

func CheckAndClosePositions(ctx context.Context, baseURL, apiKey string, currentIV float64) {
	var remaining []OptionPosition

	for _, pos := range openPositions {
		exit := false

		// decide if exit condition met
		if pos.Action == "BUY" && currentIV >= pos.TargetIV {
			exit = true // long straddle reached target vol
		} else if pos.Action == "SELL" && currentIV <= pos.TargetIV {
			exit = true // short straddle reached target vol
		}

		if exit {
			closeOrders := []api.Order{
				{
					Ticker:   pos.CallTicker,
					Quantity: pos.Quantity,
					Action:   OppositeAction(pos.Action),
					Type:     "MARKET",
				},
				{
					Ticker:   pos.PutTicker,
					Quantity: pos.Quantity,
					Action:   OppositeAction(pos.Action),
					Type:     "MARKET",
				},
			}

			if err := SendOrders(ctx, baseURL, apiKey, closeOrders); err != nil {
				log.Println("close order err:", err)
				remaining = append(remaining, pos) // keep if failed
			} else {
				log.Printf(
					"Closed position on %s/%s at IV=%.2f (entry %.2f target %.2f)\n",
					pos.CallTicker, pos.PutTicker, currentIV, pos.EntryIV, pos.TargetIV,
				)
			}
		} else {
			remaining = append(remaining, pos)
		}
	}

	openPositions = remaining
}

func CalcImpliedVol(midPrice, S, K float64, isCall bool, tick int) float64 {
	// 1. Remaining time to expiry in years
	const totalTicks = 300 // 20 days * 15 ticks per day
	const ticksPerDay = 15
	const daysPerYear = 240

	remTicks := totalTicks - tick
	if remTicks < 1 {
		remTicks = 1 // avoid zero at the end
	}
	T := float64(remTicks) / float64(ticksPerDay*daysPerYear) // time in years

	// 2. Clamp option price to intrinsic value
	intrinsic := 0.0
	if isCall {
		intrinsic = math.Max(0, S-K)
	} else {
		intrinsic = math.Max(0, K-S)
	}
	if midPrice < intrinsic {
		midPrice = intrinsic + 1e-6 // avoid impossible price
	}

	// 3. Invert Black-Scholes to get sigma
	sigma := ImpliedVol(midPrice, S, K, T, 0.0, isCall) // your existing ImpliedVol(bisection)

	// 4. Return as percent
	return sigma * 100
}

func CalcT(tick int) float64 {
	const totalTicks = 300 // 20 days * 15 ticks per day
	const ticksPerDay = 15
	const daysPerYear = 240

	remTicks := totalTicks - tick
	if remTicks < 1 {
		remTicks = 1 // avoid zero at the end
	}
	T := float64(remTicks) / float64(ticksPerDay*daysPerYear)
	return T
}

// getThresholds returns the thresholds for inc/dec, range, fixed
// automatically adjusted by week and tick.
func getThresholds(week int, tick int) (incDec, rng, fix float64) {
	// defaults
	incDec = 5.0
	rng = 2.0
	fix = 4.0

	// boost at week open
	if tick%75 == 1 {
		incDec *= 0.6
		rng *= 0.6
		fix *= 0.6
	}

	// explode late in the case to effectively block entries
	if tick > 280 {
		incDec *= 5
		rng *= 5
		fix *= 5
	}

	// double thresholds in week 4
	// if week == 4 {
	// 	incDec *= 2
	// 	rng *= 2
	// 	fix *= 2
	// }
	return
}

func chooseQty(gap, T float64, base, cap int) int {
	if gap <= 0 {
		return 0
	}
	// scale with gap and sqrt(T) to approximate vega
	v := int(gap * 2.4 * math.Sqrt(T))
	q := base + v
	if q > cap {
		q = cap
	}
	if q < 0 {
		q = 0
	}
	return q
}

func calcTotals() (gross, net int) {
	for _, pos := range openPositions {
		// long for BUY, short for SELL
		if strings.ToUpper(pos.Action) == "BUY" {
			net += pos.Quantity * 2 // call+put
			gross += pos.Quantity * 2
		} else {
			net -= pos.Quantity * 2
			gross += pos.Quantity * 2
		}
	}
	return
}

func OptionDelta(S, K, T, r, sigma float64, isCall bool) float64 {
	d1 := (math.Log(S/K) + (r+0.5*sigma*sigma)*T) / (sigma * math.Sqrt(T))
	if isCall {
		return normCDF(d1) // call delta per share
	}
	return normCDF(d1) - 1 // put delta per share
}

func timeToExpiry(tick int) float64 {
	const totalTicks = 300
	const ticksPerDay = 15
	const daysPerYear = 240
	remTicks := totalTicks - tick
	if remTicks < 1 {
		remTicks = 1
	}
	return float64(remTicks) / float64(ticksPerDay*daysPerYear)
}

func ManageDelta(ctx context.Context, baseURL, apiKey string, S float64, tick int, iv float64) {
	const maxETFOrder = 10000

	// 1. Compute total option delta
	T := timeToExpiry(tick)
	sigma := iv / 100.0
	optionDelta := 0.0

	for _, pos := range openPositions {
		// extract strike from ticker, e.g. RTM51C -> 51
		callPart := strings.TrimSuffix(strings.TrimPrefix(pos.CallTicker, "RTM"), "C")
		kInt, _ := strconv.Atoi(callPart)
		K := float64(kInt)

		callDelta := OptionDelta(S, K, T, 0.0, sigma, true)
		putDelta := OptionDelta(S, K, T, 0.0, sigma, false)
		qty := float64(pos.Quantity) * 100.0 // contracts * 100 shares

		if pos.Action == "BUY" {
			optionDelta += qty*callDelta + qty*putDelta
		} else {
			optionDelta -= qty*callDelta + qty*putDelta
		}
	}

	// 2. Total delta = ETF position + option delta
	totalDelta := float64(rtmPosition) + optionDelta

	// 3. If no option positions left, flatten RTM hedge immediately in chunks ≤ maxETFOrder
	if len(openPositions) == 0 {
		for rtmPosition != 0 {
			// choose slice size
			slice := rtmPosition
			if slice > maxETFOrder {
				slice = maxETFOrder
			}
			if slice < -maxETFOrder {
				slice = -maxETFOrder
			}

			action := "SELL"
			qty := slice
			if slice < 0 {
				action = "BUY"
				qty = -slice
			}

			order := api.Order{Ticker: "RTM", Quantity: qty, Action: action, Type: "MARKET"}
			if err := SendOrders(ctx, baseURL, apiKey, []api.Order{order}); err != nil {
				log.Println("flatten hedge err:", err)
				break
			}

			if action == "SELL" {
				rtmPosition -= qty
			} else {
				rtmPosition += qty // rtmPosition negative, adding qty moves toward zero
			}

			log.Printf("Flattened residual hedge: %s %d RTM shares (remaining position=%d)\n",
				action, qty, rtmPosition)
		}
		return // nothing else to do if no options
	}

	// 4. Otherwise normal hedging bands
	trigger, target := getHedgeBand()

	if math.Abs(totalDelta) > trigger {
		// target ETF shares to bring delta toward target
		targetETF := -(optionDelta - target*math.Copysign(1, optionDelta))
		diff := int(targetETF - float64(rtmPosition))

		// clip to API max order size per leg
		if diff > maxETFOrder {
			diff = maxETFOrder
		}
		if diff < -maxETFOrder {
			diff = -maxETFOrder
		}

		if diff != 0 {
			action := "BUY"
			qty := diff
			if diff < 0 {
				action = "SELL"
				qty = -diff
			}

			order := api.Order{Ticker: "RTM", Quantity: qty, Action: action, Type: "MARKET"}
			if err := SendOrders(ctx, baseURL, apiKey, []api.Order{order}); err != nil {
				log.Println("hedge order err:", err)
			} else {
				if action == "BUY" {
					rtmPosition += qty
				} else {
					rtmPosition -= qty
				}
				log.Printf("Adjusted hedge with %s %d RTM shares (totalDelta before hedge=%.0f)\n",
					action, qty, totalDelta)
			}
		}
	}
}

func getHedgeBand() (trigger, target float64) {
	longVol := 0
	for _, pos := range openPositions {
		if pos.Action == "BUY" {
			longVol += pos.Quantity
		} else {
			longVol -= pos.Quantity
		}
	}
	if longVol > 0 {
		// week 4 gamma scalp even tighter
		return 800, 0 // hedge sooner and to zero
	}
	return 6000, 5000 // lazier when short vol
}

func checkRiskLimits(action string, qtyPerLeg int) bool {
	gross, net := 0, 0
	for _, pos := range openPositions {
		if strings.ToUpper(pos.Action) == "BUY" {
			net += pos.Quantity * 2
			gross += pos.Quantity * 2
		} else {
			net -= pos.Quantity * 2
			gross += pos.Quantity * 2
		}
	}
	qty := qtyPerLeg * 2 // two legs
	newGross := gross + qty
	newNet := net
	if strings.ToUpper(action) == "BUY" {
		newNet += qty
	} else {
		newNet -= qty
	}
	return newGross < 2500 && math.Abs(float64(newNet)) < 1000
}

func maxAddableContracts(action string) int {
	gross, net := 0, 0
	for _, pos := range openPositions {
		if strings.ToUpper(pos.Action) == "BUY" {
			net += pos.Quantity * 2
			gross += pos.Quantity * 2
		} else {
			net -= pos.Quantity * 2
			gross += pos.Quantity * 2
		}
	}
	// each straddle = 2 contracts (call+put)
	remainingGross := 2500 - gross
	remainingNet := 1000 - int(math.Abs(float64(net)))
	// how many per leg we can add safely:
	maxPerLeg := remainingGross / 2
	if remainingNet/2 < maxPerLeg {
		maxPerLeg = remainingNet / 2
	}
	if maxPerLeg < 0 {
		maxPerLeg = 0
	}
	return maxPerLeg
}

func strikeFromTicker(ticker string) int {
	// strip prefix and suffix to get the number
	core := strings.TrimPrefix(ticker, "RTM")
	core = strings.TrimSuffix(core, "C")
	core = strings.TrimSuffix(core, "P")
	k, _ := strconv.Atoi(core)
	return k
}

// RollPosition closes an old straddle and re-opens it at the new ATM strike.
func RollPosition(ctx context.Context, baseURL, apiKey string, old OptionPosition, newStrike int, iv float64) {
	callTicker := fmt.Sprintf("RTM%dC", newStrike)
	putTicker := fmt.Sprintf("RTM%dP", newStrike)

	// 1. Close old straddle (reverse the action)
	reverseAction := "SELL"
	if strings.ToUpper(old.Action) == "SELL" {
		reverseAction = "BUY"
	}
	closeOrders := []api.Order{
		{Ticker: old.CallTicker, Quantity: old.Quantity, Action: reverseAction, Type: "MARKET"},
		{Ticker: old.PutTicker, Quantity: old.Quantity, Action: reverseAction, Type: "MARKET"},
	}
	if err := SendOrders(ctx, baseURL, apiKey, closeOrders); err != nil {
		log.Println("roll close err:", err)
		return
	}
	RemovePosition(old)

	// 2. Open new straddle at ATM
	if !checkRiskLimits(old.Action, old.Quantity) {
		log.Println("risk limit on roll, skipping reopen")
		return
	}
	openOrders := []api.Order{
		{Ticker: callTicker, Quantity: old.Quantity, Action: old.Action, Type: "MARKET"},
		{Ticker: putTicker, Quantity: old.Quantity, Action: old.Action, Type: "MARKET"},
	}
	if err := SendOrders(ctx, baseURL, apiKey, openOrders); err != nil {
		log.Println("roll open err:", err)
		return
	}
	// add new position with same targetIV
	AddPosition(callTicker, putTicker, old.Quantity, old.Action, iv, old.TargetIV)
	log.Printf("Rolled %s/%s to %s/%s (strike %d→%d)\n",
		old.CallTicker, old.PutTicker, callTicker, putTicker,
		strikeFromTicker(old.CallTicker), newStrike)
}

func RemovePosition(p OptionPosition) {
	for i, op := range openPositions {
		if op.CallTicker == p.CallTicker &&
			op.PutTicker == p.PutTicker &&
			strings.EqualFold(op.Action, p.Action) &&
			op.Quantity == p.Quantity {
			// remove op from slice
			openPositions = append(openPositions[:i], openPositions[i+1:]...)
			return
		}
	}
}

// // computeEdge compares market mid vs your forecast
// func computeEdge(S, K, T, analystSigma float64, marketMid float64, isCall bool) OptionEdge {
// 	// your fair price & greeks
// 	fairRes := BlackScholes(S, K, T, analystSigma, isCall)
// 	// market implied vol from mid
// 	mktIV := ImpliedVol(marketMid, S, K, T, isCall)
// 	vegaPerContract := fairRes.Vega * 100.0 // each contract =100 shares

// 	// rough dollar edge per contract
// 	edge := (fairRes.Price-marketMid)*100.0 - 1.0 // minus $1 fee per contract
// 	if edge < 0 {
// 		edge = 0
// 	}

// 	ticker := fmt.Sprintf("RTM%.0f%s", K, map[bool]string{true: "C", false: "P"}[isCall])
// 	return OptionEdge{
// 		Ticker:      ticker,
// 		Strike:      K,
// 		Call:        isCall,
// 		MarketIV:    mktIV,
// 		FairIV:      analystSigma,
// 		Vega:        vegaPerContract,
// 		EdgeDollars: edge,
// 	}
// }

// // pickBestATM finds the option with max vega-edge
// func pickBestATM(S float64, T float64, analystSigma float64,
// 	callMids map[float64]float64, putMids map[float64]float64) OptionEdge {

// 	best := OptionEdge{EdgeDollars: -1}
// 	for strike, mid := range callMids {
// 		e := computeEdge(S, strike, T, analystSigma, mid, true)
// 		if e.EdgeDollars > best.EdgeDollars {
// 			best = e
// 		}
// 	}
// 	for strike, mid := range putMids {
// 		e := computeEdge(S, strike, T, analystSigma, mid, false)
// 		if e.EdgeDollars > best.EdgeDollars {
// 			best = e
// 		}
// 	}
// 	return best
// }

// // decideTrade turns the edge into a buy/sell quantity
// func decideTrade(edge OptionEdge, maxContracts int) (action string, qty int) {
// 	// if market IV < fair IV => option cheap => BUY (long vol)
// 	// if market IV > fair IV => option rich => SELL (short vol)
// 	if edge.MarketIV < edge.FairIV {
// 		action = "BUY"
// 	} else {
// 		action = "SELL"
// 	}

// 	// simple sizing: scale with vega edge but respect net limit
// 	// e.g. 50 contracts minimum, up to maxContracts
// 	if edge.EdgeDollars < 5 { // too small edge, skip
// 		return "", 0
// 	}
// 	qty = maxContracts
// 	return action, qty
// }
