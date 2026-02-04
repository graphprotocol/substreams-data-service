package devenv

import "math/big"

// Reporter is an interface for reporting progress during devenv startup
type Reporter interface {
	ReportProgress(message string)
}

// NoopReporter is a reporter that does nothing
type NoopReporter struct{}

func (NoopReporter) ReportProgress(message string) {}

// Config holds configuration for the development environment
type Config struct {
	// ChainID is the chain ID for the Anvil network (default: 1337)
	ChainID uint64
	// EscrowAmount is the default amount to deposit in escrow (default: 10,000 GRT)
	EscrowAmount *big.Int
	// ProvisionAmount is the default provision amount (default: 1,000 GRT)
	ProvisionAmount *big.Int
	// Reporter is used to report progress during startup
	Reporter Reporter
}

// DefaultConfig returns the default configuration
func DefaultConfig() *Config {
	escrow := new(big.Int)
	escrow.SetString("10000000000000000000000", 10) // 10,000 GRT

	provision := new(big.Int)
	provision.SetString("1000000000000000000000", 10) // 1,000 GRT

	return &Config{
		ChainID:         1337,
		EscrowAmount:    escrow,
		ProvisionAmount: provision,
		Reporter:        NoopReporter{},
	}
}

// Option is a function that modifies a Config
type Option func(*Config)

// WithChainID sets the chain ID
func WithChainID(chainID uint64) Option {
	return func(c *Config) {
		c.ChainID = chainID
	}
}

// WithEscrowAmount sets the default escrow amount
func WithEscrowAmount(amount *big.Int) Option {
	return func(c *Config) {
		c.EscrowAmount = amount
	}
}

// WithProvisionAmount sets the default provision amount
func WithProvisionAmount(amount *big.Int) Option {
	return func(c *Config) {
		c.ProvisionAmount = amount
	}
}

// WithReporter sets the progress reporter
func WithReporter(reporter Reporter) Option {
	return func(c *Config) {
		c.Reporter = reporter
	}
}
