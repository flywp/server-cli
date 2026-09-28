package cmd

import (
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/flywp/server-cli/internal/agent"
	"github.com/flywp/server-cli/internal/metrics"
	"github.com/spf13/cobra"
)

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Run the FlyWP monitoring agent",
}

// agentRunCmd does not need Docker: the agent must also report when Docker
// is down.
var agentRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Run the monitoring agent until it is stopped",
	Long: `Run the FlyWP monitoring agent until it is stopped. systemd starts this
command (fly-agent.service). The agent reads FLY_AGENT_URL, FLY_AGENT_TOKEN,
FLY_AGENT_SERVER_ID and STATE_DIRECTORY from the environment.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := agent.ConfigFromEnv(os.Getenv)
		if err != nil {
			return err
		}

		ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGTERM, os.Interrupt)
		defer stop()

		log := slog.New(slog.NewTextHandler(os.Stderr, nil))
		return agent.Run(ctx, cfg, log, metrics.New("/", cfg.StateDir, log))
	},
}

func init() {
	agentCmd.AddCommand(agentRunCmd)
	rootCmd.AddCommand(agentCmd)
}
