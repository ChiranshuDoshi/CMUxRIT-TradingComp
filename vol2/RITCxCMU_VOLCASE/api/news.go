package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

func GetNews(ctx context.Context, baseURL, apiKey string) ([]News, error) {
	url := fmt.Sprintf("%s/news", baseURL)
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var news []News
	return news, json.NewDecoder(resp.Body).Decode(&news)
}
