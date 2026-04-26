package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Structs to hold asset and news data
type Asset struct {
	Ticker   string  `json:"ticker"`
	Last     float64 `json:"last"`
	Size     float64 `json:"size"`
	Position float64 `json:"position"`
}

type News struct {
	Ticker   string `json:"ticker"`
	Headline string `json:"headline"`
	Body     string `json:"body"`
}

type Helper struct {
	ShareExposure   float64 `json:"share_exposure"`
	RequiredHedge   float64 `json:"required_hedge"`
	MustBeTraded    float64 `json:"must_be_traded"`
	CurrentPos      string  `json:"current_pos"`
	RequiredPos     string  `json:"required_pos"`
	SAME            bool    `json:"SAME"`
	TotalDelta      float64 `json:"total_delta"`
	RtmNetPos       float64 `json:"rtm_net_pos"`
	RtmGrossPos     float64 `json:"rtm_gross_pos"`
	OptionsNetPos   float64 `json:"options_net_pos"`
	OptionsGrossPos float64 `json:"options_gross_pos"`
}

// PositionData tracks entry price, volatility, and tick for P&L calculations
type PositionData struct {
	Size           float64
	EntryPrice     float64
	PeakPnL        float64
	EntryVolSpread float64
	EntryTick      int
	EntryValue     float64
}

// Global state for trading
var newsVolatilities = make(map[string]float64)
var entryPositions = make(map[string]PositionData)
var optionsNetPosition float64
var optionsGrossPosition float64
var underlyingNetPosition float64
var underlyingGrossPosition float64
var portfolioPnL float64
var portfolioValue float64
var initialPortfolioValue = 100000.0
var underlyingPrices []float64
var tickerToTrade string

// Constants for API and trading strategy
const API_KEY = "18WWG30P"
const baseURL = "http://localhost:9999/v1"

// Trading and Risk Management Limits - ADJUSTED FOR DYNAMIC SIZING
const MAX_UNDERLYING_TRADE_SIZE_RATIO = 0.20 // Max underlying trade size is 20% of portfolio value
const MAX_OPTION_TRADE_SIZE_RATIO = 0.05     // Max option trade size is 5% of portfolio value
const DELTA_LIMIT_RATIO = 0.07               // Delta limit is 7% of portfolio value
const UNDERLYING_GROSS_LIMIT = 50000.0
const UNDERLYING_NET_LIMIT = 50000.0
const OPTIONS_GROSS_LIMIT = 2500.0
const OPTIONS_NET_LIMIT = 1000.0
const PORTFOLIO_DRAWDOWN_PERCENT = 0.10
const GAMMA_SCALP_TICKS_LIMIT = 50
const PENALTY_RATE = 0.01
const PnL_TAKE_PROFIT_RATIO = 0.20 // Take profit at 20% gain on the straddle's entry value
const PnL_STOP_LOSS_RATIO = -0.10  // Stop loss at 10% loss on the straddle's entry value

// Transaction Costs
const UNDERLYING_FEE_PER_SHARE = 0.01
const OPTIONS_FEE_PER_CONTRACT = 1.00

// getTick fetches the current tick from the API.
func getTick() (int, error) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/case", baseURL), nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("X-API-Key", API_KEY)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("failed to send tick request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return 0, fmt.Errorf("unauthorized request - invalid API key")
	}
	var result map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&result)
	if err != nil {
		return 0, fmt.Errorf("failed to decode result: %v", err)
	}
	if tickVal, exists := result["tick"]; exists {
		switch v := tickVal.(type) {
		case string:
			tick, err := strconv.Atoi(v)
			if err != nil {
				return 0, fmt.Errorf("failed to convert tick string to int: %v", err)
			}
			return tick, nil
		case float64:
			return int(v), nil
		default:
			return 0, fmt.Errorf("unexpected type for tick: %T", v)
		}
	}
	return 0, fmt.Errorf("tick field not found in the API response")
}

