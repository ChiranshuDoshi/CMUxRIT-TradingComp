package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

func GetCase(ctx context.Context, baseURL, apiKey string) (CaseInfo, error) {
	url := fmt.Sprintf("%s/case", baseURL)
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return CaseInfo{}, err
	}
	defer resp.Body.Close()

	var c CaseInfo
	return c, json.NewDecoder(resp.Body).Decode(&c)
}
