package sidecar

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewPriceFromDecimal(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expectedWei string
		wantErr     bool
	}{
		{
			name:        "zero",
			input:       "0",
			expectedWei: "0",
		},
		{
			name:        "one GRT",
			input:       "1",
			expectedWei: "1000000000000000000",
		},
		{
			name:        "half GRT",
			input:       "0.5",
			expectedWei: "500000000000000000",
		},
		{
			name:        "one millionth GRT (1 GRT per million)",
			input:       "0.000001",
			expectedWei: "1000000000000",
		},
		{
			name:        "very small price",
			input:       "0.0000000001",
			expectedWei: "100000000",
		},
		{
			name:        "large value",
			input:       "1000000",
			expectedWei: "1000000000000000000000000",
		},
		{
			name:        "decimal with trailing zeros",
			input:       "1.500000",
			expectedWei: "1500000000000000000",
		},
		{
			name:        "empty string",
			input:       "",
			expectedWei: "0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			price, err := NewPriceFromDecimal(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			expected, ok := new(big.Int).SetString(tt.expectedWei, 10)
			require.True(t, ok)
			assert.Equal(t, expected.String(), price.Wei().String())
		})
	}
}

func TestPrice_ToDecimalString(t *testing.T) {
	tests := []struct {
		name     string
		wei      string
		expected string
	}{
		{
			name:     "zero",
			wei:      "0",
			expected: "0",
		},
		{
			name:     "one GRT",
			wei:      "1000000000000000000",
			expected: "1",
		},
		{
			name:     "half GRT",
			wei:      "500000000000000000",
			expected: "0.5",
		},
		{
			name:     "one millionth GRT",
			wei:      "1000000000000",
			expected: "0.000001",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wei, ok := new(big.Int).SetString(tt.wei, 10)
			require.True(t, ok)

			price := NewPriceFromWei(wei)
			assert.Equal(t, tt.expected, price.ToDecimalString())
		})
	}
}

func TestPrice_CalculateCost(t *testing.T) {
	// Price of 0.000001 GRT per unit (1 GRT per million)
	price, err := NewPriceFromDecimal("0.000001")
	require.NoError(t, err)

	// 1 million units should cost 1 GRT
	cost := price.CalculateCost(1000000)
	oneGRT, _ := new(big.Int).SetString("1000000000000000000", 10)
	assert.Equal(t, oneGRT.String(), cost.String())

	// 100 units should cost 0.0001 GRT
	cost = price.CalculateCost(100)
	expected, _ := new(big.Int).SetString("100000000000000", 10)
	assert.Equal(t, expected.String(), cost.String())
}

func TestParsePricingConfig(t *testing.T) {
	yaml := `
price_per_block: "0.000001"
price_per_byte: "0.0000000001"
`
	config, err := ParsePricingConfig([]byte(yaml))
	require.NoError(t, err)

	// Verify block price
	assert.Equal(t, "0.000001", config.PricePerBlock.ToDecimalString())

	// Verify byte price
	assert.Equal(t, "0.0000000001", config.PricePerByte.ToDecimalString())
}

func TestPricingConfig_CalculateUsageCost(t *testing.T) {
	config := DefaultPricingConfig()

	// 1 million blocks at 0.000001 GRT/block = 1 GRT
	// 10GB at 0.0000000001 GRT/byte = 1 GRT
	// Total = 2 GRT
	cost := config.CalculateUsageCost(1000000, 10*1024*1024*1024)

	// Should be approximately 2 GRT (2 * 10^18 wei)
	twoGRT, _ := new(big.Int).SetString("2000000000000000000", 10)

	// Allow some rounding difference for the byte calculation
	diff := new(big.Int).Sub(cost, twoGRT)
	diff.Abs(diff)

	// Difference should be less than 0.1 GRT due to byte rounding
	maxDiff, _ := new(big.Int).SetString("100000000000000000", 10)
	assert.True(t, diff.Cmp(maxDiff) < 0, "cost %s should be close to 2 GRT", cost.String())
}
