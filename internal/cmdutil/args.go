package cmdutil

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

// errMissingAppID names the positional token most commands need first.
var errMissingAppID = errors.New("requires an application id (e.g. app_123)")

// AppIDArgs validates the single <app-id> positional argument.
func AppIDArgs() cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		switch {
		case len(args) == 0:
			return errMissingAppID
		case len(args) > 1:
			return fmt.Errorf("accepts one application id, received %d arguments", len(args))
		}

		return nil
	}
}

// AppIDPlusArgs validates <app-id> plus at least one more token. what names the
// missing token, for example "KEY=VALUE pair".
func AppIDPlusArgs(what string) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		switch len(args) {
		case 0:
			return errMissingAppID
		case 1:
			return errors.New("requires at least one " + what)
		}

		return nil
	}
}
