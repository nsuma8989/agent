package clicommand

import (
	"fmt"
	"os"
	"time"

	"github.com/buildkite/agent/v3/agent"
	"github.com/urfave/cli"
)

const lockAcquireHelpDescription = `Usage:

   buildkite-agent lock acquire [key]

Description:
   Acquires the lock for the given key. ′lock acquire′ will wait (potentially
   forever) until it can acquire the lock, if the lock is already held by
   another process. If multiple processes are waiting for the same lock, there
   is no ordering guarantee of which one will be given the lock next.

Examples:

   $ buildkite-agent lock acquire llama
   $ critical_section()
   $ buildkite-agent lock release llama

`

type LockAcquireConfig struct{}

var LockAcquireCommand = cli.Command{
	Name:        "acquire",
	Usage:       "Acquires a lock from the agent leader",
	Description: lockAcquireHelpDescription,
	Action:      lockAcquireAction,
}

func lockAcquireAction(c *cli.Context) error {
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

	for {
		done, err := cli.CompareAndSwap(key, "", "1")
		if err != nil {
			fmt.Fprintf(c.App.ErrWriter, "Error performing compare-and-swap: %v\n", err)
			os.Exit(1)
		}

		if done {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
}
