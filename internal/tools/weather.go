package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	openMeteoGeocodingEndpoint = "https://geocoding-api.open-meteo.com/v1/search"
	openMeteoForecastEndpoint  = "https://api.open-meteo.com/v1/forecast"
)

// WeatherForecastTool returns current conditions and a short forecast using
// Open-Meteo, which does not require an API key.
type WeatherForecastTool struct {
	GeocodingEndpoint string
	ForecastEndpoint  string
	Client            *http.Client
}

func (w *WeatherForecastTool) Definition() Tool {
	return Tool{
		Name:        "weather_forecast",
		Description: "Look up current weather and a short forecast for a location using Open-Meteo. Use this for weather, rain, temperature, wind, or whether outdoor plans are advisable. If the user does not specify a location, use the user's profile home base or ask a short follow-up if no location is known.",
		Params: []Param{
			{Name: "location", Type: "string", Description: "City, address, or place name, for example 'Clearwater, Florida'.", Required: true},
			{Name: "days", Type: "int", Description: "Forecast days to include, 1-7. Defaults to 3.", Required: false},
			{Name: "temperature_unit", Type: "string", Description: "Temperature unit: 'fahrenheit' or 'celsius'. Defaults to fahrenheit.", Required: false},
		},
	}
}

type openMeteoGeocodingResponse struct {
	Results []openMeteoGeocodingResult `json:"results"`
}

