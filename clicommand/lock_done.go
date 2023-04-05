package clicommand

import (
	"fmt"
	"os"

	"github.com/buildkite/agent/v3/agent"
	"github.com/urfave/cli"
)

const lockDoneHelpDescription = `Usage:

   buildkite-agent lock release [key]

Description:
   Completes a do-once lock. This should only be used by the process performing
   the work.

Examples:

   #!/bin/bash
   if buildkite-agent lock do llama ; then
	  setup_code()
	  buildkite-agent lock done llama
   fi


`

type LockDoneConfig struct{}

var LockDoneCommand = cli.Command{
	Name:        "release",
	Usage:       "Releases a previously-acquired lock",
	Description: lockDoneHelpDescription,
	Action:      lockDoneAction,
}

func lockDoneAction(c *cli.Context) error {
	if c.NArg() != 1 {
		fmt.Fprint(c.App.ErrWriter, lockDoneHelpDescription)
		os.Exit(1)
	}
	key := c.Args()[0]

	cli, err := agent.NewLeaderClient()
	if err != nil {
		fmt.Fprintf(c.App.ErrWriter, lockClientErrMessage, err)
		os.Exit(1)
	}

	done, err := cli.CompareAndSwap(key, "1", "2")
	if err != nil {
		fmt.Fprintf(c.App.ErrWriter, "Error performing compare-and-swap: %v\n", err)
		os.Exit(1)
	}

	if !done {
		fmt.Fprintln(c.App.ErrWriter, "Lock in invalid state to mark complete - investigate with 'lock get'")
		os.Exit(1)
	}
	return nil
}
