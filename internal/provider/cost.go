package provider

import (
	"github.com/iamnikolie/gengoya/internal/registry"
)

// Usage is the OpenAI Images API usage object (GPT Image models).
type Usage struct {
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"`
	TotalTokens        int `json:"total_tokens"`
	InputTokensDetails *struct {
		TextTokens  int `json:"text_tokens"`
		ImageTokens int `json:"image_tokens"`
	} `json:"input_tokens_details"`
	OutputTokensDetails *struct {
		TextTokens  int `json:"text_tokens"`
		ImageTokens int `json:"image_tokens"`
	} `json:"output_tokens_details"`
}

const (
	CostSourceUsage    = "usage"
	CostSourceRegistry = "registry"
)

// CostFromUsage estimates USD from token usage and model rates (per 1M tokens).
// Returns ok=false when usage is nil or rates are unset.
func CostFromUsage(m registry.Model, u *Usage) (usd float64, ok bool) {
	if u == nil {
		return 0, false
	}
	if m.PriceTextIn == 0 && m.PriceImageIn == 0 && m.PriceImageOut == 0 {
		return 0, false
	}
	textIn := 0
	imageIn := 0
	if u.InputTokensDetails != nil {
		textIn = u.InputTokensDetails.TextTokens
		imageIn = u.InputTokensDetails.ImageTokens
	} else {
		textIn = u.InputTokens
	}
	imageOut := u.OutputTokens
	if u.OutputTokensDetails != nil && u.OutputTokensDetails.ImageTokens > 0 {
		imageOut = u.OutputTokensDetails.ImageTokens
	}
	usd = (float64(textIn)*m.PriceTextIn +
		float64(imageIn)*m.PriceImageIn +
		float64(imageOut)*m.PriceImageOut) / 1_000_000
	return usd, true
}

func applyCost(r GenRequest, nImages int, u *Usage) (cost float64, source string) {
	if c, ok := CostFromUsage(r.Model, u); ok {
		return c, CostSourceUsage
	}
	return costOf(r, nImages), CostSourceRegistry
}
