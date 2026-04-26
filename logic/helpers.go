package logic

import (
	"context"
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
				{Ticker: pos.CallTicker, Quantity: pos.Quantity,
					Action: OppositeAction(pos.Action), Type: "MARKET"},
				{Ticker: pos.PutTicker, Quantity: pos.Quantity,
					Action: OppositeAction(pos.Action), Type: "MARKET"},
			}

			if err := SendOrders(ctx, baseURL, apiKey, closeOrders); err != nil {
				log.Println("close order err:", err)
				remaining = append(remaining, pos) // keep if failed
			} else {
				log.Printf("Closed position on %s/%s at IV=%.2f (entry %.2f target %.2f)\n",
					pos.CallTicker, pos.PutTicker, currentIV, pos.EntryIV, pos.TargetIV)
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

func getThresholds(week int) (incDec, rng, fix float64) {
	// defaults
	incDec = 2.0 // for "increase"/"decrease"
	rng = 0.5    // for "range"
	fix = 1.0    // for "fixed"

	incDec *= float64(week)
	rng *= float64(week)
	fix *= float64(week)

	return incDec, rng, fix
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

// ManageDelta computes total portfolio delta and hedges using RTM ETF if needed.
// S is the current RTM price, tick is the current tick, iv is current implied vol (%).
func ManageDelta(ctx context.Context, baseURL, apiKey string, S float64, tick int, iv float64) {
	// 1. Current time to expiry and sigma
	T := timeToExpiry(tick)
	sigma := iv / 100.0

	// 2. Compute option deltas only (no ETF yet)
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

	// 3. Total portfolio delta = ETF shares + option delta
	totalDelta := float64(rtmPosition) + optionDelta

	const band = 6500.0
	if math.Abs(totalDelta) > band {
		// target ETF shares to bring total delta to zero:
		// totalDelta = rtmPosition + optionDelta, so rtmPosition_target = -optionDelta
		targetETF := -optionDelta

		// how many shares to trade to reach targetETF from current rtmPosition
		diff := int(targetETF - float64(rtmPosition))

		// clip to API max order size per leg
		const maxETFOrder = 10000
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
				// update our ETF position
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
