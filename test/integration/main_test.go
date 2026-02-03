package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// getTestDir returns the absolute path to the integration test directory
func getTestDir() string {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		panic("failed to get current file path")
	}
	return filepath.Dir(currentFile)
}

func TestMain(m *testing.M) {
	if err := ensureContractArtifacts(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to ensure contract artifacts: %v\n", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

// ensureContractArtifacts checks if contract artifacts exist, builds them if needed
func ensureContractArtifacts() error {
	testDir := getTestDir()
	artifactsDir := filepath.Join(testDir, "testdata", "contracts")
	collectorArtifact := filepath.Join(artifactsDir, "GraphTallyCollector.json")

	// Check if artifacts already exist
	forceBuild := os.Getenv("FORCE_CONTRACTS_BUILD") == "true"
	if !forceBuild {
		if _, err := os.Stat(collectorArtifact); err == nil {
			fmt.Println("Contract artifacts found, skipping build")
			return nil
		}
	}

	fmt.Println("Building contract artifacts...")

	// Ensure output directory exists
	if err := os.MkdirAll(artifactsDir, 0755); err != nil {
		return fmt.Errorf("creating artifacts directory: %w", err)
	}

	// Run build container
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	buildDir := filepath.Join(testDir, "build")

	req := testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{
			Context:       buildDir,
			Dockerfile:    "Dockerfile",
			PrintBuildLog: true,
		},
		Mounts: testcontainers.ContainerMounts{
			testcontainers.BindMount(artifactsDir, "/output"),
		},
		WaitingFor: wait.ForLog("Build complete!").
			WithStartupTimeout(5 * time.Minute),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return fmt.Errorf("starting build container: %w", err)
	}
	defer container.Terminate(ctx)

	// Wait for container to complete by checking state
	for {
		state, err := container.State(ctx)
		if err != nil {
			return fmt.Errorf("getting container state: %w", err)
		}

		if !state.Running {
			if state.ExitCode != 0 {
				return fmt.Errorf("build container exited with code %d", state.ExitCode)
			}
			break
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for build container")
		case <-time.After(1 * time.Second):
			continue
		}
	}

	// Verify artifact was created
	if _, err := os.Stat(collectorArtifact); err != nil {
		return fmt.Errorf("artifact not found after build: %w", err)
	}

	fmt.Println("Contract artifacts built successfully")
	return nil
}
