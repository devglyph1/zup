package setup

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// Step represents a single setup step from the YAML config.
type Step struct {
	Desc string `yaml:"desc"`
	Cmd  string `yaml:"cmd"`
	Meta string `yaml:"meta,omitempty"`
	Mode string `yaml:"mode,omitempty"`
}

// Config represents the overall YAML configuration.
type Config struct {
	Setup []Step `yaml:"setup"`
}

// FixResponse represents the structure of a fix suggestion from OpenAI.
type FixResponse struct {
	Fix         string `json:"fix"`
	Explanation string `json:"explanation"`
}

// RunCmd is the main Cobra command for running the setup. It loads the YAML configuration and executes all setup steps defined in zup.yaml.
var RunCmd = &cobra.Command{
	Use:   "run",
	Short: "Run setup steps defined in zup.yaml",
	Run: func(cmd *cobra.Command, args []string) {
		runSetup()
	},
}

/*
runSetup is the entry point for executing the setup process as defined in the YAML configuration file (zup.yaml).

This function attempts to load the configuration file, parse its contents into a Config struct, and then iterates over each setup step defined in the file. For each step, it delegates execution to the executeStep function, which handles command execution and error recovery. If the configuration file cannot be loaded or parsed, an error message is printed and the setup process is aborted.
*/
func runSetup() {
	cfg, err := loadConfig("zup.yaml")
	if err != nil {
		fmt.Println("Failed to load zup.yaml:", err)
		return
	}
	for _, step := range cfg.Setup {
		executeStep(step)
	}
}

/*
loadConfig reads the YAML configuration from the specified file path and unmarshals it into a Config struct.
It returns a pointer to the Config struct and a possible error. If the file cannot be read (e.g., due to missing file or permissions) or if the YAML is invalid, an error is returned. This function is responsible for ensuring that the setup steps are loaded into memory before execution begins.
*/
func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

/*
executeStep is responsible for running a single setup step as defined in the configuration.
It prints the step's description and command to the terminal for user visibility. The function then attempts to execute the command using fixAndRunCommandWithMeta, which handles both normal execution and error recovery. If the command fails and cannot be fixed, an error message is displayed. This function ensures that each step is clearly communicated to the user and that failures are handled gracefully.
*/
func executeStep(step Step) {
	mode := step.Mode
	if mode == "" {
		mode = "same-terminal"
	}
	cyan := color.New(color.FgCyan, color.Bold).SprintFunc()
	fmt.Printf("\n%s %s\n%s %s\n",
		cyan("🔧 Step:"), step.Desc,
		cyan("Command:"), step.Cmd,
	)
	if err := fixAndRunCommandWithMeta(step.Cmd, step.Meta, mode); err != nil {
		color.New(color.FgRed, color.Bold).Printf("\n❌ Command ultimately failed after all fixes: %v\n", err)
	}
}

/*
fixAndRunCommandWithMeta attempts to execute a shell command in the specified mode (e.g., same-terminal or background).
If the command fails, it queries OpenAI for a suggested fix, presents the fix and its explanation to the user, and prompts the user to apply the fix. If the user agrees, the fix is applied recursively until the command succeeds or the user declines further fixes. This function is central to the tool's self-healing capability, allowing for interactive troubleshooting and automated recovery from common errors.
*/
func fixAndRunCommandWithMeta(command, meta, mode string) error {
	err := runCommandWithMode(command, mode)
	if err == nil {
		return nil
	}
	color.New(color.FgRed, color.Bold).Printf("\n❌ Command failed: %s\n", err.Error())
	errMsg := err.Error()
	fix, explanation := getFixFromOpenAIWithMeta(command, errMsg, meta)
	color.New(color.FgYellow, color.Bold).Printf("\n💡 Suggested Fix: %s\n", fix)
	color.New(color.FgHiBlack).Printf("📝 %s\n", explanation)
	if askYesNo(color.New(color.FgGreen, color.Bold).Sprintf("Apply this fix?")) {
		if fixErr := fixAndRunCommandWithMeta(fix, meta, ""); fixErr == nil {
			if mode == "background" {
				if !waitForBinary(getBinaryName(command), 10, time.Second) {
					color.New(color.FgRed, color.Bold).Printf("\n❌ Binary '%s' still not found after fix. Please ensure it is installed and in your PATH.\n", getBinaryName(command))
					return fmt.Errorf("binary '%s' still not found after fix", getBinaryName(command))
				}
			}
			color.New(color.FgGreen, color.Bold).Println("\n✅ Fix applied. Retrying original command...")
			return fixAndRunCommandWithMeta(command, meta, mode)
		} else {
			color.New(color.FgRed, color.Bold).Printf("\n❌ Fix command failed: %v\n", fixErr)
			return fixErr
		}
	}
	return err
}