// getAssets fetches all securities data from the API.
func getAssets() ([]Asset, error) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/securities", baseURL), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-Key", API_KEY)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var assets []Asset
	err = json.NewDecoder(resp.Body).Decode(&assets)
	if err != nil {
		return nil, fmt.Errorf("failed to decode assets: %v", err)
	}

	optionsNetPosition, optionsGrossPosition = 0.0, 0.0
	underlyingNetPosition, underlyingGrossPosition = 0.0, 0.0
	portfolioPnL = 0.0
	for _, asset := range assets {
		if asset.Ticker == tickerToTrade {
			underlyingNetPosition += asset.Position
			underlyingGrossPosition += math.Abs(asset.Position)
			entryData, ok := entryPositions[asset.Ticker]
			if ok {
				portfolioPnL += (asset.Last - entryData.EntryPrice) * asset.Position
			}
		} else {
			optionsNetPosition += asset.Position
			optionsGrossPosition += math.Abs(asset.Position)
			entryData, ok := entryPositions[asset.Ticker]
			if ok {
				pnl := (asset.Last - entryData.EntryPrice) * asset.Position * 100
				portfolioPnL += pnl
			}
		}
	}
	portfolioValue = initialPortfolioValue + portfolioPnL
	return assets, nil
}

// getNews fetches news items from the API and updates the newsVolatilities map.
func getNews() error {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/news", baseURL), nil)
	if err != nil {
		return fmt.Errorf("failed to create news request: %v", err)
	}
	req.Header.Set("X-API-Key", API_KEY)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send news request: %v", err)
	}
	defer resp.Body.Close()
	var newsItems []News
	err = json.NewDecoder(resp.Body).Decode(&newsItems)
	if err != nil {
		return fmt.Errorf("failed to decode news: %v", err)
	}
	for _, news := range newsItems {
		if strings.Contains(strings.ToUpper(news.Headline), "VOLATILITY INCREASE") {
			log.Printf("News: '%s' - detected volatility increase for %s", news.Headline, news.Ticker)
			newsVolatilities[news.Ticker] = 0.50
		} else if strings.Contains(strings.ToUpper(news.Headline), "VOLATILITY DECREASE") {
			log.Printf("News: '%s' - detected volatility decrease for %s", news.Headline, news.Ticker)
			newsVolatilities[news.Ticker] = 0.05
		}
	}
	return nil
}

// getUnderlyingPrice fetches the price of the underlying asset from the assets list.
func getUnderlyingPrice(assets []Asset, ticker string) (float64, error) {
	for _, asset := range assets {
		if asset.Ticker == ticker {
			return asset.Last, nil
		}
	}
	return 0, fmt.Errorf("underlying asset '%s' not found", ticker)
}

// Black-Scholes option pricing formula
func blackScholes(optionType string, S, K, T, r, v float64) float64 {
	if v <= 0 || T <= 0 {
		return 0
	}
	d1 := (math.Log(S/K) + (r+0.5*v*v)*T) / (v * math.Sqrt(T))
	d2 := d1 - v*math.Sqrt(T)
	if optionType == "call" {
		return S*normCdf(d1) - K*math.Exp(-r*T)*normCdf(d2)
	} else if optionType == "put" {
		return K*math.Exp(-r*T)*normCdf(-d2) - S*normCdf(-d1)
	}
	return 0
}

// CDF of the standard normal distribution
func normCdf(x float64) float64 {
	return 0.5*math.Erf(x/math.Sqrt2) + 0.5
}

// Vega of the option
func vega(S, K, T, r, v float64) float64 {
	if v <= 0 || T <= 0 {
		return 0
	}
	d1 := (math.Log(S/K) + (r+0.5*v*v)*T) / (v * math.Sqrt(T))
	return S * normPdf(d1) * math.Sqrt(T)
}

// Delta of the option
func delta(optionType string, S, K, T, r, v float64) float64 {
	if v <= 0 || T <= 0 {
		return 0
	}
	d1 := (math.Log(S/K) + (r+0.5*v*v)*T) / (v * math.Sqrt(T))
	if optionType == "call" {
		return normCdf(d1)
	} else if optionType == "put" {
		return normCdf(d1) - 1.0
	}
	return 0
}

// PDF of the standard normal distribution
func normPdf(x float64) float64 {
	return math.Exp(-0.5*x*x) / math.Sqrt(2*math.Pi)
}

