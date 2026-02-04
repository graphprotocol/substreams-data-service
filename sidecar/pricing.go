package sidecar

import (
	"fmt"
	"math/big"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// PriceDecimals is the number of decimals used for prices (12 decimals)
	// This allows expressing prices like "0.000001 GRT per block" (1 GRT per million blocks)
	PriceDecimals = 12
)

var (
	// priceMultiplier is 10^12 for converting between decimal and wei
	priceMultiplier = new(big.Int).Exp(big.NewInt(10), big.NewInt(PriceDecimals), nil)

	// weiPerGRT is 10^18
	weiPerGRT = new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
)

// Price represents a price value with fixed 12 decimal places
// It stores the value in wei (10^18) internally
type Price struct {
	wei *big.Int
}

// NewPriceFromWei creates a Price from wei value
func NewPriceFromWei(wei *big.Int) *Price {
	if wei == nil {
		return &Price{wei: big.NewInt(0)}
	}
	return &Price{wei: new(big.Int).Set(wei)}
}

// NewPriceFromDecimal creates a Price from a decimal string
// Examples: "0.000001" (1 GRT per million units), "1.5", "0.01"
func NewPriceFromDecimal(decimal string) (*Price, error) {
	decimal = strings.TrimSpace(decimal)
	if decimal == "" {
		return &Price{wei: big.NewInt(0)}, nil
	}

	// Split by decimal point
	parts := strings.Split(decimal, ".")
	if len(parts) > 2 {
		return nil, fmt.Errorf("invalid decimal format: %s", decimal)
	}

	// Parse integer part
	intPart := parts[0]
	if intPart == "" {
		intPart = "0"
	}

	intValue, ok := new(big.Int).SetString(intPart, 10)
	if !ok {
		return nil, fmt.Errorf("invalid integer part: %s", intPart)
	}

	// Convert integer part to wei
	wei := new(big.Int).Mul(intValue, weiPerGRT)

	// Handle fractional part
	if len(parts) == 2 {
		fracPart := parts[1]
		// Pad or truncate to 18 decimals
		if len(fracPart) > 18 {
			fracPart = fracPart[:18]
		} else {
			fracPart = fracPart + strings.Repeat("0", 18-len(fracPart))
		}

		fracValue, ok := new(big.Int).SetString(fracPart, 10)
		if !ok {
			return nil, fmt.Errorf("invalid fractional part: %s", fracPart)
		}
		wei.Add(wei, fracValue)
	}

	return &Price{wei: wei}, nil
}

// Wei returns the price in wei
func (p *Price) Wei() *big.Int {
	if p == nil || p.wei == nil {
		return big.NewInt(0)
	}
	return new(big.Int).Set(p.wei)
}

// ToDecimalString returns the price as a decimal string
func (p *Price) ToDecimalString() string {
	if p == nil || p.wei == nil {
		return "0"
	}

	// Divide by 10^18 to get GRT value
	grt := new(big.Int).Div(p.wei, weiPerGRT)
	remainder := new(big.Int).Mod(p.wei, weiPerGRT)

	if remainder.Sign() == 0 {
		return grt.String()
	}

	// Format with fractional part
	fracStr := fmt.Sprintf("%018d", remainder)
	fracStr = strings.TrimRight(fracStr, "0")
	return fmt.Sprintf("%s.%s", grt.String(), fracStr)
}

// CalculateCost calculates the cost for a given quantity
func (p *Price) CalculateCost(quantity uint64) *big.Int {
	if p == nil || p.wei == nil {
		return big.NewInt(0)
	}
	return new(big.Int).Mul(p.wei, big.NewInt(int64(quantity)))
}

// IsZero returns true if the price is zero
func (p *Price) IsZero() bool {
	return p == nil || p.wei == nil || p.wei.Sign() == 0
}

// PricingConfig holds the pricing configuration for a provider
type PricingConfig struct {
	// PricePerBlock is the price per processed block in GRT
	PricePerBlock *Price `yaml:"-"`
	// PricePerByte is the price per byte transferred in GRT
	PricePerByte *Price `yaml:"-"`

	// YAML fields (strings for human-readable decimal values)
	PricePerBlockStr string `yaml:"price_per_block"`
	PricePerByteStr  string `yaml:"price_per_byte"`
}

// LoadPricingConfig loads pricing configuration from a YAML file
func LoadPricingConfig(path string) (*PricingConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading pricing config: %w", err)
	}

	return ParsePricingConfig(data)
}

// ParsePricingConfig parses pricing configuration from YAML bytes
func ParsePricingConfig(data []byte) (*PricingConfig, error) {
	var config PricingConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("parsing pricing config: %w", err)
	}

	// Convert string prices to Price objects
	var err error
	config.PricePerBlock, err = NewPriceFromDecimal(config.PricePerBlockStr)
	if err != nil {
		return nil, fmt.Errorf("invalid price_per_block: %w", err)
	}

	config.PricePerByte, err = NewPriceFromDecimal(config.PricePerByteStr)
	if err != nil {
		return nil, fmt.Errorf("invalid price_per_byte: %w", err)
	}

	return &config, nil
}

// CalculateUsageCost calculates the total cost for given usage
func (c *PricingConfig) CalculateUsageCost(blocksProcessed, bytesTransferred uint64) *big.Int {
	total := big.NewInt(0)

	if c.PricePerBlock != nil {
		total.Add(total, c.PricePerBlock.CalculateCost(blocksProcessed))
	}

	if c.PricePerByte != nil {
		total.Add(total, c.PricePerByte.CalculateCost(bytesTransferred))
	}

	return total
}

// DefaultPricingConfig returns a default pricing configuration
func DefaultPricingConfig() *PricingConfig {
	// Default: 0.000001 GRT per block (1 GRT per million blocks)
	// Default: 0.0000000001 GRT per byte (1 GRT per 10GB)
	blockPrice, _ := NewPriceFromDecimal("0.000001")
	bytePrice, _ := NewPriceFromDecimal("0.0000000001")

	return &PricingConfig{
		PricePerBlock:    blockPrice,
		PricePerByte:     bytePrice,
		PricePerBlockStr: "0.000001",
		PricePerByteStr:  "0.0000000001",
	}
}
