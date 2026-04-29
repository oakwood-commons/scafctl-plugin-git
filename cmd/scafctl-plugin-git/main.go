// Package main is the entry point for the scafctl-plugin-git plugin.
package main

import (
	"github.com/oakwood-commons/scafctl-plugin-git/internal/git"

	sdkplugin "github.com/oakwood-commons/scafctl-plugin-sdk/plugin"
)

func main() {
	sdkplugin.Serve(git.NewPlugin())
}
