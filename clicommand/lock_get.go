package clicommand

import (
	"fmt"
	"os"

	"github.com/buildkite/agent/v3/agent"
	"github.com/urfave/cli"
)

const lockClientErrMessage = `Could not connect to Leader API: %v
This command can only be used when at least one agent is running with the
"leader-api" experiment enabled.
`

const lockGetHelpDescription = `Usage:

   buildkite-agent lock get [key]

Description:
   Retrieves the value of a lock key. Any key not in use returns an empty 
   string.
   
   ′lock get′ is generally only useful for inspecting lock state, as the value
   can change concurrently. To acquire or release a lock, use ′lock acquire′ and
   ′lock release′.

Examples:

   $ buildkite-agent lock get llama
   Kuzco

`

type LockGetConfig struct{}

var LockGetCommand = cli.Command{
	Name:        "get",
	Usage:       "Gets a lock value from the agent leader",
	Description: lockGetHelpDescription,
	Action:      lockGetAction,
}

func lockGetAction(c *cli.Context) error {
	if c.NArg() != 1 {
		fmt.Fprint(c.App.ErrWriter, lockGetHelpDescription)
		os.Exit(1)
	}
	key := c.Args()[0]

	cli, err := agent.NewLeaderClient()
	if err != nil {
		fmt.Fprintf(c.App.ErrWriter, lockClientErrMessage, err)
		os.Exit(1)
	}

	v, err := cli.Get(key)
	if err != nil {
		fmt.Fprintf(c.App.ErrWriter, "Error from leader client: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintln(c.App.Writer, v)
	return nil
}
