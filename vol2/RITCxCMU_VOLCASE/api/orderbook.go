package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

func GetOrderBook(ctx context.Context, baseURL, apiKey, ticker string) (OrderBook, error) {
	url := fmt.Sprintf("%s/securities/book?ticker=%s", baseURL, ticker)
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return OrderBook{}, err
	}
	defer resp.Body.Close()
	var ob OrderBook
	return ob, json.NewDecoder(resp.Body).Decode(&ob)
}