// Calculate implied volatility using a numerical method (Newton-Raphson)
func impliedVolatility(price, S, K, T, r float64, optionType string) float64 {
	if price <= 0 || S <= 0 || K <= 0 || T <= 0 {
		return -1.0
	}
	const tolerance = 0.0001
	const maxIterations = 100
	v := 0.2
	for i := 0; i < maxIterations; i++ {
		priceEstimate := blackScholes(optionType, S, K, T, r, v)
		vegaVal := vega(S, K, T, r, v)
		if vegaVal == 0 {
			break
		}
		v = v - (priceEstimate-price)/vegaVal
		if v < 0 {
			v = 0.001
		}
		if math.Abs(priceEstimate-price) < tolerance {
			return v
		}
	}
	return -1
}

// extractStrikePrice extracts the strike price from the ticker string.
func extractStrikePrice(ticker string) float64 {
	re := regexp.MustCompile(`(\d+)([CP])$`)
	match := re.FindStringSubmatch(ticker)
	if len(match) > 1 {
		strikePrice, err := strconv.ParseFloat(match[1], 64)
		if err != nil {
			log.Printf("Error parsing strike price for %s: %v", ticker, err)
			return 0.0
		}
		return strikePrice
	}
	return 0.0
}

// getAnalystVolatility fetches the current analyst volatility from the news map.
func getAnalystVolatility(ticker string) float64 {
	if vol, ok := newsVolatilities[ticker]; ok {
		return vol
	}
	return 0.20
}

// sendOrder sends a market order to the trading API and updates entry price.
func sendOrder(ticker, direction string, size int, currentPrice float64, currentTick int) error {
	if size <= 0 {
		return nil
	}

	// Apply transaction costs
	var transactionCost float64
	if ticker == tickerToTrade {
		transactionCost = float64(size) * UNDERLYING_FEE_PER_SHARE
	} else {
		transactionCost = float64(size) * OPTIONS_FEE_PER_CONTRACT
	}
	portfolioValue -= transactionCost

	orderURL := fmt.Sprintf("%s/orders?ticker=%s&type=MARKET&quantity=%d&action=%s", baseURL, ticker, size, direction)
	req, err := http.NewRequest("POST", orderURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create order request: %v", err)
	}
	req.Header.Set("X-API-Key", API_KEY)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send order request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("API returned non-OK status: %s", resp.Status)
	}

	currentPos, ok := entryPositions[ticker]
	if !ok {
		entryPositions[ticker] = PositionData{
			Size:       float64(size),
			EntryPrice: currentPrice,
			EntryTick:  currentTick,
			EntryValue: currentPrice * float64(size),
		}
	} else {
		tradedSize := float64(size)
		if direction == "SELL" {
			tradedSize = -tradedSize
		}
		newSize := currentPos.Size + tradedSize

		if math.Abs(newSize) < 0.01 {
			delete(entryPositions, ticker)
			log.Printf("Position for %s closed.", ticker)
		} else {
			currentTotalValue := currentPos.EntryPrice * currentPos.Size
			tradedTotalValue := currentPrice * tradedSize
			newEntryPrice := (currentTotalValue + tradedTotalValue) / newSize

			currentPos.Size = newSize
			currentPos.EntryPrice = newEntryPrice
			currentPos.EntryTick = currentTick
			currentPos.EntryValue += currentPrice * float64(size)
			entryPositions[ticker] = currentPos
		}
	}

	log.Printf("Order for %s %s successful. Size: %d, New Position: %.0f. Cost: %.2f", ticker, direction, size, entryPositions[ticker].Size, transactionCost)
	return nil
}

// getAssetsSync is a helper to get assets for other functions
func getAssetsSync() []Asset {
	assets, err := getAssets()
	if err != nil {
		log.Printf("Error getting assets in getAssetsSync: %v", err)
		return nil
	}
	return assets
}

// findATMStrike finds the closest at-the-money strike price
func findATMStrike(assets []Asset, underlyingPrice float64, ticker string) (float64, error) {
	minDiff := math.MaxFloat64
	atmStrike := 0.0
	found := false
	for _, asset := range assets {
		if strings.HasPrefix(asset.Ticker, ticker) && (strings.HasSuffix(asset.Ticker, "C") || strings.HasSuffix(asset.Ticker, "P")) {
			strike := extractStrikePrice(asset.Ticker)
			if math.Abs(strike-underlyingPrice) < minDiff {
				minDiff = math.Abs(strike - underlyingPrice)
				atmStrike = strike
				found = true
			}
		}
	}
	if !found {
		return 0, fmt.Errorf("no options found for ticker '%s' to determine ATM strike", ticker)
	}
	return atmStrike, nil
}

