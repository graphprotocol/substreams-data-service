# Agent Operational Guide

## Build and Test Commands

```bash
# Build the project
go build ./...

# Run go vet checks
go vet ./...

# Run tests (note: no test files exist yet)
go test ./...

# Format
gofmt -w .

# Update dependencies
go get -u ./...
go mod tidy
```

- ALWAYS Use `gofmt` after finish creating/editing a Golang file once you are ready to run tests or make any other external validations but after it compiles correctly.

## Project Structure

- Main package: root directory
- Commands: `cmd/sf_analyzer/` and `cmd/sf_comparator/`
- Metrics: `metrics/`

## Environment

- Go Version: 1.24.0 (toolchain go1.24.4)
- Build Status: PASSING
- Test Status: PASSING (21 tests)
- Only use latest Golang features instead of older idioms (slices, maps, iter, any, generics, etc.)

## CLI Flag Parsing and Error Handling

When parsing CLI flags that require validation:

- Use `cli.Ensure` for required field presence checks (preferred)
- Use non-Must parsing functions and handle errors with `cli.NoError`
- Provide contextual error messages - adjust based on whether field is required or optional

```go
// Preferred - check required fields with cli.Ensure
cli.Ensure(signerKeyHex != "", "<signer-key> is required")

// Good - parsing with contextual error for required field
addr, err := parseAddress(addrHex)
cli.NoError(err, "invalid <service-provider> address %q, it is required", addrHex)

// Good - parsing with contextual error for optional field
if configPath != "" {
    cfg, err := loadConfig(configPath)
    cli.NoError(err, "unable to load config from %q", configPath)
}

// Avoid - Must functions panic without context
addr := MustParseAddress(hex)

// Avoid - returns error without user-friendly context
if err != nil {
    return err
}
```

## Notes

- All builds must pass before committing
- Run `go vet` to ensure code quality
- Use `go mod tidy` after updating dependencies
- Test coverage exists for event.go and utils.go
- Known bug: utils.go line 49 uses `count` before it's set (results in +Inf for average)