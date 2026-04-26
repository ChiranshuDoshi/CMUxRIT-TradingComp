package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

func GetSecurities(ctx context.Context, baseURL, apiKey string) ([]Security, error) {
	u := fmt.Sprintf("%s/securities", baseURL)
	req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var secs []Security
	return secs, json.NewDecoder(resp.Body).Decode(&secs)
}