type openMeteoGeocodingResult struct {
	Name      string  `json:"name"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Country   string  `json:"country"`
	Admin1    string  `json:"admin1"`
	Timezone  string  `json:"timezone"`
}

type openMeteoForecastResponse struct {
	Latitude             float64 `json:"latitude"`
	Longitude            float64 `json:"longitude"`
	Timezone             string  `json:"timezone"`
	TimezoneAbbreviation string  `json:"timezone_abbreviation"`
	CurrentUnits         struct {
		Time                string `json:"time"`
		Temperature         string `json:"temperature_2m"`
		ApparentTemperature string `json:"apparent_temperature"`
		RelativeHumidity    string `json:"relative_humidity_2m"`
		Precipitation       string `json:"precipitation"`
		WindSpeed           string `json:"wind_speed_10m"`
		WindGusts           string `json:"wind_gusts_10m"`
	} `json:"current_units"`
	Current struct {
		Time                string  `json:"time"`
		Temperature         float64 `json:"temperature_2m"`
		ApparentTemperature float64 `json:"apparent_temperature"`
		RelativeHumidity    float64 `json:"relative_humidity_2m"`
		Precipitation       float64 `json:"precipitation"`
		Rain                float64 `json:"rain"`
		Showers             float64 `json:"showers"`
		WeatherCode         int     `json:"weather_code"`
		WindSpeed           float64 `json:"wind_speed_10m"`
		WindGusts           float64 `json:"wind_gusts_10m"`
	} `json:"current"`
	DailyUnits struct {
		Time                        string `json:"time"`
		TemperatureMax              string `json:"temperature_2m_max"`
		TemperatureMin              string `json:"temperature_2m_min"`
		PrecipitationProbabilityMax string `json:"precipitation_probability_max"`
		PrecipitationSum            string `json:"precipitation_sum"`
	} `json:"daily_units"`
	Daily struct {
		Time                        []string  `json:"time"`
		WeatherCode                 []int     `json:"weather_code"`
		TemperatureMax              []float64 `json:"temperature_2m_max"`
		TemperatureMin              []float64 `json:"temperature_2m_min"`
		PrecipitationProbabilityMax []float64 `json:"precipitation_probability_max"`
		PrecipitationSum            []float64 `json:"precipitation_sum"`
	} `json:"daily"`
}

func (w *WeatherForecastTool) Execute(ctx context.Context, params map[string]any) (any, error) {
	location := strings.TrimSpace(stringParam(params, "location"))
	if location == "" {
		return nil, fmt.Errorf("weather_forecast: location must not be empty")
	}

	days := intParam(params, "days", 3)
	if days < 1 {
		days = 1
	}
	if days > 7 {
		days = 7
	}

	temperatureUnit := strings.ToLower(strings.TrimSpace(stringParam(params, "temperature_unit")))
	if temperatureUnit == "" {
		temperatureUnit = "fahrenheit"
	}
	if temperatureUnit != "fahrenheit" && temperatureUnit != "celsius" {
		return nil, fmt.Errorf("weather_forecast: temperature_unit must be fahrenheit or celsius")
	}

	client := w.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}

	place, err := w.geocode(ctx, client, location)
	if err != nil {
		return nil, err
	}

	forecast, err := w.forecast(ctx, client, place.Latitude, place.Longitude, days, temperatureUnit)
	if err != nil {
		return nil, err
	}

	return weatherResult(location, place, forecast, temperatureUnit), nil
}

type weatherPlace struct {
	Name      string
	Latitude  float64
	Longitude float64
	Country   string
	Admin1    string
	Timezone  string
}

type geocodeCandidate struct {
	Query           string
	PreferredAdmin1 string
}

func (w *WeatherForecastTool) geocode(ctx context.Context, client *http.Client, location string) (weatherPlace, error) {
	var lastErr error
	for _, candidate := range geocodeCandidates(location) {
		place, err := w.geocodeCandidate(ctx, client, candidate)
		if err == nil {
			return place, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return weatherPlace{}, lastErr
	}
	return weatherPlace{}, fmt.Errorf("weather_forecast: no location match for %q", location)
}

func (w *WeatherForecastTool) geocodeCandidate(ctx context.Context, client *http.Client, candidate geocodeCandidate) (weatherPlace, error) {
	endpoint := w.GeocodingEndpoint
	if endpoint == "" {
		endpoint = openMeteoGeocodingEndpoint
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return weatherPlace{}, fmt.Errorf("weather_forecast: parse geocoding endpoint: %w", err)
	}
	q := u.Query()
	q.Set("name", candidate.Query)
	q.Set("count", "10")
	q.Set("language", "en")
	q.Set("format", "json")
	u.RawQuery = q.Encode()

	var parsed openMeteoGeocodingResponse
	if err := getJSON(ctx, client, u.String(), &parsed); err != nil {
		return weatherPlace{}, fmt.Errorf("weather_forecast: geocode: %w", err)
	}
	if len(parsed.Results) == 0 {
		return weatherPlace{}, fmt.Errorf("weather_forecast: no location match for %q", candidate.Query)
	}
	result := chooseGeocodeResult(parsed.Results, candidate.PreferredAdmin1)
	return weatherPlace{
		Name:      result.Name,
		Latitude:  result.Latitude,
		Longitude: result.Longitude,
		Country:   result.Country,
		Admin1:    result.Admin1,
		Timezone:  result.Timezone,
	}, nil
}

func geocodeCandidates(location string) []geocodeCandidate {
	location = strings.TrimSpace(location)
	if location == "" {
		return nil
	}

	candidates := []geocodeCandidate{{Query: location}}
	seen := map[string]bool{strings.ToLower(location) + "|": true}
	add := func(query, preferredAdmin1 string) {
		query = strings.TrimSpace(strings.Trim(query, ","))
		preferredAdmin1 = normalizeUSStateName(preferredAdmin1)
		key := strings.ToLower(query) + "|" + strings.ToLower(preferredAdmin1)
		if query == "" || seen[key] {
			return
		}
		seen[key] = true
		candidates = append(candidates, geocodeCandidate{Query: query, PreferredAdmin1: preferredAdmin1})
	}

	if beforeComma, afterComma, ok := strings.Cut(location, ","); ok {
		add(beforeComma, afterComma)
	}

	lower := strings.ToLower(location)
	for alias, stateName := range usStateAliases {
		if lower == alias || strings.HasSuffix(lower, " "+alias) || strings.HasSuffix(lower, ", "+alias) {
			query := strings.TrimSpace(location[:len(location)-len(alias)])
			query = strings.TrimSpace(strings.Trim(query, ","))
			add(query, stateName)
			break
		}
	}

	return candidates
}

func chooseGeocodeResult(results []openMeteoGeocodingResult, preferredAdmin1 string) openMeteoGeocodingResult {
	if preferredAdmin1 != "" {
		for _, result := range results {
			if strings.EqualFold(result.Admin1, preferredAdmin1) {
				return result
			}
		}
	}
	return results[0]
}

func normalizeUSStateName(value string) string {
	value = strings.ToLower(strings.TrimSpace(strings.Trim(value, ",")))
	if value == "" {
		return ""
	}
	if stateName, ok := usStateAliases[value]; ok {
		return stateName
	}
	return value
}

var usStateAliases = map[string]string{
	"al": "Alabama", "alabama": "Alabama",
	"ak": "Alaska", "alaska": "Alaska",
	"az": "Arizona", "arizona": "Arizona",
	"ar": "Arkansas", "arkansas": "Arkansas",
	"ca": "California", "california": "California",
	"co": "Colorado", "colorado": "Colorado",
	"ct": "Connecticut", "connecticut": "Connecticut",
	"de": "Delaware", "delaware": "Delaware",
	"fl": "Florida", "florida": "Florida",
	"ga": "Georgia", "georgia": "Georgia",
	"hi": "Hawaii", "hawaii": "Hawaii",
	"id": "Idaho", "idaho": "Idaho",
	"il": "Illinois", "illinois": "Illinois",
	"in": "Indiana", "indiana": "Indiana",
	"ia": "Iowa", "iowa": "Iowa",
	"ks": "Kansas", "kansas": "Kansas",
	"ky": "Kentucky", "kentucky": "Kentucky",
	"la": "Louisiana", "louisiana": "Louisiana",
	"me": "Maine", "maine": "Maine",
	"md": "Maryland", "maryland": "Maryland",
	"ma": "Massachusetts", "massachusetts": "Massachusetts",
	"mi": "Michigan", "michigan": "Michigan",
	"mn": "Minnesota", "minnesota": "Minnesota",
	"ms": "Mississippi", "mississippi": "Mississippi",
	"mo": "Missouri", "missouri": "Missouri",
	"mt": "Montana", "montana": "Montana",
	"ne": "Nebraska", "nebraska": "Nebraska",
	"nv": "Nevada", "nevada": "Nevada",
	"nh": "New Hampshire", "new hampshire": "New Hampshire",
	"nj": "New Jersey", "new jersey": "New Jersey",
	"nm": "New Mexico", "new mexico": "New Mexico",
	"ny": "New York", "new york": "New York",
	"nc": "North Carolina", "north carolina": "North Carolina",
	"nd": "North Dakota", "north dakota": "North Dakota",
	"oh": "Ohio", "ohio": "Ohio",
	"ok": "Oklahoma", "oklahoma": "Oklahoma",
	"or": "Oregon", "oregon": "Oregon",
	"pa": "Pennsylvania", "pennsylvania": "Pennsylvania",
	"ri": "Rhode Island", "rhode island": "Rhode Island",
	"sc": "South Carolina", "south carolina": "South Carolina",
	"sd": "South Dakota", "south dakota": "South Dakota",
	"tn": "Tennessee", "tennessee": "Tennessee",
	"tx": "Texas", "texas": "Texas",
	"ut": "Utah", "utah": "Utah",
	"vt": "Vermont", "vermont": "Vermont",
	"va": "Virginia", "virginia": "Virginia",
	"wa": "Washington", "washington": "Washington",
	"wv": "West Virginia", "west virginia": "West Virginia",
	"wi": "Wisconsin", "wisconsin": "Wisconsin",
	"wy": "Wyoming", "wyoming": "Wyoming",
	"dc": "District of Columbia", "district of columbia": "District of Columbia",
}

func (w *WeatherForecastTool) forecast(ctx context.Context, client *http.Client, latitude, longitude float64, days int, temperatureUnit string) (openMeteoForecastResponse, error) {
	endpoint := w.ForecastEndpoint
	if endpoint == "" {
		endpoint = openMeteoForecastEndpoint
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return openMeteoForecastResponse{}, fmt.Errorf("weather_forecast: parse forecast endpoint: %w", err)
	}
	q := u.Query()
	q.Set("latitude", strconv.FormatFloat(latitude, 'f', -1, 64))
	q.Set("longitude", strconv.FormatFloat(longitude, 'f', -1, 64))
	q.Set("current", "temperature_2m,relative_humidity_2m,apparent_temperature,precipitation,rain,showers,weather_code,wind_speed_10m,wind_gusts_10m")
	q.Set("daily", "weather_code,temperature_2m_max,temperature_2m_min,precipitation_probability_max,precipitation_sum")
	q.Set("timezone", "auto")
	q.Set("forecast_days", strconv.Itoa(days))
	q.Set("temperature_unit", temperatureUnit)
	if temperatureUnit == "fahrenheit" {
		q.Set("wind_speed_unit", "mph")
		q.Set("precipitation_unit", "inch")
	} else {
		q.Set("wind_speed_unit", "kmh")
		q.Set("precipitation_unit", "mm")
	}
	u.RawQuery = q.Encode()

	var parsed openMeteoForecastResponse
	if err := getJSON(ctx, client, u.String(), &parsed); err != nil {
		return openMeteoForecastResponse{}, fmt.Errorf("weather_forecast: forecast: %w", err)
	}
	return parsed, nil
}

func getJSON(ctx context.Context, client *http.Client, url string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("http %d: %s", resp.StatusCode, string(body))
	}
	if err := json.Unmarshal(body, target); err != nil {
		return err
	}
	return nil
}

func weatherResult(requestedLocation string, place weatherPlace, forecast openMeteoForecastResponse, temperatureUnit string) map[string]any {
	daily := make([]map[string]any, 0, len(forecast.Daily.Time))
	for i, date := range forecast.Daily.Time {
		entry := map[string]any{
			"date":         date,
			"weather_code": valueAtInt(forecast.Daily.WeatherCode, i),
			"weather":      weatherCodeDescription(valueAtInt(forecast.Daily.WeatherCode, i)),
		}
		if value, ok := valueAtFloat(forecast.Daily.TemperatureMax, i); ok {
			entry["temperature_max"] = value
		}
		if value, ok := valueAtFloat(forecast.Daily.TemperatureMin, i); ok {
			entry["temperature_min"] = value
		}
		if value, ok := valueAtFloat(forecast.Daily.PrecipitationProbabilityMax, i); ok {
			entry["precipitation_probability_max"] = value
		}
		if value, ok := valueAtFloat(forecast.Daily.PrecipitationSum, i); ok {
			entry["precipitation_sum"] = value
		}
		daily = append(daily, entry)
	}

	temperatureUnitLabel := forecast.CurrentUnits.Temperature
	if temperatureUnitLabel == "" {
		if temperatureUnit == "fahrenheit" {
			temperatureUnitLabel = "°F"
		} else {
			temperatureUnitLabel = "°C"
		}
	}

	return map[string]any{
		"ok":                 true,
		"source":             "open-meteo",
		"summary":            weatherSummary(place, forecast, temperatureUnitLabel),
		"requested_location": requestedLocation,
		"location": map[string]any{
			"name":      place.Name,
			"admin1":    place.Admin1,
			"country":   place.Country,
			"latitude":  place.Latitude,
			"longitude": place.Longitude,
			"timezone":  firstNonEmptyWeatherString(place.Timezone, forecast.Timezone),
		},
		"current": map[string]any{
			"time":                 forecast.Current.Time,
			"weather_code":         forecast.Current.WeatherCode,
			"weather":              weatherCodeDescription(forecast.Current.WeatherCode),
			"temperature":          forecast.Current.Temperature,
			"apparent_temperature": forecast.Current.ApparentTemperature,
			"temperature_unit":     temperatureUnitLabel,
			"humidity_percent":     forecast.Current.RelativeHumidity,
			"precipitation":        forecast.Current.Precipitation,
			"precipitation_unit":   forecast.CurrentUnits.Precipitation,
			"wind_speed":           forecast.Current.WindSpeed,
			"wind_speed_unit":      forecast.CurrentUnits.WindSpeed,
			"wind_gusts":           forecast.Current.WindGusts,
		},
		"daily_units": map[string]any{
			"temperature":                   firstNonEmptyWeatherString(forecast.DailyUnits.TemperatureMax, temperatureUnitLabel),
			"precipitation_probability_max": forecast.DailyUnits.PrecipitationProbabilityMax,
			"precipitation_sum":             forecast.DailyUnits.PrecipitationSum,
		},
		"daily": daily,
	}
}

func weatherSummary(place weatherPlace, forecast openMeteoForecastResponse, temperatureUnit string) string {
	placeName := place.Name
	if place.Admin1 != "" {
		placeName += ", " + place.Admin1
	}
	return fmt.Sprintf(
		"Current weather in %s: %s, %.1f%s, feels like %.1f%s, humidity %.0f%%, precipitation %.2f %s, wind %.1f %s.",
		placeName,
		weatherCodeDescription(forecast.Current.WeatherCode),
		forecast.Current.Temperature,
		temperatureUnit,
		forecast.Current.ApparentTemperature,
		temperatureUnit,
		forecast.Current.RelativeHumidity,
		forecast.Current.Precipitation,
		forecast.CurrentUnits.Precipitation,
		forecast.Current.WindSpeed,
		forecast.CurrentUnits.WindSpeed,
	)
}

func weatherCodeDescription(code int) string {
	switch code {
	case 0:
		return "clear sky"
	case 1, 2, 3:
		return "partly cloudy"
	case 45, 48:
		return "fog"
	case 51, 53, 55, 56, 57:
		return "drizzle"
	case 61, 63, 65, 66, 67:
		return "rain"
	case 71, 73, 75, 77:
		return "snow"
	case 80, 81, 82:
		return "rain showers"
	case 85, 86:
		return "snow showers"
	case 95, 96, 99:
		return "thunderstorm"
	default:
		return "unknown"
	}
}

func stringParam(params map[string]any, key string) string {
	if params == nil {
		return ""
	}
	value, _ := params[key].(string)
	return value
}

func intParam(params map[string]any, key string, fallback int) int {
	if params == nil {
		return fallback
	}
	switch value := params[key].(type) {
	case int:
		return value
	case float64:
		return int(value)
	case string:
		parsed, err := strconv.Atoi(value)
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func valueAtInt(values []int, index int) int {
	if index < 0 || index >= len(values) {
		return 0
	}
	return values[index]
}

func valueAtFloat(values []float64, index int) (float64, bool) {
	if index < 0 || index >= len(values) {
		return 0, false
	}
	return values[index], true
}

func firstNonEmptyWeatherString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
