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

## Notes

- All builds must pass before committing
- Run `go vet` to ensure code quality
- Use `go mod tidy` after updating dependencies
- Test coverage exists for event.go and utils.go
- Known bug: utils.go line 49 uses `count` before it's set (results in +Inf for average)