// This function now implements the pairs trading strategy by maintaining delta neutrality.
func handleAssets(assets []Asset, currentTick int, ticker string) ([]Asset, Helper) {
	var helper Helper
	underlyingPrice, err := getUnderlyingPrice(assets, ticker)
	if err != nil {
		log.Printf("Error getting underlying price: %v", err)
		return assets, helper
	}

	totalDelta := 0.0
	underlyingPosition := 0.0
	underlyingGross := 0.0
	optionsGross := 0.0

	for _, asset := range assets {
		if asset.Ticker == ticker {
			underlyingPosition = asset.Position
			underlyingGross += math.Abs(asset.Position)
		} else if strings.HasPrefix(asset.Ticker, ticker) {
			optionsGross += math.Abs(asset.Position)
			r, T, iVol := 0.0, 1.0, 0.20
			strikePrice := extractStrikePrice(asset.Ticker)
			optionType := optionType(asset.Ticker)

			currentImpliedVol := impliedVolatility(asset.Last, underlyingPrice, strikePrice, T, r, optionType)
			if currentImpliedVol > 0 {
				iVol = currentImpliedVol
			}

			// Add to total delta, considering 1 contract = 100 shares
			totalDelta += delta(optionType, underlyingPrice, strikePrice, T, r, iVol) * asset.Position * 100
		}
	}

	totalDelta += underlyingPosition
	helper.TotalDelta = totalDelta
	helper.RtmGrossPos = underlyingGross
	helper.OptionsGrossPos = optionsGross
	helper.RtmNetPos = underlyingPosition
	helper.OptionsNetPos = optionsNetPosition

	// --- Gamma Scalping (Pairs Trading) Logic ---
	straddleOpen := false
	var callAsset, putAsset Asset
	for _, asset := range assets {
		if strings.HasPrefix(asset.Ticker, ticker) && strings.Contains(asset.Ticker, "C") && asset.Position > 0 {
			callAsset = asset
			straddleOpen = true
		} else if strings.HasPrefix(asset.Ticker, ticker) && strings.Contains(asset.Ticker, "P") && asset.Position > 0 {
			putAsset = asset
			straddleOpen = true
		}
	}

	if straddleOpen {
		// Calculate P&L for the straddle
		callEntryData, callOk := entryPositions[callAsset.Ticker]
		putEntryData, putOk := entryPositions[putAsset.Ticker]

		if callOk && putOk {
			callPnL := (callAsset.Last - callEntryData.EntryPrice) * callEntryData.Size * 100
			putPnL := (putAsset.Last - putEntryData.EntryPrice) * putEntryData.Size * 100
			totalStraddlePnL := callPnL + putPnL

			// Calculate total entry value for the straddle
			totalEntryValue := callEntryData.EntryValue*100 + putEntryData.EntryValue*100

			// Take-profit and stop-loss checks
			if totalStraddlePnL > PnL_TAKE_PROFIT_RATIO*totalEntryValue {
				log.Printf("TAKE PROFIT: Straddle P&L hit %.2f%%. Squaring off.", PnL_TAKE_PROFIT_RATIO*100)
				sendOrder(callAsset.Ticker, "SELL", int(math.Abs(callAsset.Position)), callAsset.Last, currentTick)
				sendOrder(putAsset.Ticker, "SELL", int(math.Abs(putAsset.Position)), putAsset.Last, currentTick)
				return assets, helper
			}

			if totalStraddlePnL < PnL_STOP_LOSS_RATIO*totalEntryValue {
				log.Printf("STOP LOSS: Straddle P&L fell to %.2f%%. Squaring off.", PnL_STOP_LOSS_RATIO*100)
				sendOrder(callAsset.Ticker, "SELL", int(math.Abs(callAsset.Position)), callAsset.Last, currentTick)
				sendOrder(putAsset.Ticker, "SELL", int(math.Abs(putAsset.Position)), putAsset.Last, currentTick)
				return assets, helper
			}
		}

		if entryPositions[callAsset.Ticker].EntryTick > 0 && currentTick-entryPositions[callAsset.Ticker].EntryTick > GAMMA_SCALP_TICKS_LIMIT {
			log.Printf("Straddle held for too long. Squaring off to limit theta decay.")
			sendOrder(callAsset.Ticker, "SELL", int(math.Abs(callAsset.Position)), callAsset.Last, currentTick)
			sendOrder(putAsset.Ticker, "SELL", int(math.Abs(putAsset.Position)), putAsset.Last, currentTick)
			return assets, helper
		}
	} else {
		// Open a new straddle to initiate the "pair" if none exists
		atmStrike, err := findATMStrike(assets, underlyingPrice, ticker)
		if err != nil {
			log.Printf("Could not find ATM strike to open straddle: %v", err)
		} else {
			atmCallTicker := fmt.Sprintf("%s%dC", ticker, int(atmStrike))
			atmPutTicker := fmt.Sprintf("%s%dP", ticker, int(atmStrike))
			atmCallAsset, atmPutAsset := Asset{}, Asset{}
			for _, asset := range assets {
				if asset.Ticker == atmCallTicker {
					atmCallAsset = asset
				}
				if asset.Ticker == atmPutTicker {
					atmPutAsset = asset
				}
			}

			callIVol := impliedVolatility(atmCallAsset.Last, underlyingPrice, atmStrike, 1.0, 0.0, "call")
			putIVol := impliedVolatility(atmPutAsset.Last, underlyingPrice, atmStrike, 1.0, 0.0, "put")

			avgIVol := 0.20
			if callIVol > 0 && putIVol > 0 {
				avgIVol = (callIVol + putIVol) / 2.0
			}

			// Dynamic sizing based on portfolio value
			maxOptionTradeSize := int(MAX_OPTION_TRADE_SIZE_RATIO * portfolioValue)
			straddleSize := int(avgIVol * 200)
			if straddleSize < 1 {
				straddleSize = 1
			}
			if straddleSize > maxOptionTradeSize {
				straddleSize = maxOptionTradeSize
			}

			if optionsGross+float64(straddleSize*2) > OPTIONS_GROSS_LIMIT || math.Abs(optionsNetPosition+float64(straddleSize*2)) > OPTIONS_NET_LIMIT {
				log.Println("Cannot open new straddle, would exceed options gross/net limits.")
			} else {
				log.Printf("Opening new ATM straddle at strike %d with size %d", int(atmStrike), straddleSize)
				sendOrder(atmCallTicker, "BUY", straddleSize, atmCallAsset.Last, currentTick)
				sendOrder(atmPutTicker, "BUY", straddleSize, atmPutAsset.Last, currentTick)
			}
		}
	}

	// --- Pairs Trading (Delta Hedging) Logic ---
	// Trade the underlying to bring the portfolio's delta back toward the center (0)
	deltaLimit := DELTA_LIMIT_RATIO * portfolioValue
	maxUnderlyingTradeSize := int(MAX_UNDERLYING_TRADE_SIZE_RATIO * portfolioValue)

	if totalDelta > deltaLimit {
		hedgeSize := totalDelta - deltaLimit
		if hedgeSize > float64(maxUnderlyingTradeSize) {
			hedgeSize = float64(maxUnderlyingTradeSize)
		}
		if underlyingGross+hedgeSize > UNDERLYING_GROSS_LIMIT || underlyingPosition-hedgeSize < -UNDERLYING_NET_LIMIT {
			log.Println("Cannot perform hedge, would exceed underlying gross/net limits.")
		} else {
			log.Printf("PAIRS TRADING: Total Delta: %.2f. Selling underlying to reduce long exposure. Size: %.0f", totalDelta, hedgeSize)
			sendOrder(ticker, "SELL", int(hedgeSize), underlyingPrice, currentTick)
		}
	} else if totalDelta < -deltaLimit {
		hedgeSize := math.Abs(totalDelta + deltaLimit)
		if hedgeSize > float64(maxUnderlyingTradeSize) {
			hedgeSize = float64(maxUnderlyingTradeSize)
		}
		if underlyingGross+hedgeSize > UNDERLYING_GROSS_LIMIT || underlyingPosition+hedgeSize > UNDERLYING_NET_LIMIT {
			log.Println("Cannot perform hedge, would exceed underlying gross/net limits.")
		} else {
			log.Printf("PAIRS TRADING: Total Delta: %.2f. Buying underlying to reduce short exposure. Size: %.0f", totalDelta, hedgeSize)
			sendOrder(ticker, "BUY", int(hedgeSize), underlyingPrice, currentTick)
		}
	} else {
		log.Println("Portfolio is within delta limits. No pairs trade required.")
	}

	return assets, helper
}

