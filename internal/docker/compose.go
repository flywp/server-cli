package docker

import (
	"fmt"
	"os"
	"os/exec"

	"gopkg.in/yaml.v2"
)

type ComposeConfig struct {
	Services map[string]interface{} `yaml:"services"`
}

func isInteractive() bool {
	fileInfo, _ := os.Stdout.Stat()
	return (fileInfo.Mode() & os.ModeCharDevice) != 0
}

func RunCompose(composePath string, args ...string) error {
	baseArgs := []string{"compose", "-f", composePath}

	// Check if 'exec' is the first argument and add -T if not interactive
	if len(args) > 0 && args[0] == "exec" && !isInteractive() {
		baseArgs = append(baseArgs, "exec", "-T")
		args = args[1:]
	}

	fullArgs := append(baseArgs, args...)

	cmd := exec.Command("docker", fullArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func getContainerName(composePath string) (string, error) {
	data, err := os.ReadFile(composePath)
	if err != nil {
		return "", err
	}

	var config ComposeConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return "", err
	}

	if _, exists := config.Services["php"]; exists {
		return "php", nil
	} else if _, exists := config.Services["litespeed"]; exists {
		return "litespeed", nil
	}

	return "", fmt.Errorf("no suitable container found")
}

func RunWPCLI(composePath string, args []string) error {
	containerName, err := getContainerName(composePath)
	if err != nil {
		return err
	}

	wpArgs := append([]string{"exec", containerName, "wp"}, args...)
	return RunCompose(composePath, wpArgs...)
}
