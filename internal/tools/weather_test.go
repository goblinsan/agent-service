package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWeatherForecastToolReturnsCurrentAndDailyForecast(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search":
			assert.Equal(t, "Clearwater, Florida", r.URL.Query().Get("name"))
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results": []map[string]any{
					{
						"name":      "Clearwater",
						"latitude":  27.9659,
						"longitude": -82.8001,
						"country":   "United States",
						"admin1":    "Florida",
						"timezone":  "America/New_York",
					},
				},
			})
		case "/forecast":
			assert.Equal(t, "27.9659", r.URL.Query().Get("latitude"))
			assert.Equal(t, "-82.8001", r.URL.Query().Get("longitude"))
			assert.Equal(t, "fahrenheit", r.URL.Query().Get("temperature_unit"))
			assert.Equal(t, "2", r.URL.Query().Get("forecast_days"))
			_ = json.NewEncoder(w).Encode(map[string]any{
				"latitude":              27.9659,
				"longitude":             -82.8001,
				"timezone":              "America/New_York",
				"timezone_abbreviation": "EDT",
				"current_units": map[string]any{
					"time":                 "iso8601",
					"temperature_2m":       "°F",
					"apparent_temperature": "°F",
					"relative_humidity_2m": "%",
					"precipitation":        "inch",
					"wind_speed_10m":       "mph",
					"wind_gusts_10m":       "mph",
				},
				"current": map[string]any{
					"time":                 "2026-05-27T17:00",
					"temperature_2m":       87.4,
					"apparent_temperature": 93.1,
					"relative_humidity_2m": 64,
					"precipitation":        0,
					"rain":                 0,
					"showers":              0,
					"weather_code":         2,
					"wind_speed_10m":       9.2,
					"wind_gusts_10m":       18.5,
				},
				"daily_units": map[string]any{
					"time":                          "iso8601",
					"temperature_2m_max":            "°F",
					"temperature_2m_min":            "°F",
					"precipitation_probability_max": "%",
					"precipitation_sum":             "inch",
				},
				"daily": map[string]any{
					"time":                          []string{"2026-05-27", "2026-05-28"},
					"weather_code":                  []int{2, 61},
					"temperature_2m_max":            []float64{89, 88},
					"temperature_2m_min":            []float64{76, 75},
					"precipitation_probability_max": []int{20, 70},
					"precipitation_sum":             []float64{0, 0.31},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	tool := &WeatherForecastTool{
		GeocodingEndpoint: server.URL + "/search",
		ForecastEndpoint:  server.URL + "/forecast",
		Client:            server.Client(),
	}

	out, err := tool.Execute(context.Background(), map[string]any{
		"location":         "Clearwater, Florida",
		"days":             2,
		"temperature_unit": "fahrenheit",
	})
	require.NoError(t, err)

	result := out.(map[string]any)
	assert.Equal(t, true, result["ok"])
	assert.Equal(t, "open-meteo", result["source"])
	assert.Contains(t, result["summary"], "Current weather in Clearwater, Florida")
	location := result["location"].(map[string]any)
	assert.Equal(t, "Clearwater", location["name"])
	current := result["current"].(map[string]any)
	assert.Equal(t, "partly cloudy", current["weather"])
	assert.Equal(t, 87.4, current["temperature"])
	daily := result["daily"].([]map[string]any)
	require.Len(t, daily, 2)
	assert.Equal(t, "rain", daily[1]["weather"])
}

func TestWeatherForecastToolRetriesCityStateGeocoding(t *testing.T) {
	var searchQueries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search":
			query := r.URL.Query().Get("name")
			searchQueries = append(searchQueries, query)
			if query == "Clearwater, Florida" {
				_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
				return
			}
			assert.Equal(t, "Clearwater", query)
			assert.Equal(t, "10", r.URL.Query().Get("count"))
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results": []map[string]any{
					{
						"name":      "Clearwater",
						"latitude":  33.4968,
						"longitude": -81.8921,
						"country":   "United States",
						"admin1":    "South Carolina",
						"timezone":  "America/New_York",
					},
					{
						"name":      "Clearwater",
						"latitude":  27.9659,
						"longitude": -82.8001,
						"country":   "United States",
						"admin1":    "Florida",
						"timezone":  "America/New_York",
					},
				},
			})
		case "/forecast":
			assert.Equal(t, "27.9659", r.URL.Query().Get("latitude"))
			assert.Equal(t, "-82.8001", r.URL.Query().Get("longitude"))
			_ = json.NewEncoder(w).Encode(minimalForecastResponse())
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	tool := &WeatherForecastTool{
		GeocodingEndpoint: server.URL + "/search",
		ForecastEndpoint:  server.URL + "/forecast",
		Client:            server.Client(),
	}

	out, err := tool.Execute(context.Background(), map[string]any{"location": "Clearwater, Florida"})
	require.NoError(t, err)
	assert.Equal(t, []string{"Clearwater, Florida", "Clearwater"}, searchQueries)

	result := out.(map[string]any)
	location := result["location"].(map[string]any)
	assert.Equal(t, "Florida", location["admin1"])
}

func TestWeatherForecastToolReturnsErrorWhenLocationMissing(t *testing.T) {
	tool := &WeatherForecastTool{}
	_, err := tool.Execute(context.Background(), map[string]any{"location": ""})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "location must not be empty")
}

func TestWeatherForecastToolReturnsErrorWhenLocationNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
	}))
	defer server.Close()

	tool := &WeatherForecastTool{GeocodingEndpoint: server.URL, Client: server.Client()}
	_, err := tool.Execute(context.Background(), map[string]any{"location": "Atlantis"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no location match")
}

func minimalForecastResponse() map[string]any {
	return map[string]any{
		"latitude":              27.9659,
		"longitude":             -82.8001,
		"timezone":              "America/New_York",
		"timezone_abbreviation": "EDT",
		"current_units": map[string]any{
			"time":                 "iso8601",
			"temperature_2m":       "°F",
			"apparent_temperature": "°F",
			"relative_humidity_2m": "%",
			"precipitation":        "inch",
			"wind_speed_10m":       "mph",
			"wind_gusts_10m":       "mph",
		},
		"current": map[string]any{
			"time":                 "2026-05-27T17:00",
			"temperature_2m":       87.4,
			"apparent_temperature": 93.1,
			"relative_humidity_2m": 64,
			"precipitation":        0,
			"rain":                 0,
			"showers":              0,
			"weather_code":         2,
			"wind_speed_10m":       9.2,
			"wind_gusts_10m":       18.5,
		},
		"daily_units": map[string]any{
			"time":                          "iso8601",
			"temperature_2m_max":            "°F",
			"temperature_2m_min":            "°F",
			"precipitation_probability_max": "%",
			"precipitation_sum":             "inch",
		},
		"daily": map[string]any{
			"time":                          []string{"2026-05-27"},
			"weather_code":                  []int{2},
			"temperature_2m_max":            []float64{89},
			"temperature_2m_min":            []float64{76},
			"precipitation_probability_max": []int{20},
			"precipitation_sum":             []float64{0},
		},
	}
}