func optionType(ticker string) string {
	if len(ticker) > 0 {
		if ticker[len(ticker)-1] == 'C' {
			return "call"
		} else if ticker[len(ticker)-1] == 'P' {
			return "put"
		}
	}
	return ""
}

func main() {
	if len(os.Args) < 2 {
		log.Fatalf("Usage: go run main.go <TICKER>")
	}
	tickerToTrade = os.Args[1]

	for {
		log.Println("Starting new trading cycle...")
		tick, err := getTick()
		if err != nil {
			log.Printf("Error getting tick: %v. Retrying...", err)
			time.Sleep(2 * time.Second)
			continue
		}
		fmt.Printf("Current tick: %d\n", tick)
		err = getNews()
		if err != nil {
			log.Printf("Error getting news: %v. Using previous news data...", err)
		}

		assets, err := getAssets()
		if err != nil {
			log.Printf("Error getting assets: %v. Retrying...", err)
			time.Sleep(2 * time.Second)
			continue
		}

		underlyingPrice, err := getUnderlyingPrice(assets, tickerToTrade)
		if err != nil {
			log.Printf("Error getting underlying price for delta calculation: %v", err)
			time.Sleep(2 * time.Second)
			continue
		}

		// Calculate penalty for this second
		totalDelta := 0.0
		for _, asset := range assets {
			if asset.Ticker == tickerToTrade {
				totalDelta += asset.Position
			} else if strings.HasPrefix(asset.Ticker, tickerToTrade) {
				r, T, iVol := 0.0, 1.0, 0.20
				strikePrice := extractStrikePrice(asset.Ticker)
				optionType := optionType(asset.Ticker)
				currentImpliedVol := impliedVolatility(asset.Last, underlyingPrice, strikePrice, T, r, iVol)
				if currentImpliedVol > 0 {
					iVol = currentImpliedVol
				}
				totalDelta += delta(optionType, underlyingPrice, strikePrice, T, r, iVol) * asset.Position * 100
			}
		}

		deltaLimit := DELTA_LIMIT_RATIO * portfolioValue
		if math.Abs(totalDelta) > deltaLimit {
			penalty := (math.Abs(totalDelta) - deltaLimit) * PENALTY_RATE
			portfolioValue -= penalty
			log.Printf("DELTA LIMIT EXCEEDED! Current Delta: %.2f. Penalty incurred: $%.2f", totalDelta, penalty)
		}

		if portfolioValue < initialPortfolioValue*(1-PORTFOLIO_DRAWDOWN_PERCENT) {
			log.Printf("PORTFOLIO STOP-LOSS HIT! Portfolio Value: %.2f, Drawdown: %.2f%%. Squaring off all positions.", portfolioValue, PORTFOLIO_DRAWDOWN_PERCENT*100)
			for _, asset := range assets {
				if asset.Position != 0 {
					direction := "SELL"
					if asset.Position < 0 {
						direction = "BUY"
					}
					sendOrder(asset.Ticker, direction, int(math.Abs(asset.Position)), asset.Last, tick)
				}
			}
			return
		}

		_, helper := handleAssets(assets, tick, tickerToTrade)
		for _, asset := range assets {
			entryData, ok := entryPositions[asset.Ticker]
			pnlString := "N/A"
			if ok {
				pnl := (asset.Last - entryData.EntryPrice) * asset.Position
				if asset.Ticker != tickerToTrade {
					pnl *= 100
				}
				pnlString = fmt.Sprintf("%.2f", pnl)
			}
			fmt.Printf("Asset: %s, Last Price: %.2f, Position: %.0f, P&L: %s\n", asset.Ticker, asset.Last, asset.Position, pnlString)
		}
		fmt.Printf("Helper Data: %+v\n", helper)
		fmt.Printf("Current Portfolio Value: %.2f\n", portfolioValue)
		log.Println("Trading cycle complete. Waiting 2 seconds before next cycle.")
		time.Sleep(2 * time.Second)
	}
}