/*
runCommandWithMode executes a shell command according to the specified mode.
If the mode is 'background', the command is run using nohup so it continues running after the terminal closes, and output is redirected to a log file. The function checks for the existence of the required binary before attempting execution. In the default mode, the command is run in the current terminal session. Errors are returned if the binary is missing or the command fails. This function abstracts the details of command execution modes for the rest of the setup process.
*/
func runCommandWithMode(command, mode string) error {
	switch mode {
	case "background":
		binary := getBinaryName(command)
		if binary == "" {
			return fmt.Errorf("could not determine binary for background command: %s", command)
		}
		if _, err := exec.LookPath(binary); err != nil {
			return fmt.Errorf("binary '%s' not found: %w", binary, err)
		}
		color.New(color.FgCyan).Printf("\n🚀 Running '%s' in background...\n", command)
		backgroundCmd := fmt.Sprintf("nohup %s > background_command.log 2>&1 &", command)
		return runCommand(backgroundCmd, true)
	default:
		return runCommand(command, false)
	}
}

/*
runCommand executes a shell command using bash, with optional output suppression.
If suppressOutput is true, both stdout and stderr are captured and not printed to the terminal; otherwise, output is streamed directly to the terminal. The function returns an error if the command fails, including any captured output for debugging. This function provides a flexible way to run shell commands and handle their output as needed by the setup process.
*/
func runCommand(command string, suppressOutput bool) error {
	cmd := exec.Command("bash", "-c", command)
	cmd.Stdin = os.Stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if !suppressOutput {
		cmd.Stdout = os.Stdout
	}
	if err := cmd.Run(); err != nil {
		if suppressOutput {
			return errors.New(strings.TrimSpace(stdout.String() + "\n" + stderr.String()))
		}
		return errors.New(stderr.String())
	}
	if !suppressOutput && stdout.Len() > 0 {
		fmt.Print(stdout.String())
	}
	return nil
}

/*
askYesNo prompts the user with a yes/no question and waits for input from stdin.
The function returns true if the user responds with 'y' or 'yes' (case-insensitive), and false for any other response. This is used to confirm user intent before applying potentially impactful fixes or changes during the setup process.
*/
func askYesNo(prompt string) bool {
	color.New(color.FgHiMagenta, color.Bold).Printf("%s (y/n): ", prompt)
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	resp := strings.ToLower(scanner.Text())
	return resp == "y" || resp == "yes"
}

/*
getBinaryName extracts the first word from a shell command string, which is assumed to be the binary or executable name.
If the command string is empty, it returns an empty string. This utility is used to check for the presence of required binaries before attempting to run commands, especially in background mode.
*/
func getBinaryName(cmd string) string {
	parts := strings.Fields(cmd)
	if len(parts) > 0 {
		return parts[0]
	}
	return ""
}

/*
waitForBinary repeatedly checks if a given binary is available in the system PATH.
It retries up to maxTries times, waiting for the specified delay between attempts. Returns true if the binary is found within the allotted attempts, or false otherwise. This is useful for waiting on installations or updates to complete before proceeding with dependent steps.
*/
func waitForBinary(binary string, maxTries int, delay time.Duration) bool {
	for i := 0; i < maxTries; i++ {
		if _, err := exec.LookPath(binary); err == nil {
			return true
		}
		time.Sleep(delay)
	}
	return false
}
